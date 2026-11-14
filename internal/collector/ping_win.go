//go:build windows

package collector

import (
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"time"
	"unsafe"

	"traffcount/internal/storage"
)

// PingTargetStatus holds live TCP/ICMP probe latency, packet loss, and rolling 60-sample history.
type PingTargetStatus struct {
	Name      string  `json:"name"`
	Target    string  `json:"target"`
	Address   string  `json:"address"`
	Method    string  `json:"method"` // "ICMP" or "TCP"
	LatencyMs int64   `json:"latencyMs"`
	Online    bool    `json:"online"`
	LossPct   float64 `json:"lossPct"`
	History   []int64 `json:"history"` // Last 60 samples in ms (-1 = timeout/offline)
}

// DiagnosticsStatus holds live internet connectivity and latency health.
type DiagnosticsStatus struct {
	Targets []PingTargetStatus `json:"targets"`
	Probes  []PingTargetStatus `json:"probes"`
}

type probeTargetDef struct {
	key     string
	name    string
	addr    string
	shortIP string
}

type probeRing struct {
	name    string
	addr    string
	shortIP string
	samples []bool  // Rolling 20-sample success ring for loss%
	history []int64 // Rolling 60-sample latency ring (-1 = offline)
	lastMs  int64
	online  bool
	method  string
}

type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          uintptr
	Ttl           uint8
	Tos           uint8
	Flags         uint8
	OptionsSize   uint8
	OptionsData   uintptr
}

var (
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")

	probeRunMu sync.Mutex
	diagMu     sync.RWMutex
	diagLast   DiagnosticsStatus
	probeState = make(map[string]*probeRing)

	gwMu          sync.Mutex
	cachedGwIP    string
	cachedGwUntil time.Time
)

func getCachedDefaultGateway(now time.Time) string {
	gwMu.Lock()
	defer gwMu.Unlock()
	if now.Before(cachedGwUntil) {
		return cachedGwIP
	}
	cachedGwIP = GetDefaultGatewayIP()
	cachedGwUntil = now.Add(30 * time.Second)
	return cachedGwIP
}

func buildProbeList(pingGateway bool, custom []storage.PingTargetConfig) []probeTargetDef {
	var defs []probeTargetDef
	if pingGateway {
		if gw := getCachedDefaultGateway(time.Now()); gw != "" {
			defs = append(defs, probeTargetDef{
				key:     "gw:" + gw,
				name:    "Default Gateway",
				addr:    gw,
				shortIP: gw,
			})
		}
	}
	defs = append(defs,
		probeTargetDef{key: "1.1.1.1:53", name: "Cloudflare DNS", addr: "1.1.1.1:53", shortIP: "1.1.1.1"},
		probeTargetDef{key: "8.8.8.8:53", name: "Google DNS", addr: "8.8.8.8:53", shortIP: "8.8.8.8"},
	)
	for _, c := range custom {
		addr := strings.TrimSpace(c.Address)
		if addr == "" {
			continue
		}
		name := strings.TrimSpace(c.Name)
		if name == "" {
			name = addr
		}
		short := addr
		if h, _, err := net.SplitHostPort(addr); err == nil && h != "" {
			short = h
		}
		defs = append(defs, probeTargetDef{
			key:     "custom:" + addr,
			name:    name,
			addr:    addr,
			shortIP: short,
		})
	}
	return defs
}

// ProbeConnectivity runs default connectivity probes (1.1.1.1 & 8.8.8.8).
func ProbeConnectivity() DiagnosticsStatus {
	return ProbeConnectivityWithConfig(false, nil)
}

