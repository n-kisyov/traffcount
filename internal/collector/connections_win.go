//go:build windows

package collector

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"traffcount/internal/service"
)

var (
	iphlpapi                      = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetPerTcpConnectionEStat  = iphlpapi.NewProc("GetPerTcpConnectionEStats")
	procSetPerTcpConnectionEStat  = iphlpapi.NewProc("SetPerTcpConnectionEStats")
	procGetPerTcp6ConnectionEStat = iphlpapi.NewProc("GetPerTcp6ConnectionEStats")
	procSetPerTcp6ConnectionEStat = iphlpapi.NewProc("SetPerTcp6ConnectionEStats")
	procSetTcpEntry               = iphlpapi.NewProc("SetTcpEntry")
)

const (
	tcpConnectionEstatsSynOpts = 0
	tcpConnectionEstatsData    = 1
	tcpConnectionEstatsSndCong = 2
	tcpConnectionEstatsPath    = 3
)

type mibTcpRow struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
}

type mibTcp6Row struct {
	State         uint32
	LocalAddr     [16]byte
	LocalScopeId  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeId uint32
	RemotePort    uint32
}

// Mirrors TCP_ESTATS_DATA_ROD_v0 in <tcpestats.h> (96 bytes on x64).
type tcpEstatsDataRodv0 struct {
	DataBytesOut      uint64
	DataSegsOut       uint64
	DataBytesIn       uint64
	DataSegsIn        uint64
	SegsOut           uint64
	SegsIn            uint64
	SoftErrors        uint32
	SoftErrorReason   uint32
	SndUna            uint32
	SndNxt            uint32
	SndMax            uint32
	ThruBytesAcked    uint64
	RcvNxt            uint32
	ThruBytesReceived uint64
}

type tcpEstatsDataRwv0 struct {
	EnableCollection uint8
}

// Mirrors TCP_ESTATS_PATH_ROD_v0 in <tcpestats.h> (40 ULONG fields = 160 bytes).
type tcpEstatsPathRodv0 struct {
	FastRetran            int32
	Timeouts              uint32
	SubsequentTimeouts    uint32
	CurTimeoutCount       uint32
	AbruptTimeouts        uint32
	PktsRetrans           uint32
	BytesRetrans          uint32
	DupAcksIn             uint32
	SacksRcvd             uint32
	SackBlocksRcvd        uint32
	CongSignals           uint32
	PreCongSumCwnd        uint32
	PreCongSumRtt         uint32
	PostCongSumRtt        uint32
	PostCongCountRtt      uint32
	EcnSignals            uint32
	EceRcvd               uint32
	SendStall             uint32
	QuenchRcvd            uint32
	RetranThresh          uint32
	SndDupAckEpisodes     uint32
	SumBytesReordered     uint32
	NonRecovDa            uint32
	NonRecovDaEpisodes    uint32
	AckAfterFr            uint32
	DsackDups             uint32
	SampleRtt             uint32
	SmoothedRtt           uint32
	RttVar                uint32
	MaxRtt                uint32
	MinRtt                uint32
	SumRtt                uint32
	CountRtt              uint32
	CurRto                uint32
	MaxRto                uint32
	MinRto                uint32
	CurMss                uint32
	MaxMss                uint32
	MinMss                uint32
	SpuriousRtoDetections uint32
}

type tcpEstatsPathRwv0 struct {
	EnableCollection uint8
}

func newTCPMetricsLookup() tcpMetricsLookup {
	return func(localIP string, localPort uint32, remoteIP string, remotePort uint32, isIPv6 bool) (uint64, uint64, uint32) {
		if isIPv6 {
			return queryTCPEStatsV6(localIP, localPort, remoteIP, remotePort)
		}
		return queryTCPEStatsV4(localIP, localPort, remoteIP, remotePort)
	}
}

func enforceBlockedSocket(string, uint32, string, uint32, bool) {
	// Enforced persistently via Windows Defender Firewall rules.
}

