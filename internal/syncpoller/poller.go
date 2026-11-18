package syncpoller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"traffcount/internal/collector"
	"traffcount/internal/service"
	"traffcount/internal/storage"
	"traffcount/internal/syncstore"
)

const ringBufferSize = 60

// HostQuotaSummary holds cached monthly data cap telemetry for overview badges.
type HostQuotaSummary struct {
	Enabled    bool    `json:"enabled"`
	CycleLabel string  `json:"cycleLabel"`
	CapGB      float64 `json:"capGB"`
	UsedBytes  uint64  `json:"usedBytes"`
	UsedPct    float64 `json:"usedPct"`
	Over80     bool    `json:"over80"`
	Over100    bool    `json:"over100"`
}

// HostLiveSnapshot combines persisted host state with live 1Hz telemetry and 60s ring buffer.
type HostLiveSnapshot struct {
	Host        syncstore.HostRecord            `json:"host"`
	TotalRxBps  uint64                          `json:"totalRxBps"`
	TotalTxBps  uint64                          `json:"totalTxBps"`
	SessionRx   uint64                          `json:"sessionRx"`
	SessionTx   uint64                          `json:"sessionTx"`
	History     []collector.RatePoint           `json:"history"`
	Interfaces  []collector.LiveInterfaceSnapshot `json:"interfaces"`
	Diagnostics collector.DiagnosticsStatus     `json:"diagnostics"`
	Quota       HostQuotaSummary                `json:"quota"`
}

// ClusterRealtimePayload is broadcast every second to Sync Server web dashboards.
type ClusterRealtimePayload struct {
	Timestamp   int64                 `json:"timestamp"`
	Source      string                `json:"source"` // "service" or "server"
	OnlineHosts int                   `json:"onlineHosts"`
	TotalHosts  int                   `json:"totalHosts"`
	TotalRxBps  uint64                `json:"totalRxBps"`
	TotalTxBps  uint64                `json:"totalTxBps"`
	TotalHist   []collector.RatePoint `json:"totalHistory"`
	Hosts       []HostLiveSnapshot    `json:"hosts"`
}

// ProbeResult summarizes connection validation before saving a host.
type ProbeResult struct {
	NormalizedURL string `json:"normalizedUrl"`
	OK            bool   `json:"ok"`
	Platform      string `json:"platform"`
	Hostname      string `json:"hostname"`
	Version       string `json:"version"`
	Source        string `json:"source"`
	LatencyMs     int64  `json:"latencyMs"`
	Error         string `json:"error,omitempty"`
}

