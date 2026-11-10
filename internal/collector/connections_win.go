//go:build windows

package collector

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	psnet "github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/windows"
)

// ConnectionInfo represents an active TCP/UDP socket with its owning process, reverse DNS, and per-socket traffic counters.
type ConnectionInfo struct {
	Protocol    string `json:"protocol"`    // "TCP", "TCP6", "UDP", "UDP6"
	LocalAddr   string `json:"localAddr"`   // e.g. "192.168.1.10:54321"
	LocalIP     string `json:"localIP"`     // e.g. "192.168.1.10"
	LocalPort   uint32 `json:"localPort"`   // e.g. 54321
	RemoteAddr  string `json:"remoteAddr"`  // e.g. "142.250.185.78:443"
	RemoteIP    string `json:"remoteIP"`    // e.g. "142.250.185.78"
	RemoteHost  string `json:"remoteHost"`  // Asynchronous reverse-DNS hostname or provider label
	RemotePort  uint32 `json:"remotePort"`  // e.g. 443
	State       string `json:"state"`       // "ESTABLISHED", "LISTEN", "TIME_WAIT", etc.
	PID         int32  `json:"pid"`         // Owning Process ID
	ProcessName string `json:"processName"` // e.g. "chrome.exe"
	RttMs       uint32 `json:"rttMs"`       // TCP smoothed RTT in ms (from GetPerTcpConnectionEStats)
	RxBytes     uint64 `json:"rxBytes"`     // Cumulative inbound bytes on this TCP connection
	TxBytes     uint64 `json:"txBytes"`     // Cumulative outbound bytes on this TCP connection
}

// ProcessConnSummary groups socket counts and measured TCP throughput/volume by process.
type ProcessConnSummary struct {
	PID         int32  `json:"pid"`
	ProcessName string `json:"processName"`
	Established int    `json:"established"`
	Listening   int    `json:"listening"`
	Other       int    `json:"other"`
	Total       int    `json:"total"`
	RxBps       uint64 `json:"rxBps"`
	TxBps       uint64 `json:"txBps"`
	TotalRx     uint64 `json:"totalRx"`
	TotalTx     uint64 `json:"totalTx"`
}

// ConnectionsSnapshot is returned by /api/connections.
type ConnectionsSnapshot struct {
	Timestamp        int64                `json:"timestamp"`
	TotalTCP         int                  `json:"totalTcp"`
	EstablishedCount int                  `json:"establishedCount"`
	ListenCount      int                  `json:"listenCount"`
	TimeWaitCount    int                  `json:"timeWaitCount"`
	TotalUDP         int                  `json:"totalUdp"`
	Connections      []ConnectionInfo     `json:"connections"`
	TopProcesses     []ProcessConnSummary `json:"topProcesses"`
}

type pidCacheEntry struct {
	name      string
	expiresAt time.Time
}

type dnsCacheEntry struct {
	host      string
	expiresAt time.Time
	pending   bool
}

type procTrafficTracker struct {
	lastSocketRx map[string]uint64
	lastSocketTx map[string]uint64
	cumRx        uint64
	cumTx        uint64
	rxBps        uint64
	txBps        uint64
	lastSample   time.Time
}

var (
	pidMu    sync.Mutex
	pidCache = make(map[int32]pidCacheEntry)

	dnsMu    sync.Mutex
	dnsCache = make(map[string]dnsCacheEntry)

	procTrackMu sync.Mutex
	procTrack   = make(map[string]*procTrafficTracker)

	iphlpapi                     = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetPerTcpConnectionEStat = iphlpapi.NewProc("GetPerTcpConnectionEStats")
	procSetPerTcpConnectionEStat = iphlpapi.NewProc("SetPerTcpConnectionEStats")
)

const (
	tcpConnectionEstatsData = 1
	tcpConnectionEstatsPath = 2
)

type mibTcpRow struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
}

type tcpEstatsDataRodv0 struct {
	DataBytesOut      uint64
	DataSegsOut       uint64
	DataBytesIn       uint64
	DataSegsIn        uint64
	SegsOut           uint64
	SegsIn            uint64
	ThruBytesAcked    uint32
	ThruBytesReceived uint32
}

type tcpEstatsDataRwv0 struct {
	EnableCollection uint8
}

