package collector

import (
	"context"
	"strings"
	"sync"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"
	"traffcount/internal/service"
	"traffcount/internal/storage"
)

const RingBufferSize = 60

// RatePoint represents a 1-second bandwidth sample.
type RatePoint struct {
	Timestamp int64  `json:"ts"`
	RxBps     uint64 `json:"rxBps"`     // Receive bytes per second
	TxBps     uint64 `json:"txBps"`     // Transmit bytes per second
	RxPps     uint64 `json:"rxPps"`     // Receive packets per second
	TxPps     uint64 `json:"txPps"`     // Transmit packets per second
}

// LiveInterfaceSnapshot holds the current status and 60-second history of a NIC.
type LiveInterfaceSnapshot struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Description   string      `json:"description"`
	MAC           string      `json:"mac"`
	IPv4          string      `json:"ipv4"`
	IPv6          string      `json:"ipv6"`
	LinkSpeedBits uint64      `json:"linkSpeedBits"`
	IsUp          bool        `json:"isUp"`
	IsLoopback    bool        `json:"isLoopback"`
	CurrentRxBps  uint64      `json:"currentRxBps"`
	CurrentTxBps  uint64      `json:"currentTxBps"`
	SessionRx     uint64      `json:"sessionRx"`
	SessionTx     uint64      `json:"sessionTx"`
	TotalRx       uint64      `json:"totalRx"`
	TotalTx       uint64      `json:"totalTx"`
	History       []RatePoint `json:"history"`
}

// RealtimePayload is broadcast every second to WebSocket subscribers and tray tooltip.
type RealtimePayload struct {
	Timestamp   int64                   `json:"timestamp"`
	Source      string                  `json:"source"` // "service" or "app"
	TotalRxBps  uint64                  `json:"totalRxBps"`
	TotalTxBps  uint64                  `json:"totalTxBps"`
	TotalHist   []RatePoint             `json:"totalHistory"`
	Interfaces  []LiveInterfaceSnapshot `json:"interfaces"`
	Diagnostics DiagnosticsStatus       `json:"diagnostics"`
}

type rawCounter struct {
	bytesRecv   uint64
	bytesSent   uint64
	packetsRecv uint64
	packetsSent uint64
	sessionRx   uint64
	sessionTx   uint64
	history     []RatePoint
}

// ComputeDelta calculates counter delta safely handling 0-resets/reboots.
func ComputeDelta(prev, curr uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	// Counter reset (e.g., adapter reset or reboot)
	return curr
}

// MakeInterfaceID creates a clean, stable identifier for a NIC.
func MakeInterfaceID(name, mac string) string {
	cleanName := strings.TrimSpace(name)
	if mac != "" && mac != "00:00:00:00:00:00" {
		return cleanName + "_" + strings.ReplaceAll(mac, ":", "")
	}
	return cleanName
}

// Collector polls network interfaces at 1Hz, maintains a 60s ring buffer, and flushes deltas to SQLite.
type Collector struct {
	store *storage.Store

	mu          sync.RWMutex
	counters    map[string]*rawCounter
	metaCache   map[string]AdapterMeta
	metaUpdated time.Time
	totalHist   []RatePoint
	pendingDB   map[string]*storage.TrafficDelta
	lastPayload RealtimePayload

	subMu       sync.Mutex
	subscribers map[chan RealtimePayload]struct{}

	alertMu      sync.Mutex
	newAppAlerts []string
	appsSeeded   bool
}

// New creates a new Collector backed by the provided SQLite Store.
func New(store *storage.Store) *Collector {
	now := time.Now().Unix()
	initHist := make([]RatePoint, RingBufferSize)
	for i := 0; i < RingBufferSize; i++ {
		initHist[i] = RatePoint{Timestamp: now - int64(RingBufferSize-1-i)}
	}
	if store != nil {
		if blocked, err := store.GetBlockedApps(); err == nil {
			SetBlockedAppsCache(blocked)
		}
	}
	return &Collector{
		store:       store,
		counters:    make(map[string]*rawCounter),
		metaCache:   make(map[string]AdapterMeta),
		totalHist:   initHist,
		pendingDB:   make(map[string]*storage.TrafficDelta),
		subscribers: make(map[chan RealtimePayload]struct{}),
	}
}

// PopNewAppAlerts returns and clears any newly detected network applications for tray notifications.
func (c *Collector) PopNewAppAlerts() []string {
	c.alertMu.Lock()
	defer c.alertMu.Unlock()
	if len(c.newAppAlerts) == 0 {
		return nil
	}
	out := c.newAppAlerts
	c.newAppAlerts = nil
	return out
}

