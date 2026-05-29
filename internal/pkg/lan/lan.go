// Package lan discovers private-range IPv4 addresses on the local
// host. Used by the admin "Detect LAN URLs" button, the install.sh
// Termux prompt, and `<app> config allowed-origins autodetect` to
// surface URLs reachable from other devices on the same network.
package lan

import "net"

// DiscoverPrivateIPv4 walks every up, non-loopback interface and
// returns each IPv4 address that looks routable on a LAN. Only
// private-range addresses (RFC1918) are kept so a host with a public
// IP doesn't accidentally suggest exposing itself. Order is stable
// per interface enumeration; duplicates are deduped.
func DiscoverPrivateIPv4() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]string, 0)
	seen := make(map[string]bool)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil || !ip4.IsPrivate() {
				continue
			}
			s := ip4.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
