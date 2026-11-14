//go:build windows

package collector

import (
	"fmt"
	"net"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// AdapterMeta holds enriched Windows network adapter properties.
type AdapterMeta struct {
	Name        string
	Description string
	MAC         string
	IPv4        string
	IPv6        string
	Gateway     string
	LinkSpeed   uint64 // bits per second
	IsLoopback  bool
	IsUp        bool
}

// GetWindowsAdapters queries Windows GetAdaptersAddresses for rich NIC metadata.
func GetWindowsAdapters() map[string]AdapterMeta {
	result := make(map[string]AdapterMeta)

	var size uint32 = 32768
	var buf []byte
	var err error

	flags := uint32(windows.GAA_FLAG_INCLUDE_PREFIX | 0x0080) // 0x0080 = GAA_FLAG_INCLUDE_GATEWAY
	for i := 0; i < 3; i++ {
		buf = make([]byte, size)
		err = windows.GetAdaptersAddresses(
			windows.AF_UNSPEC,
			flags,
			0,
			(*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])),
			&size,
		)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return result
		}
	}
	if err != nil {
		return result
	}

	for aa := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); aa != nil; aa = aa.Next {
		friendlyName := windows.UTF16PtrToString(aa.FriendlyName)
		description := windows.UTF16PtrToString(aa.Description)

		var macParts []string
		if aa.PhysicalAddressLength > 0 && aa.PhysicalAddressLength <= 8 {
			for i := uint32(0); i < aa.PhysicalAddressLength; i++ {
				macParts = append(macParts, fmt.Sprintf("%02X", aa.PhysicalAddress[i]))
			}
		}
		mac := strings.Join(macParts, ":")

		var ipv4, ipv6 string
		for ua := aa.FirstUnicastAddress; ua != nil; ua = ua.Next {
			if ua.Address.Sockaddr == nil {
				continue
			}
			ip := sockaddrToIP(ua.Address.Sockaddr)
			if ip == nil {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				if ipv4 == "" {
					ipv4 = ip4.String()
				}
			} else if ipv6 == "" && !ip.IsLinkLocalUnicast() {
				ipv6 = ip.String()
			} else if ipv6 == "" {
				ipv6 = ip.String()
			}
		}

		var gwIP string
		for ga := aa.FirstGatewayAddress; ga != nil; ga = ga.Next {
			if ga.Address.Sockaddr == nil {
				continue
			}
			ip := sockaddrToIP(ga.Address.Sockaddr)
			if ip == nil {
				continue
			}
			if ip4 := ip.To4(); ip4 != nil {
				gwIP = ip4.String()
				break
			}
		}

		isLoopback := aa.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK
		isUp := aa.OperStatus == windows.IfOperStatusUp

		// TransmitLinkSpeed / ReceiveLinkSpeed are uint64 in bits/sec; MaxUint64 means unknown.
		linkSpeed := aa.ReceiveLinkSpeed
		if linkSpeed == ^uint64(0) {
			linkSpeed = 0
		}

		meta := AdapterMeta{
			Name:        friendlyName,
			Description: description,
			MAC:         mac,
			IPv4:        ipv4,
			IPv6:        ipv6,
			Gateway:     gwIP,
			LinkSpeed:   linkSpeed,
			IsLoopback:  isLoopback,
			IsUp:        isUp,
		}
		result[friendlyName] = meta
	}

	return result
}

// GetDefaultGatewayIP returns the primary active IPv4 default gateway address, or "".
func GetDefaultGatewayIP() string {
	adapters := GetWindowsAdapters()
	for _, a := range adapters {
		if a.IsUp && !a.IsLoopback && a.Gateway != "" {
			return a.Gateway
		}
	}
	return ""
}

func sockaddrToIP(sa *syscall.RawSockaddrAny) net.IP {
	if sa == nil {
		return nil
	}
	switch sa.Addr.Family {
	case windows.AF_INET:
		sa4 := (*syscall.RawSockaddrInet4)(unsafe.Pointer(sa))
		return net.IPv4(sa4.Addr[0], sa4.Addr[1], sa4.Addr[2], sa4.Addr[3])
	case windows.AF_INET6:
		sa6 := (*syscall.RawSockaddrInet6)(unsafe.Pointer(sa))
		ip := make(net.IP, net.IPv6len)
		copy(ip, sa6.Addr[:])
		return ip
	}
	return nil
}
