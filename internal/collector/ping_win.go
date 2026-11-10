//go:build windows

package collector

import (
	"net"
	"sync"
	"time"
)

// PingTargetStatus holds live TCP/ICMP probe latency and packet loss for a connectivity target.
type PingTargetStatus struct {
	Name       string  `json:"name"`
	Target     string  `json:"target"`
	LatencyMs  int64   `json:"latencyMs"`
	Online     bool    `json:"online"`
	LossPct    float64 `json:"lossPct"`
}

// DiagnosticsStatus holds live internet connectivity and latency health.
type DiagnosticsStatus struct {
	Targets []PingTargetStatus `json:"targets"`
}

type probeRing struct {
	name    string
	addr    string
	samples []bool
	lastMs  int64
	online  bool
}

var (
	diagMu   sync.RWMutex
	diagLast DiagnosticsStatus
	probes   = []*probeRing{
		{name: "Cloudflare DNS", addr: "1.1.1.1:53"},
		{name: "Google DNS", addr: "8.8.8.8:53"},
	}
)

// ProbeConnectivity measures TCP handshake RTT to 1.1.1.1:53 and 8.8.8.8:53.
func ProbeConnectivity() DiagnosticsStatus {
	var results []PingTargetStatus
	for _, p := range probes {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", p.addr, 900*time.Millisecond)
		elapsed := time.Since(start).Milliseconds()
		ok := err == nil
		if conn != nil {
			conn.Close()
		}
		if len(p.samples) >= 20 {
			p.samples = append(p.samples[1:], ok)
		} else {
			p.samples = append(p.samples, ok)
		}
		p.online = ok
		if ok {
			if elapsed < 1 {
				elapsed = 1
			}
			p.lastMs = elapsed
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

		results = append(results, PingTargetStatus{
			Name:      p.name,
			Target:    p.addr,
			LatencyMs: p.lastMs,
			Online:    p.online,
			LossPct:   loss,
		})
	}

	st := DiagnosticsStatus{Targets: results}
	diagMu.Lock()
	diagLast = st
	diagMu.Unlock()
	return st
}

// LatestDiagnostics returns the most recent connectivity probe results.
func LatestDiagnostics() DiagnosticsStatus {
	diagMu.RLock()
	defer diagMu.RUnlock()
	return diagLast
}
