//go:build linux

package collector

import (
	"encoding/binary"
	"net"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestBuildAndParseInetDiagV4(t *testing.T) {
	lip := net.ParseIP("192.168.1.50").To4()
	rip := net.ParseIP("1.1.1.1").To4()
	req := buildInetDiagReqV2(unix.AF_INET, 1<<(inetDiagInfo-1), 1<<tcpEstablished, lip, 54321, rip, 443)
	if len(req) != 72 {
		t.Fatalf("expected 72 byte netlink inet_diag_req_v2 packet, got %d", len(req))
	}
	if req[16] != unix.AF_INET || req[17] != unix.IPPROTO_TCP {
		t.Fatalf("unexpected family/protocol in request header: %d/%d", req[16], req[17])
	}
	if got := binary.BigEndian.Uint16(req[24:26]); got != 54321 {
		t.Fatalf("expected sport 54321, got %d", got)
	}
	if got := binary.BigEndian.Uint16(req[26:28]); got != 443 {
		t.Fatalf("expected dport 443, got %d", got)
	}

	// Simulate kernel inet_diag_msg (72 bytes) + INET_DIAG_INFO rtattr
	var info unix.TCPInfo
	info.Rtt = 14500 // 14.5ms -> 14ms
	info.Bytes_received = 123456
	info.Bytes_acked = 7890
	infoBytes := (*[unsafe.Sizeof(info)]byte)(unsafe.Pointer(&info))[:]

	attrLen := 4 + len(infoBytes)
	msg := make([]byte, 72+((attrLen+3)&^3))
	msg[0] = unix.AF_INET
	id := msg[4:52]
	binary.BigEndian.PutUint16(id[0:2], 54321)
	binary.BigEndian.PutUint16(id[2:4], 443)
	copy(id[4:8], lip)
	copy(id[20:24], rip)

	attr := msg[72:]
	binary.NativeEndian.PutUint16(attr[0:2], uint16(attrLen))
	binary.NativeEndian.PutUint16(attr[2:4], inetDiagInfo)
	copy(attr[4:], infoBytes)

	key, st, ok := parseInetDiagMsg(msg)
	if !ok {
		t.Fatalf("expected parseInetDiagMsg to succeed")
	}
	if key.lIP != "192.168.1.50" || key.rIP != "1.1.1.1" || key.lPort != 54321 || key.rPort != 443 {
		t.Fatalf("unexpected parsed socket key: %+v", key)
	}
	if st.rxBytes != 123456 || st.txBytes != 7890 || st.rttMs != 14 {
		t.Fatalf("unexpected parsed TCPInfo metrics: %+v", st)
	}
}
