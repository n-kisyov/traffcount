//go:build linux

package collector

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	psnet "github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/unix"
)

const (
	sockDiagByFamily = 20
	sockDestroy      = 21
	inetDiagInfo     = 2
	tcpEstablished   = 1
)

type tcpSocketStats struct {
	rxBytes uint64
	txBytes uint64
	rttMs   uint32
}

type tcpSockKey struct {
	isV6  bool
	lIP   string
	lPort uint32
	rIP   string
	rPort uint32
}

var nlSeqCounter uint32

func newTCPMetricsLookup() tcpMetricsLookup {
	stats := make(map[tcpSockKey]tcpSocketStats)
	dumpLinuxInetDiagTCP(unix.AF_INET, stats)
	dumpLinuxInetDiagTCP(unix.AF_INET6, stats)

	return func(localIP string, localPort uint32, remoteIP string, remotePort uint32, isIPv6 bool) (uint64, uint64, uint32) {
		lip := normalizeIPString(localIP)
		rip := normalizeIPString(remoteIP)
		if st, ok := stats[tcpSockKey{isV6: isIPv6, lIP: lip, lPort: localPort, rIP: rip, rPort: remotePort}]; ok {
			return st.rxBytes, st.txBytes, st.rttMs
		}
		// Dual-stack IPv4-mapped IPv6 sockets may report AF_INET6 in one view and IPv4 text in another.
		if st, ok := stats[tcpSockKey{isV6: !isIPv6, lIP: lip, lPort: localPort, rIP: rip, rPort: remotePort}]; ok {
			return st.rxBytes, st.txBytes, st.rttMs
		}
		return 0, 0, 0
	}
}

func normalizeIPString(ipStr string) string {
	if idx := strings.IndexByte(ipStr, '%'); idx >= 0 {
		ipStr = ipStr[:idx]
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ipStr
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.String()
	}
	return ip.String()
}

func buildInetDiagReqV2(family uint8, ext uint8, states uint32, lIP net.IP, lPort uint16, rIP net.IP, rPort uint16) []byte {
	// sizeof(nlmsghdr)=16 + sizeof(inet_diag_req_v2)=56 = 72 bytes
	buf := make([]byte, 72)
	nlh := (*unix.NlMsghdr)(unsafe.Pointer(&buf[0]))
	nlh.Len = uint32(len(buf))
	nlh.Type = sockDiagByFamily
	nlh.Flags = unix.NLM_F_REQUEST | unix.NLM_F_DUMP
	nlh.Seq = atomic.AddUint32(&nlSeqCounter, 1)

	req := buf[16:]
	req[0] = family
	req[1] = unix.IPPROTO_TCP
	req[2] = ext
	req[3] = 0
	binary.NativeEndian.PutUint32(req[4:8], states)

	// inet_diag_sockid starts at req[8..56] (48 bytes)
	id := req[8:]
	binary.BigEndian.PutUint16(id[0:2], lPort)
	binary.BigEndian.PutUint16(id[2:4], rPort)
	writeSockIDAddr(id[4:20], family, lIP)
	writeSockIDAddr(id[20:36], family, rIP)
	// idiag_cookie = [0xffffffff, 0xffffffff]
	for i := 40; i < 48; i++ {
		id[i] = 0xff
	}
	return buf
}

func writeSockIDAddr(dst []byte, family uint8, ip net.IP) {
	if ip == nil {
		return
	}
	if family == unix.AF_INET {
		if ip4 := ip.To4(); ip4 != nil {
			copy(dst[:4], ip4)
		}
		return
	}
	if ip16 := ip.To16(); ip16 != nil {
		copy(dst[:16], ip16)
	}
}

