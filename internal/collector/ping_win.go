//go:build windows

package collector

import (
	"encoding/binary"
	"net"
	"time"
	"unsafe"
)

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
)

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
