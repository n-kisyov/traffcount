//go:build windows

package collector

import (
	"net"
	"testing"
	"time"
	"unsafe"
)

func TestEstatsStructSizes(t *testing.T) {
	if sz := unsafe.Sizeof(mibTcpRow{}); sz != 20 {
		t.Fatalf("expected sizeof(mibTcpRow) == 20, got %d", sz)
	}
	if sz := unsafe.Sizeof(mibTcp6Row{}); sz != 52 {
		t.Fatalf("expected sizeof(mibTcp6Row) == 52, got %d", sz)
	}
	if sz := unsafe.Sizeof(tcpEstatsDataRodv0{}); sz != 96 {
		t.Fatalf("expected sizeof(tcpEstatsDataRodv0) == 96, got %d", sz)
	}
	if sz := unsafe.Sizeof(tcpEstatsPathRodv0{}); sz != 160 {
		t.Fatalf("expected sizeof(tcpEstatsPathRodv0) == 160, got %d", sz)
	}
}

func TestLiveTCPEStatsV4Probe(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		for {
			_, err := c.Read(buf)
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("PONG_DATA_PAYLOAD_1234567890"))
		}
	}()

	conn, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	laddr := conn.LocalAddr().(*net.TCPAddr)
	raddr := conn.RemoteAddr().(*net.TCPAddr)
	lip := laddr.IP.To4()
	rip := raddr.IP.To4()
	row := mibTcpRow{
		State:      5,
		LocalAddr:  *(*uint32)(unsafe.Pointer(&lip[0])),
		LocalPort:  htons32(uint16(laddr.Port)),
		RemoteAddr: *(*uint32)(unsafe.Pointer(&rip[0])),
		RemotePort: htons32(uint16(raddr.Port)),
	}

	var dataRw tcpEstatsDataRwv0
	var dataRod tcpEstatsDataRodv0
	ret1, _, _ := procGetPerTcpConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&dataRw)), 0, uintptr(unsafe.Sizeof(dataRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&dataRod)), 0, uintptr(unsafe.Sizeof(dataRod)),
	)

	dataRw.EnableCollection = 1
	setRet, _, _ := procSetPerTcpConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&dataRw)), 0, uintptr(unsafe.Sizeof(dataRw)), 0,
	)

	_, _ = conn.Write(make([]byte, 2048))
	buf := make([]byte, 128)
	_, _ = conn.Read(buf)
	time.Sleep(50 * time.Millisecond)

	var dataRw2 tcpEstatsDataRwv0
	var dataRod2 tcpEstatsDataRodv0
	ret2, _, _ := procGetPerTcpConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&dataRw2)), 0, uintptr(unsafe.Sizeof(dataRw2)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&dataRod2)), 0, uintptr(unsafe.Sizeof(dataRod2)),
	)

	var pathRw tcpEstatsPathRwv0
	var pathRod tcpEstatsPathRodv0
	pathRet, _, _ := procGetPerTcpConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsPath,
		uintptr(unsafe.Pointer(&pathRw)), 0, uintptr(unsafe.Sizeof(pathRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&pathRod)), 0, uintptr(unsafe.Sizeof(pathRod)),
	)

	if setRet == 0 {
		if dataRw2.EnableCollection == 0 {
			t.Errorf("expected EnableCollection != 0 after SetPerTcpConnectionEStats succeeded")
		}
		if dataRod2.DataBytesIn == 0 || dataRod2.DataBytesOut == 0 {
			t.Errorf("expected non-zero DataBytesIn/Out when EStats enabled, got in=%d out=%d", dataRod2.DataBytesIn, dataRod2.DataBytesOut)
		}
	}
	if ret1 != 0 || ret2 != 0 {
		t.Errorf("expected GetPerTcpConnectionEStats ret=0, got ret1=%d ret2=%d", ret1, ret2)
	}
	_ = pathRet
	_ = pathRw
	_ = pathRod
}

func TestLiveTCPEStatsV6Probe(t *testing.T) {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 256)
		_, _ = c.Read(buf)
	}()

	conn, err := net.Dial("tcp6", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	laddr := conn.LocalAddr().(*net.TCPAddr)
	raddr := conn.RemoteAddr().(*net.TCPAddr)
	var row mibTcp6Row
	row.State = 5
	copy(row.LocalAddr[:], laddr.IP.To16())
	row.LocalPort = htons32(uint16(laddr.Port))
	copy(row.RemoteAddr[:], raddr.IP.To16())
	row.RemotePort = htons32(uint16(raddr.Port))

	var dataRw tcpEstatsDataRwv0
	var dataRod tcpEstatsDataRodv0
	ret, _, _ := procGetPerTcp6ConnectionEStat.Call(
		uintptr(unsafe.Pointer(&row)),
		tcpConnectionEstatsData,
		uintptr(unsafe.Pointer(&dataRw)), 0, uintptr(unsafe.Sizeof(dataRw)),
		0, 0, 0,
		uintptr(unsafe.Pointer(&dataRod)), 0, uintptr(unsafe.Sizeof(dataRod)),
	)
	if ret != 0 {
		t.Errorf("expected GetPerTcp6ConnectionEStats ret=0, got %d", ret)
	}
}

func TestICMPStructAndPingHelpers(t *testing.T) {
	if sz := unsafe.Sizeof(icmpEchoReply{}); sz != 40 {
		t.Fatalf("expected sizeof(icmpEchoReply) == 40, got %d", sz)
	}
	ms, ok := icmpPingIPv4(net.IPv4(127, 0, 0, 1), 800*time.Millisecond)
	if !ok || ms < 1 {
		t.Errorf("expected localhost ICMP ping success, got ok=%v ms=%d", ok, ms)
	}

	ip, scope := parseIPv6AndScope("fe80::1%12")
	if ip == nil || scope != 12 {
		t.Errorf("expected fe80::1 with scope 12, got ip=%v scope=%d", ip, scope)
	}

	// Non-DNS /8 ranges like 52.94.x.x (AWS) must NOT be pre-labeled Azure
	host, cc, _ := classifyWellKnownIP("52.94.236.248")
	if host != "" || cc != "" {
		t.Errorf("expected 52.94.236.248 not statically classified, got host=%q cc=%q", host, cc)
	}
}