func specialPIDIdentity(pid int32) (string, string, bool) {
	if pid == 0 {
		return "System Idle", "", true
	}
	if pid == 4 {
		return "System", "ntoskrnl.exe", true
	}
	return "", "", false
}

// CloseTCPConnectionV4 forcefully closes an established IPv4 TCP socket using SetTcpEntry (MIB_TCP_STATE_DELETE_TCB = 12).
func CloseTCPConnectionV4(localIP string, localPort uint32, remoteIP string, remotePort uint32) error {
	lip := net.ParseIP(localIP).To4()
	rip := net.ParseIP(remoteIP).To4()
	if lip == nil || rip == nil {
		return fmt.Errorf("only IPv4 TCP sockets can be closed via SetTcpEntry")
	}
	row := mibTcpRow{
		State:      12, // MIB_TCP_STATE_DELETE_TCB
		LocalAddr:  *(*uint32)(unsafe.Pointer(&lip[0])),
		LocalPort:  htons32(uint16(localPort)),
		RemoteAddr: *(*uint32)(unsafe.Pointer(&rip[0])),
		RemotePort: htons32(uint16(remotePort)),
	}
	ret, _, _ := procSetTcpEntry.Call(uintptr(unsafe.Pointer(&row)))
	if ret != 0 {
		return fmt.Errorf("SetTcpEntry failed with code %d (requires Administrator privileges)", ret)
	}
	return nil
}

