//go:build linux

package collector

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// GetSystemAdapters collects Linux network interface metadata via net.Interfaces, /sys/class/net, and /proc/net/route.
func GetSystemAdapters() map[string]AdapterMeta {
	result := make(map[string]AdapterMeta)
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}

	gwByIface, _ := readLinuxDefaultGateways()

	for _, iface := range ifaces {
		var ipv4, ipv6 string
		if addrs, err := iface.Addrs(); err == nil {
			for _, a := range addrs {
				ipNet, ok := a.(*net.IPNet)
				if !ok || ipNet.IP == nil {
					continue
				}
				ip := ipNet.IP
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
		}

		isLoopback := (iface.Flags & net.FlagLoopback) != 0
		isUp := (iface.Flags & net.FlagUp) != 0
		if state := readSysNetString(iface.Name, "operstate"); state != "" {
			if state == "down" || state == "notpresent" {
				isUp = false
			} else if state == "up" {
				isUp = true
			}
		}

		var linkSpeed uint64
		if speedStr := readSysNetString(iface.Name, "speed"); speedStr != "" {
			if mbps, err := strconv.ParseInt(speedStr, 10, 64); err == nil && mbps > 0 {
				linkSpeed = uint64(mbps) * 1_000_000
			}
		}

		mac := strings.ToUpper(iface.HardwareAddr.String())
		desc := describeLinuxInterface(iface.Name, isLoopback)

		result[iface.Name] = AdapterMeta{
			Name:        iface.Name,
			Description: desc,
			MAC:         mac,
			IPv4:        ipv4,
			IPv6:        ipv6,
			Gateway:     gwByIface[iface.Name],
			LinkSpeed:   linkSpeed,
			IsLoopback:  isLoopback,
			IsUp:        isUp,
		}
	}
	return result
}

// GetDefaultGatewayIP returns the primary active IPv4 default gateway address from /proc/net/route, or "".
func GetDefaultGatewayIP() string {
	_, primary := readLinuxDefaultGateways()
	return primary
}

func readLinuxDefaultGateways() (map[string]string, string) {
	byIface := make(map[string]string)
	bestMetric := int(^uint(0) >> 1)
	var primary string

	f, err := os.Open("/proc/net/route")
	if err != nil {
		return byIface, ""
	}
	defer f.Close()

	bestIfaceMetric := make(map[string]int)
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			continue
		}
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		iface := fields[0]
		destHex := fields[1]
		gwHex := fields[2]
		flagsVal, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil {
			continue
		}
		// RTF_UP = 0x1, RTF_GATEWAY = 0x2
		if destHex != "00000000" || (flagsVal&0x3) != 0x3 {
			continue
		}
		gwVal, err := strconv.ParseUint(gwHex, 16, 32)
		if err != nil || gwVal == 0 {
			continue
		}
		var b [4]byte
		binary.NativeEndian.PutUint32(b[:], uint32(gwVal))
		gwIP := net.IPv4(b[0], b[1], b[2], b[3]).String()

		metric := 0
		if m, err := strconv.Atoi(fields[6]); err == nil {
			metric = m
		}
		if prevM, ok := bestIfaceMetric[iface]; !ok || metric < prevM {
			bestIfaceMetric[iface] = metric
			byIface[iface] = gwIP
		}
		if metric < bestMetric {
			bestMetric = metric
			primary = gwIP
		}
	}
	return byIface, primary
}

func readSysNetString(iface, file string) string {
	data, err := os.ReadFile(filepath.Join("/sys/class/net", iface, file))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func describeLinuxInterface(name string, isLoopback bool) string {
	if isLoopback {
		return "Loopback Interface"
	}
	sysBase := filepath.Join("/sys/class/net", name)
	if _, err := os.Stat(filepath.Join(sysBase, "wireless")); err == nil {
		return fmt.Sprintf("Wi-Fi (%s)", name)
	}
	if _, err := os.Stat(filepath.Join(sysBase, "phy80211")); err == nil {
		return fmt.Sprintf("Wi-Fi (%s)", name)
	}
	switch {
	case strings.HasPrefix(name, "eth"), strings.HasPrefix(name, "en"):
		return fmt.Sprintf("Ethernet (%s)", name)
	case strings.HasPrefix(name, "wl"):
		return fmt.Sprintf("Wi-Fi (%s)", name)
	case strings.HasPrefix(name, "wg"), strings.HasPrefix(name, "tun"), strings.HasPrefix(name, "tap"):
		return fmt.Sprintf("VPN / Tunnel (%s)", name)
	case strings.HasPrefix(name, "docker"), strings.HasPrefix(name, "br-"), strings.HasPrefix(name, "veth"):
		return fmt.Sprintf("Virtual Bridge (%s)", name)
	case strings.HasPrefix(name, "usb"):
		return fmt.Sprintf("USB Network (%s)", name)
	default:
		return fmt.Sprintf("Network Interface (%s)", name)
	}
}