func parseInetDiagMsg(data []byte) (key tcpSockKey, st tcpSocketStats, ok bool) {
	if len(data) < 72 {
		return key, st, false
	}
	family := data[0]
	id := data[4:52]
	lPort := uint32(binary.BigEndian.Uint16(id[0:2]))
	rPort := uint32(binary.BigEndian.Uint16(id[2:4]))

	var lIP, rIP string
	var isV6 bool
	if family == unix.AF_INET {
		lIP = net.IP(id[4:8]).String()
		rIP = net.IP(id[20:24]).String()
	} else if family == unix.AF_INET6 {
		isV6 = true
		lIP = net.IP(id[4:20]).String()
		rIP = net.IP(id[20:36]).String()
	} else {
		return key, st, false
	}

	key = tcpSockKey{
		isV6:  isV6,
		lIP:   normalizeIPString(lIP),
		lPort: lPort,
		rIP:   normalizeIPString(rIP),
		rPort: rPort,
	}

	// Parse netlink rtattr extensions after 72-byte inet_diag_msg
	attrs := data[72:]
	for len(attrs) >= 4 {
		attrLen := int(binary.NativeEndian.Uint16(attrs[0:2]))
		attrType := binary.NativeEndian.Uint16(attrs[2:4])
		if attrLen < 4 || attrLen > len(attrs) {
			break
		}
		if attrType == inetDiagInfo && attrLen > 4 {
			payload := attrs[4:attrLen]
			var info unix.TCPInfo
			dst := (*[unsafe.Sizeof(info)]byte)(unsafe.Pointer(&info))[:]
			copy(dst, payload)

			rx := info.Bytes_received
			tx := info.Bytes_acked
			if info.Bytes_sent > tx {
				tx = info.Bytes_sent
			}
			var rttMs uint32
			if info.Rtt > 0 {
				rttMs = info.Rtt / 1000 // Rtt in tcp_info is microseconds
				if rttMs == 0 {
					rttMs = 1
				}
			}
			st = tcpSocketStats{rxBytes: rx, txBytes: tx, rttMs: rttMs}
			break
		}
		aligned := (attrLen + 3) &^ 3
		if aligned > len(attrs) {
			break
		}
		attrs = attrs[aligned:]
	}
	return key, st, true
}

func dumpLinuxInetDiagTCP(family uint8, out map[tcpSockKey]tcpSocketStats) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM, unix.NETLINK_INET_DIAG)
	if err != nil {
		return
	}
	defer unix.Close(fd)

	tv := unix.NsecToTimeval((500 * time.Millisecond).Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)

	sa := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	if err := unix.Bind(fd, sa); err != nil {
		return
	}

	req := buildInetDiagReqV2(family, 1<<(inetDiagInfo-1), 1<<tcpEstablished, nil, 0, nil, 0)
	if err := unix.Sendto(fd, req, 0, sa); err != nil {
		return
	}

	buf := make([]byte, 32768)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if n == 0 {
			return
		}
		msgs := parseNetlinkMessages(buf[:n])
		for _, m := range msgs {
			switch m.hdr.Type {
			case unix.NLMSG_DONE, unix.NLMSG_ERROR:
				return
			case sockDiagByFamily:
				if k, st, ok := parseInetDiagMsg(m.data); ok {
					out[k] = st
				}
			}
		}
	}
}

func enforceBlockedSocket(localIP string, localPort uint32, remoteIP string, remotePort uint32, isIPv6 bool) {
	if os.Geteuid() != 0 {
		return
	}
	_ = closeTCPSocketDiag(localIP, localPort, remoteIP, remotePort, isIPv6)
}

func specialPIDIdentity(pid int32) (string, string, bool) {
	if pid <= 0 {
		return "Kernel", "", true
	}
	return "", "", false
}

// CloseTCPConnectionV4 forcefully destroys an established TCP socket via Linux NETLINK_INET_DIAG SOCK_DESTROY.
func CloseTCPConnectionV4(localIP string, localPort uint32, remoteIP string, remotePort uint32) error {
	isV6 := strings.Contains(localIP, ":") || strings.Contains(remoteIP, ":")
	return closeTCPSocketDiag(localIP, localPort, remoteIP, remotePort, isV6)
}

func closeTCPSocketDiag(localIP string, localPort uint32, remoteIP string, remotePort uint32, isV6 bool) error {
	if idx := strings.IndexByte(localIP, '%'); idx >= 0 {
		localIP = localIP[:idx]
	}
	if idx := strings.IndexByte(remoteIP, '%'); idx >= 0 {
		remoteIP = remoteIP[:idx]
	}
	lip := net.ParseIP(localIP)
	rip := net.ParseIP(remoteIP)
	if lip == nil || rip == nil {
		return fmt.Errorf("invalid socket endpoint IP address")
	}

	var family uint8 = unix.AF_INET
	if isV6 || lip.To4() == nil || rip.To4() == nil {
		family = unix.AF_INET6
	}

	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM, unix.NETLINK_INET_DIAG)
	if err != nil {
		return fmt.Errorf("netlink socket failed: %w", err)
	}
	defer unix.Close(fd)

	sa := &unix.SockaddrNetlink{Family: unix.AF_NETLINK}
	if err := unix.Bind(fd, sa); err != nil {
		return fmt.Errorf("netlink bind failed: %w", err)
	}

	req := buildInetDiagReqV2(family, 0, 1<<tcpEstablished, lip, uint16(localPort), rip, uint16(remotePort))
	nlh := (*unix.NlMsghdr)(unsafe.Pointer(&req[0]))
	nlh.Type = sockDestroy
	nlh.Flags = unix.NLM_F_REQUEST | unix.NLM_F_ACK

	if err := unix.Sendto(fd, req, 0, sa); err != nil {
		return fmt.Errorf("SOCK_DESTROY send failed: %w", err)
	}

	tv := unix.NsecToTimeval((500 * time.Millisecond).Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)

	buf := make([]byte, 4096)
	n, _, err := unix.Recvfrom(fd, buf, 0)
	if err != nil {
		return fmt.Errorf("SOCK_DESTROY ack failed: %w", err)
	}
	msgs := parseNetlinkMessages(buf[:n])
	if len(msgs) == 0 {
		return fmt.Errorf("invalid netlink response")
	}
	if msgs[0].hdr.Type == unix.NLMSG_ERROR && len(msgs[0].data) >= 4 {
		errno := int32(binary.NativeEndian.Uint32(msgs[0].data[:4]))
		if errno == 0 {
			return nil
		}
		sysErr := unix.Errno(-errno)
		if sysErr == unix.EPERM || sysErr == unix.EACCES {
			return fmt.Errorf("root privileges (sudo or systemd service) required to close sockets")
		}
		return fmt.Errorf("SOCK_DESTROY failed: %s", sysErr.Error())
	}
	return nil
}