type tcpEstatsPathRodv0 struct {
	FastRetran int32
	Timeouts   uint32
	SubsequentTimeouts uint32
	CurTimeoutCount    uint32
	AbruptTimeouts     uint32
	PktsRetrans        uint32
	BytesRetrans       uint32
	DupAcksIn          uint32
	SacksRcvd          uint32
	SackBlocksRcvd     uint32
	CongSignals        uint32
	PreCongSumCwnd     uint32
	PreCongSumRtt      uint32
	PostCongSumRtt     uint32
	PostCongCountRtt   uint32
	EcnSignals         uint32
	EceRcvd            uint32
	SendStall          uint32
	QuenchRcvd         uint32
	RetranThresh       uint32
	SndDupAckEpisodes  uint32
	SumBytesReordered  uint32
	NonRecovDa         uint32
	NonRecovDaEpisodes uint32
	AckAfterFr         uint32
	DsackDups          uint32
	SampleRtt          uint32
	SmoothedRtt        uint32
	RttVar             uint32
	MaxRtt             uint32
	MinRtt             uint32
	SumRtt             uint32
	CountRtt           uint32
}

type tcpEstatsPathRwv0 struct {
	EnableCollection uint8
}

// GetActiveConnections returns a snapshot of current TCP/UDP connections, remote hostnames, and per-process traffic.
func GetActiveConnections(filterIP string, includeUDP bool) ConnectionsSnapshot {
	now := time.Now()
	snap := ConnectionsSnapshot{
		Timestamp:    now.Unix(),
		Connections:  []ConnectionInfo{},
		TopProcesses: []ProcessConnSummary{},
	}

	kind := "tcp"
	if includeUDP {
		kind = "inet"
	}

	stats, err := psnet.Connections(kind)
	if err != nil {
		return snap
	}

	procMap := make(map[string]*ProcessConnSummary)
	curSocketRx := make(map[string]map[string]uint64)
	curSocketTx := make(map[string]map[string]uint64)

	for _, st := range stats {
		localIP := st.Laddr.IP
		if filterIP != "" && localIP != filterIP && localIP != "0.0.0.0" && localIP != "::" && localIP != "*" {
			continue
		}

		proto := "TCP"
		isUDP := st.Type == windows.SOCK_DGRAM
		isIPv6 := strings.Contains(localIP, ":")
		if isUDP {
			if isIPv6 {
				proto = "UDP6"
			} else {
				proto = "UDP"
			}
			snap.TotalUDP++
		} else {
			if isIPv6 {
				proto = "TCP6"
			}
			snap.TotalTCP++
		}

		stateStr := st.Status
		if isUDP && stateStr == "" {
			stateStr = "ACTIVE"
		}
		switch stateStr {
		case "ESTABLISHED":
			snap.EstablishedCount++
		case "LISTEN":
			snap.ListenCount++
		case "TIME_WAIT", "CLOSE_WAIT":
			snap.TimeWaitCount++
		}

		procName := resolveProcessName(st.Pid, now)

		localFormatted := formatEndpoint(st.Laddr.IP, st.Laddr.Port)
		remoteFormatted := "—"
		var remoteHost string
		if st.Raddr.IP != "" && st.Raddr.Port != 0 {
			remoteFormatted = formatEndpoint(st.Raddr.IP, st.Raddr.Port)
			remoteHost = lookupRemoteHostnameAsync(st.Raddr.IP, now)
		}

		var rxBytes, txBytes uint64
		var rttMs uint32
		if !isUDP && !isIPv6 && stateStr == "ESTABLISHED" {
			rxBytes, txBytes, rttMs = queryTCPEStatsV4(st.Laddr.IP, st.Laddr.Port, st.Raddr.IP, st.Raddr.Port)
		}

		snap.Connections = append(snap.Connections, ConnectionInfo{
			Protocol:    proto,
			LocalAddr:   localFormatted,
			LocalIP:     st.Laddr.IP,
			LocalPort:   st.Laddr.Port,
			RemoteAddr:  remoteFormatted,
			RemoteIP:    st.Raddr.IP,
			RemoteHost:  remoteHost,
			RemotePort:  st.Raddr.Port,
			State:       stateStr,
			PID:         st.Pid,
			ProcessName: procName,
			RttMs:       rttMs,
			RxBytes:     rxBytes,
			TxBytes:     txBytes,
		})

		key := fmt.Sprintf("%d_%s", st.Pid, procName)
		ps, ok := procMap[key]
		if !ok {
			ps = &ProcessConnSummary{
				PID:         st.Pid,
				ProcessName: procName,
			}
			procMap[key] = ps
			curSocketRx[key] = make(map[string]uint64)
			curSocketTx[key] = make(map[string]uint64)
		}
		ps.Total++
		switch stateStr {
		case "ESTABLISHED":
			ps.Established++
		case "LISTEN":
			ps.Listening++
		default:
			ps.Other++
		}

		if rxBytes > 0 || txBytes > 0 {
			sockKey := localFormatted + "->" + remoteFormatted
			curSocketRx[key][sockKey] = rxBytes
			curSocketTx[key][sockKey] = txBytes
		}
	}

	// Update per-process traffic deltas & cumulative totals
	procTrackMu.Lock()
	for key, ps := range procMap {
		tr, exists := procTrack[key]
		if !exists {
			tr = &procTrafficTracker{
				lastSocketRx: make(map[string]uint64),
				lastSocketTx: make(map[string]uint64),
				lastSample:   now,
			}
			procTrack[key] = tr
		}
		elapsed := now.Sub(tr.lastSample).Seconds()
		if elapsed >= 0.4 {
			var deltaRx, deltaTx uint64
			for sk, curRx := range curSocketRx[key] {
				prevRx := tr.lastSocketRx[sk]
				if curRx >= prevRx {
					deltaRx += curRx - prevRx
				}
			}
			for sk, curTx := range curSocketTx[key] {
				prevTx := tr.lastSocketTx[sk]
				if curTx >= prevTx {
					deltaTx += curTx - prevTx
				}
			}
			tr.cumRx += deltaRx
			tr.cumTx += deltaTx
			tr.rxBps = uint64(float64(deltaRx) / elapsed)
			tr.txBps = uint64(float64(deltaTx) / elapsed)
			tr.lastSocketRx = curSocketRx[key]
			tr.lastSocketTx = curSocketTx[key]
			tr.lastSample = now
		}
		ps.RxBps = tr.rxBps
		ps.TxBps = tr.txBps
		ps.TotalRx = tr.cumRx
		ps.TotalTx = tr.cumTx
	}
	procTrackMu.Unlock()

	// Sort connections: ESTABLISHED first, then LISTEN, then by process name
	sort.Slice(snap.Connections, func(i, j int) bool {
		rank := func(s string) int {
			switch s {
			case "ESTABLISHED":
				return 0
			case "LISTEN":
				return 1
			case "ACTIVE":
				return 2
			default:
				return 3
			}
		}
		ri, rj := rank(snap.Connections[i].State), rank(snap.Connections[j].State)
		if ri != rj {
			return ri < rj
		}
		if snap.Connections[i].ProcessName != snap.Connections[j].ProcessName {
			return strings.ToLower(snap.Connections[i].ProcessName) < strings.ToLower(snap.Connections[j].ProcessName)
		}
		return snap.Connections[i].LocalPort < snap.Connections[j].LocalPort
	})

	for _, ps := range procMap {
		snap.TopProcesses = append(snap.TopProcesses, *ps)
	}
	sort.Slice(snap.TopProcesses, func(i, j int) bool {
		rateI := snap.TopProcesses[i].RxBps + snap.TopProcesses[i].TxBps
		rateJ := snap.TopProcesses[j].RxBps + snap.TopProcesses[j].TxBps
		if rateI != rateJ {
			return rateI > rateJ
		}
		totI := snap.TopProcesses[i].TotalRx + snap.TopProcesses[i].TotalTx
		totJ := snap.TopProcesses[j].TotalRx + snap.TopProcesses[j].TotalTx
		if totI != totJ {
			return totI > totJ
		}
		if snap.TopProcesses[i].Established != snap.TopProcesses[j].Established {
			return snap.TopProcesses[i].Established > snap.TopProcesses[j].Established
		}
		return snap.TopProcesses[i].Total > snap.TopProcesses[j].Total
	})

	return snap
}

