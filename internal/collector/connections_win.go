//go:build windows

package collector

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	psnet "github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/windows"
)

// ConnectionInfo represents an active TCP/UDP socket with its owning process.
type ConnectionInfo struct {
	Protocol    string `json:"protocol"`    // "TCP", "TCP6", "UDP", "UDP6"
	LocalAddr   string `json:"localAddr"`   // e.g. "192.168.1.10:54321"
	LocalIP     string `json:"localIP"`     // e.g. "192.168.1.10"
	LocalPort   uint32 `json:"localPort"`   // e.g. 54321
	RemoteAddr  string `json:"remoteAddr"`  // e.g. "142.250.185.78:443"
	RemoteIP    string `json:"remoteIP"`    // e.g. "142.250.185.78"
	RemotePort  uint32 `json:"remotePort"`  // e.g. 443
	State       string `json:"state"`       // "ESTABLISHED", "LISTEN", "TIME_WAIT", etc.
	PID         int32  `json:"pid"`         // Owning Process ID
	ProcessName string `json:"processName"` // e.g. "chrome.exe"
}

// ProcessConnSummary groups socket counts by process for quick inspection.
type ProcessConnSummary struct {
	PID         int32  `json:"pid"`
	ProcessName string `json:"processName"`
	Established int    `json:"established"`
	Listening   int    `json:"listening"`
	Other       int    `json:"other"`
	Total       int    `json:"total"`
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

var (
	pidMu    sync.Mutex
	pidCache = make(map[int32]pidCacheEntry)
)

// GetActiveConnections returns a snapshot of current TCP/UDP connections and owning processes.
// If filterIP is non-empty, only connections matching that local IP (or wildcard 0.0.0.0 / ::) are returned.
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
		if st.Raddr.IP != "" && st.Raddr.Port != 0 {
			remoteFormatted = formatEndpoint(st.Raddr.IP, st.Raddr.Port)
		}

		snap.Connections = append(snap.Connections, ConnectionInfo{
			Protocol:    proto,
			LocalAddr:   localFormatted,
			LocalIP:     st.Laddr.IP,
			LocalPort:   st.Laddr.Port,
			RemoteAddr:  remoteFormatted,
			RemoteIP:    st.Raddr.IP,
			RemotePort:  st.Raddr.Port,
			State:       stateStr,
			PID:         st.Pid,
			ProcessName: procName,
		})

		key := fmt.Sprintf("%d_%s", st.Pid, procName)
		ps, ok := procMap[key]
		if !ok {
			ps = &ProcessConnSummary{
				PID:         st.Pid,
				ProcessName: procName,
			}
			procMap[key] = ps
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
	}

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
		if snap.TopProcesses[i].Established != snap.TopProcesses[j].Established {
			return snap.TopProcesses[i].Established > snap.TopProcesses[j].Established
		}
		return snap.TopProcesses[i].Total > snap.TopProcesses[j].Total
	})

	return snap
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
