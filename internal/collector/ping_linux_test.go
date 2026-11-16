//go:build linux

package collector

import (
	"testing"
)

func TestICMPEchoPacketAndReplyParsing(t *testing.T) {
	payload := []byte("traffcount-ping!")
	pkt := buildICMPEchoPacket(0x1234, 7, payload)
	if icmpChecksum(pkt) != 0 {
		t.Fatalf("expected valid ICMP checksum complement 0, got %#x", icmpChecksum(pkt))
	}

	reply := make([]byte, len(pkt))
	copy(reply, pkt)
	reply[0] = 0 // Echo Reply
	if !parseICMPEchoReply(reply, false, 0x9999, 7, payload) {
		t.Fatalf("expected SOCK_DGRAM Echo Reply with kernel-rewritten ID to match")
	}

	ipReply := make([]byte, 20+len(reply))
	ipReply[0] = 0x45 // IPv4, IHL=20
	copy(ipReply[20:], reply)
	if !parseICMPEchoReply(ipReply, true, 0x1234, 7, payload) {
		t.Fatalf("expected SOCK_RAW Echo Reply to match")
	}
	if parseICMPEchoReply(ipReply, true, 0x1235, 7, payload) {
		t.Fatalf("expected mismatched SOCK_RAW Echo ID to be rejected")
	}
}