func queryTCPEStatsV4(localIP string, localPort uint32, remoteIP string, remotePort uint32) (rxBytes uint64, txBytes uint64, rttMs uint32) {
	lip := net.ParseIP(localIP).To4()
	rip := net.ParseIP(remoteIP).To4()
	if lip == nil || rip == nil {
		return 0, 0, 0
	}

	row := mibTcpRow{
		State:      5, // MIB_TCP_STATE_ESTAB
		LocalAddr:  *(*uint32)(unsafe.Pointer(&lip[0])),
		LocalPort:  htons32(uint16(localPort)),
		RemoteAddr: *(*uint32)(unsafe.Pointer(&rip[0])),
		RemotePort: htons32(uint16(remotePort)),
	}

	// Read Data EStats
	var dataRw tcpEstatsDataRwv0
	var dataRod tcpEstatsDataRodv0
	ret, _, _ := procGetPerTcpConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&dataRw)), 1, uintptr(unsafe.Sizeof(dataRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&dataRod)), 0, uintptr(unsafe.Sizeof(dataRod)),
	)
	if ret == 0 {
		if dataRw.EnableCollection == 0 {
			dataRw.EnableCollection = 1
			procSetPerTcpConnectionEStat.Call(
				uintptr(unsafe.Pointer(&row)),
				tcpConnectionEstatsData,
				uintptr(unsafe.Pointer(&dataRw)), 0, uintptr(unsafe.Sizeof(dataRw)), 0,
			)
		} else {
			rxBytes = dataRod.DataBytesIn
			txBytes = dataRod.DataBytesOut
		}
	}

	// Read Path EStats (Smoothed RTT)
	var pathRw tcpEstatsPathRwv0
	var pathRod tcpEstatsPathRodv0
	ret2, _, _ := procGetPerTcpConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsPath,
		uintptr(unsafe.Pointer(&pathRw)), 1, uintptr(unsafe.Sizeof(pathRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&pathRod)), 0, uintptr(unsafe.Sizeof(pathRod)),
	)
	if ret2 == 0 {
		if pathRw.EnableCollection == 0 {
			pathRw.EnableCollection = 1
			procSetPerTcpConnectionEStat.Call(
				uintptr(unsafe.Pointer(&row)),
				tcpConnectionEstatsPath,
				uintptr(unsafe.Pointer(&pathRw)), 0, uintptr(unsafe.Sizeof(pathRw)), 0,
			)
		} else if pathRod.SmoothedRtt > 0 && pathRod.SmoothedRtt < 60000 {
			rttMs = pathRod.SmoothedRtt
		}
	}

	return rxBytes, txBytes, rttMs
}