// ProbeConnectivityWithConfig concurrently measures ICMP/TCP RTT and packet loss across gateway, DNS, and custom targets.
func ProbeConnectivityWithConfig(pingGateway bool, custom []storage.PingTargetConfig) DiagnosticsStatus {
	probeRunMu.Lock()
	defer probeRunMu.Unlock()

	defs := buildProbeList(pingGateway, custom)
	results := make([]PingTargetStatus, len(defs))

	type probeOutcome struct {
		ms     int64
		method string
		ok     bool
	}
	outcomes := make([]probeOutcome, len(defs))

	var wg sync.WaitGroup
	for idx, d := range defs {
		wg.Add(1)
		go func(i int, def probeTargetDef) {
			defer wg.Done()
			ms, method, ok := probeSingleTarget(def.addr, 900*time.Millisecond)
			outcomes[i] = probeOutcome{ms: ms, method: method, ok: ok}
		}(idx, d)
	}
	wg.Wait()

	activeKeys := make(map[string]bool, len(defs))
	for i, d := range defs {
		activeKeys[d.key] = true
		p, exists := probeState[d.key]
		if !exists {
			p = &probeRing{
				name:    d.name,
				addr:    d.addr,
				shortIP: d.shortIP,
			}
			probeState[d.key] = p
		}
		p.name = d.name
		p.shortIP = d.shortIP

		out := outcomes[i]
		p.method = out.method
		p.online = out.ok
		if out.ok {
			if out.ms < 1 {
				out.ms = 1
			}
			p.lastMs = out.ms
		} else {
			p.lastMs = 0
		}

		if len(p.samples) >= 20 {
			p.samples = append(p.samples[1:], out.ok)
		} else {
			p.samples = append(p.samples, out.ok)
		}

		histVal := out.ms
		if !out.ok {
			histVal = -1
		}
		if len(p.history) >= 60 {
			p.history = append(p.history[1:], histVal)
		} else {
			p.history = append(p.history, histVal)
		}

		failures := 0
		for _, s := range p.samples {
			if !s {
				failures++
			}
		}
		loss := 0.0
		if len(p.samples) > 0 {
			loss = (float64(failures) / float64(len(p.samples))) * 100.0
		}

		histCopy := make([]int64, len(p.history))
		copy(histCopy, p.history)

		results[i] = PingTargetStatus{
			Name:      p.name,
			Target:    p.name,
			Address:   p.shortIP,
			Method:    p.method,
			LatencyMs: p.lastMs,
			Online:    p.online,
			LossPct:   loss,
			History:   histCopy,
		}
	}

	// Clean up removed custom targets or old gateway keys
	for k := range probeState {
		if !activeKeys[k] {
			delete(probeState, k)
		}
	}

	st := DiagnosticsStatus{Targets: results, Probes: results}
	diagMu.Lock()
	diagLast = st
	diagMu.Unlock()
	return st
}

func probeSingleTarget(addr string, timeout time.Duration) (int64, string, bool) {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, timeout)
		elapsed := time.Since(start).Milliseconds()
		if conn != nil {
			_ = conn.Close()
		}
		return elapsed, "TCP", err == nil
	}

	ip4 := resolveIPv4(addr)
	if ip4 != nil {
		if rtt, ok := icmpPingIPv4(ip4, timeout); ok {
			return rtt, "ICMP", true
		}
		return 0, "ICMP", false
	}

	// Fallback to TCP :443 if IPv6-only or hostname didn't return IPv4
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, "443"), timeout)
	elapsed := time.Since(start).Milliseconds()
	if conn != nil {
		_ = conn.Close()
	}
	return elapsed, "TCP", err == nil
}

func resolveIPv4(host string) net.IP {
	if ip := net.ParseIP(host); ip != nil {
		return ip.To4()
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4
		}
	}
	return nil
}

func icmpPingIPv4(ip4 net.IP, timeout time.Duration) (int64, bool) {
	ipBytes := ip4.To4()
	if ipBytes == nil {
		return 0, false
	}
	h, _, _ := procIcmpCreateFile.Call()
	if h == 0 || h == ^uintptr(0) {
		return 0, false
	}
	defer procIcmpCloseHandle.Call(h)

	destAddr := binary.LittleEndian.Uint32(ipBytes)
	payload := []byte("traffcount-ping!")
	replyBuf := make([]byte, unsafe.Sizeof(icmpEchoReply{})+uintptr(len(payload))+16)

	start := time.Now()
	ret, _, _ := procIcmpSendEcho.Call(
		h,
		uintptr(destAddr),
		uintptr(unsafe.Pointer(&payload[0])),
		uintptr(len(payload)),
		0,
		uintptr(unsafe.Pointer(&replyBuf[0])),
		uintptr(len(replyBuf)),
		uintptr(timeout.Milliseconds()),
	)
	if ret == 0 {
		return 0, false
	}
	reply := (*icmpEchoReply)(unsafe.Pointer(&replyBuf[0]))
	if reply.Status != 0 {
		return 0, false
	}
	rtt := int64(reply.RoundTripTime)
	if rtt <= 0 {
		rtt = time.Since(start).Milliseconds()
	}
	if rtt < 1 {
		rtt = 1
	}
	return rtt, true
}

// LatestDiagnostics returns the most recent connectivity probe results.
func LatestDiagnostics() DiagnosticsStatus {
	diagMu.RLock()
	defer diagMu.RUnlock()
	return diagLast
}