// TerminateProcessByPID terminates a process by PID.
func TerminateProcessByPID(pid int32) error {
	if pid <= 4 {
		return fmt.Errorf("cannot terminate system process PID %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("OpenProcess failed (requires Administrator privileges): %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.TerminateProcess(h, 1); err != nil {
		return fmt.Errorf("TerminateProcess failed: %w", err)
	}
	return nil
}

// SetFirewallBlockRule creates or deletes a Windows Defender Firewall outbound+inbound block rule for an executable.
func SetFirewallBlockRule(processName, exePath string, block bool) error {
	cleanName := strings.ReplaceAll(processName, `"`, "")
	if cleanName == "" {
		return fmt.Errorf("process name is required")
	}
	ruleOut := "TraffCount_Block_Out_" + cleanName
	ruleIn := "TraffCount_Block_In_" + cleanName

	// Always remove existing rules first to avoid duplicates
	_ = runNetshFirewall("advfirewall", "firewall", "delete", "rule", "name="+ruleOut)
	_ = runNetshFirewall("advfirewall", "firewall", "delete", "rule", "name="+ruleIn)

	if !block {
		return nil
	}
	if exePath == "" {
		return fmt.Errorf("executable path is unknown for %s (cannot create path-based firewall rule)", processName)
	}

	if err := runNetshFirewall("advfirewall", "firewall", "add", "rule",
		"name="+ruleOut, "dir=out", "action=block", "program="+exePath, "enable=yes"); err != nil {
		return err
	}
	_ = runNetshFirewall("advfirewall", "firewall", "add", "rule",
		"name="+ruleIn, "dir=in", "action=block", "program="+exePath, "enable=yes")
	return nil
}

// SyncRemoteAccessFirewallRule adds or removes an inbound Windows Defender Firewall allow rule on TCP 9876
// so TraffCount Sync Server can poll this host on the LAN.
func SyncRemoteAccessFirewallRule(allow bool) error {
	const ruleName = "TraffCount_Remote_Sync_In_TCP9876"
	_ = runNetshFirewall("advfirewall", "firewall", "delete", "rule", "name="+ruleName)
	if !allow {
		return nil
	}
	return runNetshFirewall("advfirewall", "firewall", "add", "rule",
		"name="+ruleName, "dir=in", "action=allow", "protocol=TCP", "localport=9876", "enable=yes")
}

func runNetshFirewall(args ...string) error {
	cmd := exec.Command("netsh", args...)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		if !service.IsAdmin() {
			return fmt.Errorf("Administrator privileges required to modify Windows Firewall (%s)", strings.TrimSpace(string(out)))
		}
		return fmt.Errorf("netsh failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
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

	return queryEstats(uintptr(unsafe.Pointer(&row)), procGetPerTcpConnectionEStat, procSetPerTcpConnectionEStat)
}

func parseIPv6AndScope(s string) (net.IP, uint32) {
	var scope uint32
	if idx := strings.IndexByte(s, '%'); idx >= 0 {
		zoneStr := s[idx+1:]
		s = s[:idx]
		if v, err := strconv.ParseUint(zoneStr, 10, 32); err == nil {
			scope = uint32(v)
		}
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil, 0
	}
	return ip.To16(), scope
}

func queryTCPEStatsV6(localIP string, localPort uint32, remoteIP string, remotePort uint32) (rxBytes uint64, txBytes uint64, rttMs uint32) {
	lip, lScope := parseIPv6AndScope(localIP)
	rip, rScope := parseIPv6AndScope(remoteIP)
	if lip == nil || rip == nil {
		return 0, 0, 0
	}

	var row mibTcp6Row
	row.State = 5 // MIB_TCP_STATE_ESTAB
	copy(row.LocalAddr[:], lip)
	row.LocalScopeId = lScope
	row.LocalPort = htons32(uint16(localPort))
	copy(row.RemoteAddr[:], rip)
	row.RemoteScopeId = rScope
	row.RemotePort = htons32(uint16(remotePort))

	return queryEstats(uintptr(unsafe.Pointer(&row)), procGetPerTcp6ConnectionEStat, procSetPerTcp6ConnectionEStat)
}

func queryEstats(rowPtr uintptr, getProc, setProc *windows.LazyProc) (rxBytes uint64, txBytes uint64, rttMs uint32) {
	var dataRw tcpEstatsDataRwv0
	var dataRod tcpEstatsDataRodv0
	ret, _, _ := getProc.Call(
		rowPtr,
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&dataRw)), 0, uintptr(unsafe.Sizeof(dataRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&dataRod)), 0, uintptr(unsafe.Sizeof(dataRod)),
	)
	if ret == 0 {
		if dataRw.EnableCollection == 0 {
			dataRw.EnableCollection = 1
			sRet, _, _ := setProc.Call(
				rowPtr,
				tcpConnectionEstatsData,
				uintptr(unsafe.Pointer(&dataRw)), 0, uintptr(unsafe.Sizeof(dataRw)), 0,
			)
			if sRet == 0 {
				var checkRw tcpEstatsDataRwv0
				retRetry, _, _ := getProc.Call(
					rowPtr,
					tcpConnectionEstatsData,
					uintptr(unsafe.Pointer(&checkRw)), 0, uintptr(unsafe.Sizeof(checkRw)),
					0, 0, 0,
					uintptr(unsafe.Pointer(&dataRod)), 0, uintptr(unsafe.Sizeof(dataRod)),
				)
				if retRetry == 0 && checkRw.EnableCollection != 0 {
					rxBytes = dataRod.DataBytesIn
					txBytes = dataRod.DataBytesOut
				}
			}
		} else {
			rxBytes = dataRod.DataBytesIn
			txBytes = dataRod.DataBytesOut
		}
	}

	var pathRw tcpEstatsPathRwv0
	var pathRod tcpEstatsPathRodv0
	ret2, _, _ := getProc.Call(
		rowPtr,
		tcpConnectionEstatsPath,
		uintptr(unsafe.Pointer(&pathRw)), 0, uintptr(unsafe.Sizeof(pathRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&pathRod)), 0, uintptr(unsafe.Sizeof(pathRod)),
	)
	if ret2 == 0 {
		if pathRw.EnableCollection == 0 {
			pathRw.EnableCollection = 1
			setProc.Call(
				rowPtr,
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

func queryProcessImageNameAndPath(pid uint32) (string, string) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", ""
	}
	defer windows.CloseHandle(h)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	err = windows.QueryFullProcessImageName(h, 0, &buf[0], &size)
	if err != nil || size == 0 {
		return "", ""
	}
	fullPath := windows.UTF16ToString(buf[:size])
	return filepath.Base(fullPath), fullPath
}