// DiscoveredHost describes a remote TraffCount agent detected via LAN scan.
type DiscoveredHost struct {
	BaseURL      string `json:"baseUrl"`
	Hostname     string `json:"hostname"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	Source       string `json:"source"`
	LatencyMs    int64  `json:"latencyMs"`
	RequiresAuth bool   `json:"requiresAuth"`
	AlreadyAdded bool   `json:"alreadyAdded"`
}

type hostWorkerHandle struct {
	cfg    syncstore.HostRecord
	cancel context.CancelFunc
	syncCh chan struct{}
}

type hostLiveState struct {
	totalRxBps uint64
	totalTxBps uint64
	sessionRx  uint64
	sessionTx  uint64
	history    []collector.RatePoint
	ifaces     []collector.LiveInterfaceSnapshot
	diag       collector.DiagnosticsStatus
	quota      HostQuotaSummary
	lastPoll   time.Time
}

// Poller concurrently collects live & historical metrics from configured remote TraffCount hosts.
type Poller struct {
	store      *syncstore.Store
	httpClient *http.Client

	mu          sync.RWMutex
	runCtx      context.Context
	workers     map[string]*hostWorkerHandle
	live        map[string]*hostLiveState
	clusterHist []collector.RatePoint
	lastPayload ClusterRealtimePayload

	subMu       sync.Mutex
	subscribers map[chan ClusterRealtimePayload]struct{}
}

// New creates a new multi-host Poller backed by store.
func New(store *syncstore.Store) *Poller {
	return &Poller{
		store: store,
		httpClient: &http.Client{
			Timeout: 3500 * time.Millisecond,
		},
		workers:     make(map[string]*hostWorkerHandle),
		live:        make(map[string]*hostLiveState),
		clusterHist: make([]collector.RatePoint, 0, ringBufferSize),
		subscribers: make(map[chan ClusterRealtimePayload]struct{}),
	}
}

// Subscribe registers a WebSocket listener for 1Hz ClusterRealtimePayload broadcasts.
func (p *Poller) Subscribe() (chan ClusterRealtimePayload, func()) {
	ch := make(chan ClusterRealtimePayload, 8)
	p.subMu.Lock()
	p.subscribers[ch] = struct{}{}
	p.subMu.Unlock()

	unsub := func() {
		p.subMu.Lock()
		if _, ok := p.subscribers[ch]; ok {
			delete(p.subscribers, ch)
			close(ch)
		}
		p.subMu.Unlock()
	}
	return ch, unsub
}

// LatestSnapshot returns the latest aggregated cluster payload.
func (p *Poller) LatestSnapshot() ClusterRealtimePayload {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.lastPayload.Timestamp == 0 {
		return p.buildPayloadLocked(time.Now().Unix())
	}
	return p.lastPayload
}

// Run starts host workers and the 1Hz fleet aggregation loop until ctx is canceled.
func (p *Poller) Run(ctx context.Context) {
	p.mu.Lock()
	p.runCtx = ctx
	p.mu.Unlock()

	if cfg, err := p.store.GetSettings(); err == nil && cfg.RetentionDays > 0 {
		go func(days int) { _ = p.store.PruneOldData(days) }(cfg.RetentionDays)
	}

	p.ReloadHosts()

	aggTicker := time.NewTicker(1 * time.Second)
	defer aggTicker.Stop()

	pruneTicker := time.NewTicker(1 * time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.stopAllWorkers()
			p.closeSubscribers()
			return
		case <-aggTicker.C:
			payload := p.aggregateTick(time.Now().Unix())
			p.broadcast(payload)
		case <-pruneTicker.C:
			if cfg, err := p.store.GetSettings(); err == nil && cfg.RetentionDays > 0 {
				_ = p.store.PruneOldData(cfg.RetentionDays)
			}
		}
	}
}

// ReloadHosts reconciles active polling goroutines with the SQLite hosts table.
func (p *Poller) ReloadHosts() {
	hosts, err := p.store.ListHosts()
	if err != nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runCtx == nil || p.runCtx.Err() != nil {
		return
	}

	desired := make(map[string]syncstore.HostRecord, len(hosts))
	for _, h := range hosts {
		desired[h.ID] = h
	}

	// Stop removed, disabled, or reconfigured workers
	for id, w := range p.workers {
		h, exists := desired[id]
		if !exists || !h.Enabled || h.BaseURL != w.cfg.BaseURL || h.APIToken != w.cfg.APIToken || h.PollIntervalMs != w.cfg.PollIntervalMs {
			w.cancel()
			delete(p.workers, id)
			if !exists {
				delete(p.live, id)
			}
		}
	}

	// Start workers for newly enabled hosts
	for _, h := range hosts {
		if !h.Enabled {
			continue
		}
		if _, running := p.workers[h.ID]; !running {
			wCtx, cancel := context.WithCancel(p.runCtx)
			handle := &hostWorkerHandle{
				cfg:    h,
				cancel: cancel,
				syncCh: make(chan struct{}, 1),
			}
			p.workers[h.ID] = handle
			if _, ok := p.live[h.ID]; !ok {
				p.live[h.ID] = &hostLiveState{
					history: make([]collector.RatePoint, 0, ringBufferSize),
				}
			}
			go p.runHostWorker(wCtx, h, handle.syncCh)
		}
	}
}

// TriggerSync requests an immediate historical & speedtest sync for hostID.
func (p *Poller) TriggerSync(hostID string) error {
	p.mu.RLock()
	w, ok := p.workers[hostID]
	p.mu.RUnlock()
	if ok {
		select {
		case w.syncCh <- struct{}{}:
		default:
		}
		return nil
	}
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return err
	}
	go p.syncHostHistory(context.Background(), host)
	return nil
}

func (p *Poller) stopAllWorkers() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, w := range p.workers {
		w.cancel()
		delete(p.workers, id)
	}
}

func (p *Poller) runHostWorker(ctx context.Context, host syncstore.HostRecord, syncCh <-chan struct{}) {
	pollDur := time.Duration(host.PollIntervalMs) * time.Millisecond
	if pollDur < 500*time.Millisecond {
		pollDur = 1 * time.Second
	}

	// Immediate live poll + initial historical backfill
	p.pollHostOnce(ctx, host)
	p.syncHostHistory(ctx, host)

	pollTicker := time.NewTicker(pollDur)
	defer pollTicker.Stop()

	histInterval := 60 * time.Second
	if cfg, err := p.store.GetSettings(); err == nil && cfg.HistorySyncSec >= 15 {
		histInterval = time.Duration(cfg.HistorySyncSec) * time.Second
	}
	histTicker := time.NewTicker(histInterval)
	defer histTicker.Stop()

	backoffFailures := 0
	nextAllowedPoll := time.Time{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-syncCh:
			p.pollHostOnce(ctx, host)
			p.syncHostHistory(ctx, host)
		case <-histTicker.C:
			p.syncHostHistory(ctx, host)
		case <-pollTicker.C:
			now := time.Now()
			if !nextAllowedPoll.IsZero() && now.Before(nextAllowedPoll) {
				continue
			}
			ok := p.pollHostOnce(ctx, host)
			if ok {
				backoffFailures = 0
				nextAllowedPoll = time.Time{}
			} else {
				backoffFailures++
				wait := time.Duration(backoffFailures*2) * time.Second
				if wait > 20*time.Second {
					wait = 20 * time.Second
				}
				nextAllowedPoll = now.Add(wait)
			}
		}
	}
}

func (p *Poller) doHostRequest(ctx context.Context, host syncstore.HostRecord, method, pathAndQuery string, timeout time.Duration) ([]byte, int, error) {
	return p.doHostRequestWithBody(ctx, host, method, pathAndQuery, "", nil, timeout)
}

func (p *Poller) doHostRequestWithBody(ctx context.Context, host syncstore.HostRecord, method, pathAndQuery, contentType string, reqBody []byte, timeout time.Duration) ([]byte, int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var bodyReader io.Reader
	if reqBody != nil {
		bodyReader = bytes.NewReader(reqBody)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, host.BaseURL+pathAndQuery, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if host.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+host.APIToken)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func (p *Poller) pollHostOnce(ctx context.Context, host syncstore.HostRecord) bool {
	start := time.Now()
	pingBody, pingCode, pingErr := p.doHostRequest(ctx, host, http.MethodGet, "/api/ping", 2*time.Second)
	latencyMs := time.Since(start).Milliseconds()
	if latencyMs < 1 {
		latencyMs = 1
	}

	if pingErr != nil {
		_ = p.store.UpdateHostRuntime(host.ID, "offline", "", "", "", pingErr.Error(), 0, 0)
		return false
	}
	if pingCode == http.StatusUnauthorized || pingCode == http.StatusForbidden {
		_ = p.store.UpdateHostRuntime(host.ID, "auth_error", "", "", "", fmt.Sprintf("HTTP %d (check API token)", pingCode), latencyMs, time.Now().Unix())
		return false
	}
	if pingCode >= 400 {
		_ = p.store.UpdateHostRuntime(host.ID, "offline", "", "", "", fmt.Sprintf("ping HTTP %d", pingCode), latencyMs, 0)
		return false
	}

	var pingMeta struct {
		Platform string `json:"platform"`
		Version  string `json:"version"`
		Source   string `json:"source"`
	}
	_ = json.Unmarshal(pingBody, &pingMeta)

	rtBody, rtCode, rtErr := p.doHostRequest(ctx, host, http.MethodGet, "/api/realtime", 2500*time.Millisecond)
	if rtErr != nil {
		_ = p.store.UpdateHostRuntime(host.ID, "offline", pingMeta.Platform, pingMeta.Version, pingMeta.Source, rtErr.Error(), latencyMs, 0)
		return false
	}
	if rtCode == http.StatusUnauthorized || rtCode == http.StatusForbidden {
		_ = p.store.UpdateHostRuntime(host.ID, "auth_error", pingMeta.Platform, pingMeta.Version, pingMeta.Source, fmt.Sprintf("HTTP %d (check API token)", rtCode), latencyMs, time.Now().Unix())
		return false
	}
	if rtCode >= 400 {
		_ = p.store.UpdateHostRuntime(host.ID, "offline", pingMeta.Platform, pingMeta.Version, pingMeta.Source, fmt.Sprintf("realtime HTTP %d", rtCode), latencyMs, 0)
		return false
	}

	var payload collector.RealtimePayload
	if err := json.Unmarshal(rtBody, &payload); err != nil {
		_ = p.store.UpdateHostRuntime(host.ID, "offline", pingMeta.Platform, pingMeta.Version, pingMeta.Source, "invalid realtime JSON", latencyMs, 0)
		return false
	}

	srcMode := payload.Source
	if srcMode == "" {
		srcMode = pingMeta.Source
	}
	nowUnix := time.Now().Unix()
	_ = p.store.UpdateHostRuntime(host.ID, "online", pingMeta.Platform, pingMeta.Version, srcMode, "", latencyMs, nowUnix)

	var sessRx, sessTx uint64
	ifRecords := make([]syncstore.HostInterfaceRecord, 0, len(payload.Interfaces))
	for _, ic := range payload.Interfaces {
		if !ic.IsLoopback {
			sessRx += ic.SessionRx
			sessTx += ic.SessionTx
		}
		ifRecords = append(ifRecords, syncstore.HostInterfaceRecord{
			HostID:        host.ID,
			IfaceID:       ic.ID,
			Name:          ic.Name,
			Description:   ic.Description,
			MAC:           ic.MAC,
			IPv4:          ic.IPv4,
			IPv6:          ic.IPv6,
			LinkSpeedBits: ic.LinkSpeedBits,
			IsUp:          ic.IsUp,
			IsLoopback:    ic.IsLoopback,
			LastSeen:      nowUnix,
		})
	}
	if len(ifRecords) > 0 {
		_ = p.store.UpsertHostInterfaces(host.ID, ifRecords)
	}

	p.mu.Lock()
	st, ok := p.live[host.ID]
	if !ok {
		st = &hostLiveState{}
		p.live[host.ID] = st
	}
	st.totalRxBps = payload.TotalRxBps
	st.totalTxBps = payload.TotalTxBps
	st.sessionRx = sessRx
	st.sessionTx = sessTx
	if len(payload.TotalHist) > 0 {
		histCopy := make([]collector.RatePoint, len(payload.TotalHist))
		copy(histCopy, payload.TotalHist)
		if len(histCopy) > ringBufferSize {
			histCopy = histCopy[len(histCopy)-ringBufferSize:]
		}
		st.history = histCopy
	} else {
		pt := collector.RatePoint{
			Timestamp: nowUnix,
			RxBps:     payload.TotalRxBps,
			TxBps:     payload.TotalTxBps,
		}
		if len(st.history) >= ringBufferSize {
			st.history = append(st.history[1:], pt)
		} else {
			st.history = append(st.history, pt)
		}
	}
	st.ifaces = payload.Interfaces
	st.diag = payload.Diagnostics
	st.lastPoll = time.Now()
	p.mu.Unlock()

	return true
}

func (p *Poller) syncHostHistory(ctx context.Context, host syncstore.HostRecord) {
	if fresh, err := p.store.GetHost(host.ID); err == nil {
		host = fresh
	}
	p.refreshHostQuota(ctx, host)

	// Determine incremental sync window: backfill 90 days on first sync, else last 48h to reconcile active hour cleanly.
	var sinceTS int64
	if host.LastSyncTS <= 0 {
		sinceTS = time.Now().AddDate(0, 0, -90).Unix()
	} else {
		sinceTS = host.LastSyncTS - 48*3600
		if sinceTS < 0 {
			sinceTS = 0
		}
	}

	path := fmt.Sprintf("/api/sync/export?since=%d", sinceTS)
	body, code, err := p.doHostRequest(ctx, host, http.MethodGet, path, 10*time.Second)
	if err == nil && code == http.StatusOK {
		var exp storage.SyncExportPayload
		if jErr := json.Unmarshal(body, &exp); jErr == nil {
			p.applySyncExport(host.ID, exp)
			_ = p.store.UpdateHostLastSync(host.ID, time.Now().Unix())
			return
		}
	}

	// Fallback if target host runs an older build without /api/sync/export
	if code == http.StatusNotFound {
		p.syncHostFallbackHistory(ctx, host, sinceTS)
	}
}

func (p *Poller) refreshHostQuota(ctx context.Context, host syncstore.HostRecord) {
	qBody, qCode, qErr := p.doHostRequest(ctx, host, http.MethodGet, "/api/quota", 2500*time.Millisecond)
	if qErr != nil || qCode != http.StatusOK {
		return
	}
	var qs struct {
		Enabled    bool    `json:"enabled"`
		CycleLabel string  `json:"cycleLabel"`
		CapGB      float64 `json:"capGB"`
		CapGb      float64 `json:"capGb"`
		UsedBytes  uint64  `json:"usedBytes"`
		UsedTotal  uint64  `json:"usedTotal"`
		UsedPct    float64 `json:"usedPct"`
		PctUsed    float64 `json:"pctUsed"`
		Over80     bool    `json:"over80"`
		Over100    bool    `json:"over100"`
	}
	if json.Unmarshal(qBody, &qs) != nil {
		return
	}
	capGB := qs.CapGB
	if capGB == 0 {
		capGB = qs.CapGb
	}
	usedBytes := qs.UsedBytes
	if usedBytes == 0 {
		usedBytes = qs.UsedTotal
	}
	usedPct := qs.UsedPct
	if usedPct == 0 {
		usedPct = qs.PctUsed
	}
	p.mu.Lock()
	if st, ok := p.live[host.ID]; ok {
		st.quota = HostQuotaSummary{
			Enabled:    qs.Enabled,
			CycleLabel: qs.CycleLabel,
			CapGB:      capGB,
			UsedBytes:  usedBytes,
			UsedPct:    usedPct,
			Over80:     qs.Over80 || usedPct >= 80,
			Over100:    qs.Over100 || usedPct >= 100,
		}
	}
	p.mu.Unlock()
}

func (p *Poller) applySyncExport(hostID string, exp storage.SyncExportPayload) {
	if len(exp.Interfaces) > 0 {
		ifaces := make([]syncstore.HostInterfaceRecord, 0, len(exp.Interfaces))
		for _, ic := range exp.Interfaces {
			nameLower := strings.ToLower(ic.Name)
			isLoop := strings.Contains(nameLower, "loopback") || nameLower == "lo"
			ifaces = append(ifaces, syncstore.HostInterfaceRecord{
				HostID:        hostID,
				IfaceID:       ic.ID,
				Name:          ic.Name,
				Description:   ic.Description,
				MAC:           ic.MAC,
				IsLoopback:    isLoop,
				PreserveState: true,
				LastSeen:      ic.LastSeen,
			})
		}
		_ = p.store.UpsertHostInterfaces(hostID, ifaces)
	}
	if len(exp.Traffic) > 0 {
		tRows := make([]syncstore.HostTrafficRow, 0, len(exp.Traffic))
		for _, r := range exp.Traffic {
			tRows = append(tRows, syncstore.HostTrafficRow{
				IfaceID:  r.IfaceID,
				BucketTS: r.BucketTS,
				RxBytes:  r.RxBytes,
				TxBytes:  r.TxBytes,
			})
		}
		_ = p.store.UpsertHostTrafficHourly(hostID, tRows)
	}
	if len(exp.Processes) > 0 {
		pRows := make([]syncstore.HostProcessRow, 0, len(exp.Processes))
		for _, r := range exp.Processes {
			pRows = append(pRows, syncstore.HostProcessRow{
				ProcessName: r.ProcessName,
				ExePath:     r.ExePath,
				BucketTS:    r.BucketTS,
				RxBytes:     r.RxBytes,
				TxBytes:     r.TxBytes,
				Blocked:     r.Blocked,
			})
		}
		_ = p.store.UpsertHostProcessHourly(hostID, pRows)
	}
	if len(exp.SpeedTests) > 0 {
		stRows := make([]syncstore.HostSpeedTestRow, 0, len(exp.SpeedTests))
		for _, r := range exp.SpeedTests {
			stRows = append(stRows, syncstore.HostSpeedTestRow{
				HostID:           hostID,
				Timestamp:        r.Timestamp,
				UnloadedPingMs:   r.UnloadedPingMs,
				JitterMs:         r.JitterMs,
				LoadedPingMs:     r.LoadedPingMs,
				DownloadMbps:     r.DownloadMbps,
				UploadMbps:       r.UploadMbps,
				BufferbloatGrade: r.BufferbloatGrade,
				ServerLabel:      r.ServerLabel,
				Error:            r.Error,
			})
		}
		_ = p.store.UpsertHostSpeedTests(hostID, stRows)
	}
}

func (p *Poller) syncHostFallbackHistory(ctx context.Context, host syncstore.HostRecord, sinceTS int64) {
	ifBody, code, err := p.doHostRequest(ctx, host, http.MethodGet, "/api/interfaces", 4*time.Second)
	if err != nil || code != http.StatusOK {
		return
	}
	var ifResp struct {
		Interfaces []storage.InterfaceRecord `json:"interfaces"`
	}
	if json.Unmarshal(ifBody, &ifResp) != nil {
		return
	}
	endTS := time.Now().Unix()
	for _, ic := range ifResp.Interfaces {
		qPath := fmt.Sprintf("/api/history?iface=%s&granularity=hour&start=%d&end=%d",
			url.QueryEscape(ic.ID), sinceTS, endTS)
		hBody, hCode, hErr := p.doHostRequest(ctx, host, http.MethodGet, qPath, 6*time.Second)
		if hErr != nil || hCode != http.StatusOK {
			continue
		}
		var rep storage.HistoryReport
		if json.Unmarshal(hBody, &rep) != nil {
			continue
		}
		rows := make([]syncstore.HostTrafficRow, 0, len(rep.Series))
		for _, b := range rep.Series {
			if b.RxBytes > 0 || b.TxBytes > 0 {
				rows = append(rows, syncstore.HostTrafficRow{
					IfaceID:  ic.ID,
					BucketTS: b.Timestamp,
					RxBytes:  b.RxBytes,
					TxBytes:  b.TxBytes,
				})
			}
		}
		_ = p.store.UpsertHostTrafficHourly(host.ID, rows)
	}
	_ = p.store.UpdateHostLastSync(host.ID, endTS)
}

func (p *Poller) aggregateTick(unixNow int64) ClusterRealtimePayload {
	hosts, _ := p.store.ListHosts()

	p.mu.Lock()
	defer p.mu.Unlock()

	var totalRx, totalTx uint64
	var onlineCount int
	snapshots := make([]HostLiveSnapshot, 0, len(hosts))
	staleCutoff := time.Now().Add(-8 * time.Second)

	for _, h := range hosts {
		var st *hostLiveState
		if cur, ok := p.live[h.ID]; ok {
			st = cur
		}
		snap := HostLiveSnapshot{
			Host:       h,
			History:    []collector.RatePoint{},
			Interfaces: []collector.LiveInterfaceSnapshot{},
		}
		if st != nil {
			isFresh := !st.lastPoll.IsZero() && st.lastPoll.After(staleCutoff) && h.Enabled && h.Status == "online"
			if isFresh {
				snap.TotalRxBps = st.totalRxBps
				snap.TotalTxBps = st.totalTxBps
				onlineCount++
				totalRx += st.totalRxBps
				totalTx += st.totalTxBps
			}
			snap.SessionRx = st.sessionRx
			snap.SessionTx = st.sessionTx
			snap.Diagnostics = st.diag
			snap.Quota = st.quota
			if len(st.history) > 0 {
				snap.History = make([]collector.RatePoint, len(st.history))
				copy(snap.History, st.history)
			}
			if len(st.ifaces) > 0 {
				snap.Interfaces = make([]collector.LiveInterfaceSnapshot, len(st.ifaces))
				copy(snap.Interfaces, st.ifaces)
			}
		}
		snapshots = append(snapshots, snap)
	}

	pt := collector.RatePoint{
		Timestamp: unixNow,
		RxBps:     totalRx,
		TxBps:     totalTx,
	}
	if len(p.clusterHist) >= ringBufferSize {
		p.clusterHist = append(p.clusterHist[1:], pt)
	} else {
		p.clusterHist = append(p.clusterHist, pt)
	}

	histCopy := make([]collector.RatePoint, len(p.clusterHist))
	copy(histCopy, p.clusterHist)

	src := "server"
	if service.IsServiceProcess() {
		src = "service"
	}

	payload := ClusterRealtimePayload{
		Timestamp:   unixNow,
		Source:      src,
		OnlineHosts: onlineCount,
		TotalHosts:  len(hosts),
		TotalRxBps:  totalRx,
		TotalTxBps:  totalTx,
		TotalHist:   histCopy,
		Hosts:       snapshots,
	}
	p.lastPayload = payload
	return payload
}

func (p *Poller) buildPayloadLocked(unixNow int64) ClusterRealtimePayload {
	src := "server"
	if service.IsServiceProcess() {
		src = "service"
	}
	return ClusterRealtimePayload{
		Timestamp: unixNow,
		Source:    src,
		TotalHist: []collector.RatePoint{},
		Hosts:     []HostLiveSnapshot{},
	}
}

func (p *Poller) broadcast(payload ClusterRealtimePayload) {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for ch := range p.subscribers {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (p *Poller) closeSubscribers() {
	p.subMu.Lock()
	defer p.subMu.Unlock()
	for ch := range p.subscribers {
		delete(p.subscribers, ch)
		close(ch)
	}
}

// TestHostConnection validates reachability, authentication, and agent metadata before adding a host.
func (p *Poller) TestHostConnection(ctx context.Context, rawURL, token string) ProbeResult {
	normURL, err := syncstore.NormalizeHostBaseURL(rawURL)
	if err != nil {
		return ProbeResult{Error: err.Error()}
	}
	tmpHost := syncstore.HostRecord{BaseURL: normURL, APIToken: strings.TrimSpace(token)}
	start := time.Now()
	pingBody, code, err := p.doHostRequest(ctx, tmpHost, http.MethodGet, "/api/ping", 3*time.Second)
	lat := time.Since(start).Milliseconds()
	if lat < 1 {
		lat = 1
	}
	if err != nil {
		return ProbeResult{NormalizedURL: normURL, Error: err.Error()}
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return ProbeResult{NormalizedURL: normURL, LatencyMs: lat, Error: fmt.Sprintf("Authentication failed (HTTP %d)", code)}
	}
	if code != http.StatusOK {
		return ProbeResult{NormalizedURL: normURL, LatencyMs: lat, Error: fmt.Sprintf("Unexpected HTTP %d from /api/ping", code)}
	}
	var pingMeta struct {
		OK       bool   `json:"ok"`
		Platform string `json:"platform"`
		Hostname string `json:"hostname"`
		Version  string `json:"version"`
		Source   string `json:"source"`
	}
	if err := json.Unmarshal(pingBody, &pingMeta); err != nil || !pingMeta.OK {
		return ProbeResult{NormalizedURL: normURL, LatencyMs: lat, Error: "Target did not return a valid TraffCount /api/ping payload"}
	}
	return ProbeResult{
		NormalizedURL: normURL,
		OK:            true,
		Platform:      pingMeta.Platform,
		Hostname:      pingMeta.Hostname,
		Version:       pingMeta.Version,
		Source:        pingMeta.Source,
		LatencyMs:     lat,
	}
}

// ProxyConnections retrieves active TCP/UDP sockets & per-process rates from a remote host.
func (p *Poller) ProxyConnections(ctx context.Context, hostID, ifaceIP string, includeUDP bool) ([]byte, int, error) {
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return nil, http.StatusNotFound, err
	}
	q := fmt.Sprintf("/api/connections?ip=%s&udp=%v", url.QueryEscape(ifaceIP), includeUDP)
	body, code, err := p.doHostRequest(ctx, host, http.MethodGet, q, 4*time.Second)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return body, code, nil
}

// ProxyApps retrieves known and firewall-blocked applications from a remote host.
func (p *Poller) ProxyApps(ctx context.Context, hostID string) ([]byte, int, error) {
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return nil, http.StatusNotFound, err
	}
	body, code, err := p.doHostRequest(ctx, host, http.MethodGet, "/api/apps", 4*time.Second)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return body, code, nil
}

// ProxyFirewallBlock toggles persistent firewall/socket blocking for an application on a remote host.
func (p *Poller) ProxyFirewallBlock(ctx context.Context, hostID string, reqBody []byte) ([]byte, int, error) {
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return nil, http.StatusNotFound, err
	}
	body, code, err := p.doHostRequestWithBody(ctx, host, http.MethodPost, "/api/firewall/block", "application/json", reqBody, 5*time.Second)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return body, code, nil
}

// ProxyConnectionsAction forwards socket close or process kill commands to a remote host.
func (p *Poller) ProxyConnectionsAction(ctx context.Context, hostID string, reqBody []byte) ([]byte, int, error) {
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return nil, http.StatusNotFound, err
	}
	body, code, err := p.doHostRequestWithBody(ctx, host, http.MethodPost, "/api/connections/action", "application/json", reqBody, 5*time.Second)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return body, code, nil
}

// DiscoverLANHosts scans active private IPv4 /24 subnets on port 9876 for TraffCount agents.
func (p *Poller) DiscoverLANHosts(ctx context.Context) []DiscoveredHost {
	existingURLs := make(map[string]bool)
	if hosts, err := p.store.ListHosts(); err == nil {
		for _, h := range hosts {
			existingURLs[h.BaseURL] = true
		}
	}

	candidateSet := make(map[string]struct{})
	candidates := make([]string, 0, 260)
	addCandidate := func(ip string) {
		u := "http://" + ip + ":9876"
		if _, seen := candidateSet[u]; !seen {
			candidateSet[u] = struct{}{}
			candidates = append(candidates, u)
		}
	}

	addCandidate("127.0.0.1")
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ip4 := ipNet.IP.To4()
				if ip4 == nil || !ip4.IsPrivate() {
					continue
				}
				prefix := fmt.Sprintf("%d.%d.%d", ip4[0], ip4[1], ip4[2])
				for last := 1; last <= 254; last++ {
					addCandidate(fmt.Sprintf("%s.%d", prefix, last))
				}
			}
		}
	}

	type scanResult struct {
		host DiscoveredHost
		ok   bool
	}

	jobs := make(chan string, len(candidates))
	for _, c := range candidates {
		jobs <- c
	}
	close(jobs)

	scanClient := &http.Client{Timeout: 350 * time.Millisecond}
	var outMu sync.Mutex
	found := make([]DiscoveredHost, 0)

	workerCount := 32
	if len(candidates) < workerCount {
		workerCount = len(candidates)
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for baseURL := range jobs {
				if ctx.Err() != nil {
					return
				}
				reqCtx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
				req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/api/ping", nil)
				if err != nil {
					cancel()
					continue
				}
				start := time.Now()
				resp, err := scanClient.Do(req)
				lat := time.Since(start).Milliseconds()
				if lat < 1 {
					lat = 1
				}
				if err != nil {
					cancel()
					continue
				}
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
				cancel()

				if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
					// Check if unauthorized response matches TraffCount JSON schema
					if strings.Contains(string(body), "sync API token") {
						outMu.Lock()
						found = append(found, DiscoveredHost{
							BaseURL:      baseURL,
							Hostname:     strings.TrimPrefix(strings.TrimSuffix(baseURL, ":9876"), "http://"),
							LatencyMs:    lat,
							RequiresAuth: true,
							AlreadyAdded: existingURLs[baseURL],
						})
						outMu.Unlock()
					}
					continue
				}
				if resp.StatusCode != http.StatusOK {
					continue
				}
				var ping struct {
					OK       bool   `json:"ok"`
					App      string `json:"app"`
					Platform string `json:"platform"`
					Hostname string `json:"hostname"`
					Version  string `json:"version"`
					Source   string `json:"source"`
				}
				if json.Unmarshal(body, &ping) != nil || !ping.OK || ping.App != "traffcount" {
					continue
				}
				outMu.Lock()
				found = append(found, DiscoveredHost{
					BaseURL:      baseURL,
					Hostname:     ping.Hostname,
					Platform:     ping.Platform,
					Version:      ping.Version,
					Source:       ping.Source,
					LatencyMs:    lat,
					AlreadyAdded: existingURLs[baseURL],
				})
				outMu.Unlock()
			}
		}()
	}
	wg.Wait()

	sort.Slice(found, func(i, j int) bool {
		return found[i].BaseURL < found[j].BaseURL
	})
	return found
}

// ProxyQuota retrieves billing-cycle quota status from a remote host.
func (p *Poller) ProxyQuota(ctx context.Context, hostID string) ([]byte, int, error) {
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return nil, http.StatusNotFound, err
	}
	body, code, err := p.doHostRequest(ctx, host, http.MethodGet, "/api/quota", 4*time.Second)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return body, code, nil
}

// TriggerHostSpeedTest runs a speed & bufferbloat test on the remote host and records the result centrally.
func (p *Poller) TriggerHostSpeedTest(ctx context.Context, hostID string) ([]byte, int, error) {
	host, err := p.store.GetHost(hostID)
	if err != nil {
		return nil, http.StatusNotFound, err
	}
	body, code, err := p.doHostRequest(ctx, host, http.MethodPost, "/api/speedtest", 50*time.Second)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if code == http.StatusOK {
		var res collector.SpeedTestResult
		if json.Unmarshal(body, &res) == nil && res.Timestamp > 0 {
			_ = p.store.UpsertHostSpeedTests(hostID, []syncstore.HostSpeedTestRow{{
				HostID:           hostID,
				Timestamp:        res.Timestamp,
				UnloadedPingMs:   res.UnloadedPingMs,
				JitterMs:         res.JitterMs,
				LoadedPingMs:     res.LoadedPingMs,
				DownloadMbps:     res.DownloadMbps,
				UploadMbps:       res.UploadMbps,
				BufferbloatGrade: res.BufferbloatGrade,
				ServerLabel:      res.ServerLabel,
				Error:            res.Error,
			}})
		}
	}
	return body, code, nil
}