// Subscribe returns a channel that receives 1Hz RealtimePayload updates.
func (c *Collector) Subscribe() (chan RealtimePayload, func()) {
	ch := make(chan RealtimePayload, 4)
	c.subMu.Lock()
	c.subscribers[ch] = struct{}{}
	c.subMu.Unlock()

	unsubscribe := func() {
		c.subMu.Lock()
		if _, ok := c.subscribers[ch]; ok {
			delete(c.subscribers, ch)
			close(ch)
		}
		c.subMu.Unlock()
	}
	return ch, unsubscribe
}

// LatestSnapshot returns the most recent RealtimePayload.
func (c *Collector) LatestSnapshot() RealtimePayload {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastPayload
}

// Run starts the 1Hz polling loop and 15-second SQLite flush loop until ctx is canceled.
func (c *Collector) Run(ctx context.Context) {
	// Initial sample to seed counters and connectivity probe
	go ProbeConnectivity()
	go func() {
		_ = GetActiveConnections("", false)
		c.FlushToDB()
	}()
	c.sample(true)

	pollTicker := time.NewTicker(1 * time.Second)
	flushTicker := time.NewTicker(15 * time.Second)
	diagTicker := time.NewTicker(3 * time.Second)
	procTicker := time.NewTicker(4 * time.Second)
	defer pollTicker.Stop()
	defer flushTicker.Stop()
	defer diagTicker.Stop()
	defer procTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.FlushToDB()
			c.closeSubscribers()
			return
		case <-pollTicker.C:
			payload := c.sample(false)
			c.broadcast(payload)
		case <-flushTicker.C:
			c.FlushToDB()
		case <-diagTicker.C:
			go ProbeConnectivity()
		case <-procTicker.C:
			go func() {
				_ = GetActiveConnections("", false)
			}()
		}
	}
}

func (c *Collector) broadcast(p RealtimePayload) {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for ch := range c.subscribers {
		select {
		case ch <- p:
		default:
		}
	}
}

func (c *Collector) closeSubscribers() {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	for ch := range c.subscribers {
		delete(c.subscribers, ch)
		close(ch)
	}
}

func collectorSource() string {
	if service.IsWindowsServiceProcess() {
		return "service"
	}
	return "app"
}

