//go:build linux

package collector

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

var icmpSeqCounter uint32

func icmpPingIPv4(ip4 net.IP, timeout time.Duration) (int64, bool) {
	ipBytes := ip4.To4()
	if ipBytes == nil {
		return 0, false
	}

	// Prefer unprivileged ICMP datagram socket (default allowed on Raspberry Pi OS),
	// falling back to raw ICMP socket when running as root.
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_ICMP)
	isRaw := false
	if err != nil {
		fd, err = unix.Socket(unix.AF_INET, unix.SOCK_RAW, unix.IPPROTO_ICMP)
		if err != nil {
			return 0, false
		}
		isRaw = true
	}
	defer unix.Close(fd)

	id := uint16(os.Getpid() & 0xffff)
	seq := uint16(atomic.AddUint32(&icmpSeqCounter, 1))
	payload := []byte("traffcount-ping!")
	pkt := buildICMPEchoPacket(id, seq, payload)

	var addr [4]byte
	copy(addr[:], ipBytes)
	sa := &unix.SockaddrInet4{Addr: addr}

	start := time.Now()
	if err := unix.Sendto(fd, pkt, 0, sa); err != nil {
		return 0, false
	}

	deadline := start.Add(timeout)
	buf := make([]byte, 512)
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			return 0, false
		}
		tv := unix.NsecToTimeval(rem.Nanoseconds())
		if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
			return 0, false
		}

		n, from, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, false
		}
		if from4, ok := from.(*unix.SockaddrInet4); ok && from4.Addr != addr {
			continue
		}
		if parseICMPEchoReply(buf[:n], isRaw, id, seq, payload) {
			rtt := time.Since(start).Milliseconds()
			if rtt < 1 {
				rtt = 1
			}
			return rtt, true
		}
	}
}

func buildICMPEchoPacket(id, seq uint16, payload []byte) []byte {
	pkt := make([]byte, 8+len(payload))
	pkt[0] = 8 // Echo Request
	pkt[1] = 0
	binary.BigEndian.PutUint16(pkt[4:6], id)
	binary.BigEndian.PutUint16(pkt[6:8], seq)
	copy(pkt[8:], payload)
	csum := icmpChecksum(pkt)
	binary.BigEndian.PutUint16(pkt[2:4], csum)
	return pkt
}

func parseICMPEchoReply(pkt []byte, hasIPv4Header bool, expectedID, expectedSeq uint16, expectedPayload []byte) bool {
	offset := 0
	if hasIPv4Header {
		if len(pkt) < 20 || (pkt[0]>>4) != 4 {
			return false
		}
		ihl := int(pkt[0]&0x0f) * 4
		if ihl < 20 || len(pkt) < ihl+8 {
			return false
		}
		offset = ihl
	}
	icmp := pkt[offset:]
	if len(icmp) < 8 || icmp[0] != 0 || icmp[1] != 0 {
		return false
	}
	// On Linux SOCK_DGRAM ICMP sockets, the kernel rewrites the Echo ID field with the socket's port,
	// so only require ID match on SOCK_RAW.
	if hasIPv4Header && binary.BigEndian.Uint16(icmp[4:6]) != expectedID {
		return false
	}
	if binary.BigEndian.Uint16(icmp[6:8]) != expectedSeq {
		return false
	}
	return bytes.Equal(icmp[8:], expectedPayload)
}

func icmpChecksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	if len(data)%2 == 1 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for (sum >> 16) != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