type rawNetlinkMsg struct {
	hdr  unix.NlMsghdr
	data []byte
}

func parseNetlinkMessages(b []byte) []rawNetlinkMsg {
	var out []rawNetlinkMsg
	const hdrLen = unix.NLMSG_HDRLEN
	for len(b) >= hdrLen {
		hdr := *(*unix.NlMsghdr)(unsafe.Pointer(&b[0]))
		msgLen := int(hdr.Len)
		if msgLen < hdrLen || msgLen > len(b) {
			break
		}
		out = append(out, rawNetlinkMsg{
			hdr:  hdr,
			data: b[hdrLen:msgLen],
		})
		aligned := (msgLen + unix.NLMSG_ALIGNTO - 1) &^ (unix.NLMSG_ALIGNTO - 1)
		if aligned >= len(b) {
			break
		}
		b = b[aligned:]
	}
	return out
}

// TerminateProcessByPID sends SIGKILL to a non-system PID on Linux.
func TerminateProcessByPID(pid int32) error {
	if pid <= 1 {
		return fmt.Errorf("cannot terminate system process PID %d", pid)
	}
	if err := unix.Kill(int(pid), unix.SIGKILL); err != nil {
		if err == unix.EPERM {
			return fmt.Errorf("root privileges (sudo or systemd service) required to terminate PID %d", pid)
		}
		return fmt.Errorf("kill PID %d failed: %w", pid, err)
	}
	return nil
}

// SetFirewallBlockRule records/enforces active socket blocking for an application on Linux (requires root).
func SetFirewallBlockRule(processName, exePath string, block bool) error {
	cleanName := strings.TrimSpace(processName)
	if cleanName == "" {
		return fmt.Errorf("process name is required")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("root privileges (sudo or systemd service) required to enforce socket blocking")
	}
	if !block {
		return nil
	}

	// Immediately terminate active non-loopback TCP sockets belonging to this process.
	stats, err := psnet.Connections("tcp")
	if err != nil {
		return nil
	}
	now := time.Now()
	targetLower := strings.ToLower(cleanName)
	for _, st := range stats {
		if st.Status != "ESTABLISHED" || st.Pid <= 1 {
			continue
		}
		pName, pExe := resolveProcessIdentity(st.Pid, now)
		if strings.ToLower(pName) == targetLower || (exePath != "" && pExe == exePath) {
			if !isLoopbackEndpoint(st.Laddr.IP) && !isLoopbackEndpoint(st.Raddr.IP) {
				isV6 := strings.Contains(st.Laddr.IP, ":") || strings.Contains(st.Raddr.IP, ":")
				_ = closeTCPSocketDiag(st.Laddr.IP, st.Laddr.Port, st.Raddr.IP, st.Raddr.Port, isV6)
			}
		}
	}
	return nil
}

func queryProcessImageNameAndPath(pid uint32) (string, string) {
	procDir := fmt.Sprintf("/proc/%d", pid)
	exePath, err := os.Readlink(filepath.Join(procDir, "exe"))
	if err == nil {
		exePath = strings.TrimSuffix(exePath, " (deleted)")
	}
	commBytes, errComm := os.ReadFile(filepath.Join(procDir, "comm"))
	comm := ""
	if errComm == nil {
		comm = strings.TrimSpace(string(commBytes))
	}

	baseExe := ""
	if exePath != "" {
		baseExe = filepath.Base(exePath)
	}

	// Prefer full untruncated binary name when /proc/<pid>/comm was truncated to 15 chars (TASK_COMM_LEN).
	if baseExe != "" && (comm == "" || (len(comm) >= 14 && strings.HasPrefix(baseExe, comm))) {
		return baseExe, exePath
	}
	if comm != "" {
		return comm, exePath
	}
	return baseExe, exePath
}