func (c *Collector) sample(isSeed bool) RealtimePayload {
	now := time.Now()
	unixNow := now.Unix()

	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.metaUpdated) > 10*time.Second || len(c.metaCache) == 0 {
		c.metaCache = GetWindowsAdapters()
		c.metaUpdated = now
	}

	settings, _ := c.store.GetSettings()

	ioCounters, err := psnet.IOCounters(true)
	if err != nil {
		return c.lastPayload
	}

	var totalRxBps, totalTxBps uint64
	var totalRxPps, totalTxPps uint64
	var snapshots []LiveInterfaceSnapshot

	for _, ioStat := range ioCounters {
		meta, hasMeta := c.metaCache[ioStat.Name]
		if !hasMeta {
			meta = AdapterMeta{
				Name:        ioStat.Name,
				Description: ioStat.Name,
				IsUp:        true,
			}
		}

		isLoopback := meta.IsLoopback || strings.Contains(strings.ToLower(ioStat.Name), "loopback")
		if settings.HideLoopback && isLoopback {
			continue
		}

		ifaceID := MakeInterfaceID(ioStat.Name, meta.MAC)
		rc, exists := c.counters[ifaceID]
		if !exists {
			hist := make([]RatePoint, RingBufferSize)
			for i := 0; i < RingBufferSize; i++ {
				hist[i] = RatePoint{Timestamp: unixNow - int64(RingBufferSize-1-i)}
			}
			rc = &rawCounter{
				bytesRecv:   ioStat.BytesRecv,
				bytesSent:   ioStat.BytesSent,
				packetsRecv: ioStat.PacketsRecv,
				packetsSent: ioStat.PacketsSent,
				history:     hist,
			}
			c.counters[ifaceID] = rc

			// Ensure interface is registered in pendingDB even if 0 traffic so it appears in DB list
			if _, ok := c.pendingDB[ifaceID]; !ok {
				c.pendingDB[ifaceID] = &storage.TrafficDelta{
					IfaceID:     ifaceID,
					Name:        ioStat.Name,
					Description: meta.Description,
					MAC:         meta.MAC,
					Timestamp:   now,
				}
			}
		}

		var rxDelta, txDelta, rxPktDelta, txPktDelta uint64
		if !isSeed && exists {
			rxDelta = ComputeDelta(rc.bytesRecv, ioStat.BytesRecv)
			txDelta = ComputeDelta(rc.bytesSent, ioStat.BytesSent)
			rxPktDelta = ComputeDelta(rc.packetsRecv, ioStat.PacketsRecv)
			txPktDelta = ComputeDelta(rc.packetsSent, ioStat.PacketsSent)

			rc.sessionRx += rxDelta
			rc.sessionTx += txDelta

			rc.bytesRecv = ioStat.BytesRecv
			rc.bytesSent = ioStat.BytesSent
			rc.packetsRecv = ioStat.PacketsRecv
			rc.packetsSent = ioStat.PacketsSent

			// Accumulate for DB persistence
			pend, ok := c.pendingDB[ifaceID]
			if !ok {
				pend = &storage.TrafficDelta{
					IfaceID:     ifaceID,
					Name:        ioStat.Name,
					Description: meta.Description,
					MAC:         meta.MAC,
				}
				c.pendingDB[ifaceID] = pend
			}
			pend.RxBytes += rxDelta
			pend.TxBytes += txDelta
			pend.Timestamp = now
		}

		pt := RatePoint{
			Timestamp: unixNow,
			RxBps:     rxDelta,
			TxBps:     txDelta,
			RxPps:     rxPktDelta,
			TxPps:     txPktDelta,
		}
		if len(rc.history) >= RingBufferSize {
			rc.history = append(rc.history[1:], pt)
		} else {
			rc.history = append(rc.history, pt)
		}

		if !isLoopback {
			totalRxBps += rxDelta
			totalTxBps += txDelta
			totalRxPps += rxPktDelta
			totalTxPps += txPktDelta
		}

		histCopy := make([]RatePoint, len(rc.history))
		copy(histCopy, rc.history)

		snapshots = append(snapshots, LiveInterfaceSnapshot{
			ID:            ifaceID,
			Name:          ioStat.Name,
			Description:   meta.Description,
			MAC:           meta.MAC,
			IPv4:          meta.IPv4,
			IPv6:          meta.IPv6,
			LinkSpeedBits: meta.LinkSpeed,
			IsUp:          meta.IsUp,
			IsLoopback:    isLoopback,
			CurrentRxBps:  rxDelta,
			CurrentTxBps:  txDelta,
			SessionRx:     rc.sessionRx,
			SessionTx:     rc.sessionTx,
			TotalRx:       ioStat.BytesRecv,
			TotalTx:       ioStat.BytesSent,
			History:       histCopy,
		})
	}

	totalPt := RatePoint{
		Timestamp: unixNow,
		RxBps:     totalRxBps,
		TxBps:     totalTxBps,
		RxPps:     totalRxPps,
		TxPps:     totalTxPps,
	}
	if len(c.totalHist) >= RingBufferSize {
		c.totalHist = append(c.totalHist[1:], totalPt)
	} else {
		c.totalHist = append(c.totalHist, totalPt)
	}

	totalHistCopy := make([]RatePoint, len(c.totalHist))
	copy(totalHistCopy, c.totalHist)

	payload := RealtimePayload{
		Timestamp:   unixNow,
		Source:      collectorSource(),
		TotalRxBps:  totalRxBps,
		TotalTxBps:  totalTxBps,
		TotalHist:   totalHistCopy,
		Interfaces:  snapshots,
		Diagnostics: LatestDiagnostics(),
	}
	c.lastPayload = payload
	return payload
}

// FlushToDB writes accumulated traffic deltas and per-application traffic deltas to SQLite.
func (c *Collector) FlushToDB() {
	c.mu.Lock()
	var batch []storage.TrafficDelta
	if len(c.pendingDB) > 0 {
		batch = make([]storage.TrafficDelta, 0, len(c.pendingDB))
		for _, d := range c.pendingDB {
			batch = append(batch, *d)
		}
		c.pendingDB = make(map[string]*storage.TrafficDelta)
	}
	c.mu.Unlock()

	if len(batch) > 0 {
		_ = c.store.RecordTrafficBatch(batch)
	}

	procDeltas := TakePendingProcessDeltas()
	if len(procDeltas) > 0 {
		newApps, err := c.store.RecordProcessTrafficBatch(procDeltas)
		if err == nil {
			c.alertMu.Lock()
			if c.appsSeeded && len(newApps) > 0 {
				settings, _ := c.store.GetSettings()
				if settings.NewAppAlertEnabled {
					c.newAppAlerts = append(c.newAppAlerts, newApps...)
					if len(c.newAppAlerts) > 20 {
						c.newAppAlerts = c.newAppAlerts[len(c.newAppAlerts)-20:]
					}
				}
			}
			c.appsSeeded = true
			c.alertMu.Unlock()
		}
	}
}