func htons32(p uint16) uint32 {
	return uint32((p>>8)&0xff) | uint32((p&0xff)<<8)
}

func lookupRemoteHostnameAsync(ip string, now time.Time) string {
	if ip == "" || ip == "127.0.0.1" || ip == "::1" || ip == "0.0.0.0" {
		return "localhost"
	}
	dnsMu.Lock()
	entry, ok := dnsCache[ip]
	if ok && now.Before(entry.expiresAt) {
		dnsMu.Unlock()
		return entry.host
	}
	if ok && entry.pending {
		dnsMu.Unlock()
		return entry.host
	}
	dnsCache[ip] = dnsCacheEntry{
		host:      classifyWellKnownIP(ip),
		expiresAt: now.Add(5 * time.Minute),
		pending:   true,
	}
	dnsMu.Unlock()

	go func(targetIP string) {
		names, err := net.LookupAddr(targetIP)
		resolved := classifyWellKnownIP(targetIP)
		if err == nil && len(names) > 0 {
			resolved = strings.TrimSuffix(names[0], ".")
		}
		dnsMu.Lock()
		dnsCache[targetIP] = dnsCacheEntry{
			host:      resolved,
			expiresAt: time.Now().Add(10 * time.Minute),
			pending:   false,
		}
		dnsMu.Unlock()
	}(ip)

	return classifyWellKnownIP(ip)
}

func classifyWellKnownIP(ip string) string {
	switch {
	case strings.HasPrefix(ip, "127.") || ip == "::1":
		return "localhost"
	case strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "172.16.") || strings.HasPrefix(ip, "172.30."):
		return "Local LAN"
	case ip == "1.1.1.1" || ip == "1.0.0.1":
		return "one.one.one.one (Cloudflare)"
	case ip == "8.8.8.8" || ip == "8.8.4.4":
		return "dns.google"
	case strings.HasPrefix(ip, "142.250.") || strings.HasPrefix(ip, "172.217."):
		return "Google Cloud / Edge"
	case strings.HasPrefix(ip, "20.") || strings.HasPrefix(ip, "13.") || strings.HasPrefix(ip, "40.") || strings.HasPrefix(ip, "52."):
		return "Microsoft / Cloud"
	case strings.HasPrefix(ip, "104.16.") || strings.HasPrefix(ip, "104.18.") || strings.HasPrefix(ip, "172.64."):
		return "Cloudflare CDN"
	default:
		return ""
	}
}

func formatEndpoint(ip string, port uint32) string {
	if strings.Contains(ip, ":") {
		return fmt.Sprintf("[%s]:%d", ip, port)
	}
	return fmt.Sprintf("%s:%d", ip, port)
}

func resolveProcessName(pid int32, now time.Time) string {
	if pid == 0 {
		return "System Idle"
	}
	if pid == 4 {
		return "System"
	}

	pidMu.Lock()
	if entry, ok := pidCache[pid]; ok && now.Before(entry.expiresAt) {
		pidMu.Unlock()
		return entry.name
	}
	pidMu.Unlock()

	name := queryProcessImageName(uint32(pid))
	if name == "" {
		name = fmt.Sprintf("PID %d", pid)
	}

	pidMu.Lock()
	pidCache[pid] = pidCacheEntry{
		name:      name,
		expiresAt: now.Add(30 * time.Second),
	}
	pidMu.Unlock()
	return name
}

func queryProcessImageName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
	if err != nil || size == 0 {
		return ""
	}
	fullPath := windows.UTF16ToString(buf[:size])
	return filepath.Base(fullPath)
}
