package netinfo

import (
	"fmt"
	"net"
	"strings"

	"github.com/godbus/dbus/v5"
)

// LinkDNSServers returns IPv4 DNS servers configured on iface via systemd-resolved
// (org.freedesktop.resolve1). Empty if resolved is unavailable or the link has none.
func LinkDNSServers(iface string) []string {
	iface = strings.TrimSpace(iface)
	if iface == "" || iface == "auto" {
		return nil
	}
	ifi, err := net.InterfaceByName(iface)
	if err != nil || ifi == nil {
		return nil
	}
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil
	}
	mgr := conn.Object("org.freedesktop.resolve1", "/org/freedesktop/resolve1")
	var path dbus.ObjectPath
	if err := mgr.Call("org.freedesktop.resolve1.Manager.GetLink", 0, int32(ifi.Index)).Store(&path); err != nil {
		return nil
	}
	link := conn.Object("org.freedesktop.resolve1", path)
	var v dbus.Variant
	if err := link.Call("org.freedesktop.DBus.Properties.Get", 0,
		"org.freedesktop.resolve1.Link", "DNS").Store(&v); err != nil {
		return nil
	}
	return parseResolve1DNS(v.Value())
}

func parseResolve1DNS(raw any) []string {
	// a(iay): []struct{ Family int32; Address []byte } — dbus often yields [][]any
	var out []string
	switch rows := raw.(type) {
	case [][]any:
		for _, row := range rows {
			if ip := dnsRowToIP(row); ip != nil && ip.To4() != nil {
				out = append(out, ip.To4().String())
			}
		}
	case []any:
		for _, row := range rows {
			switch r := row.(type) {
			case []any:
				if ip := dnsRowToIP(r); ip != nil && ip.To4() != nil {
					out = append(out, ip.To4().String())
				}
			case dbus.Variant:
				if nested, ok := r.Value().([]any); ok {
					if ip := dnsRowToIP(nested); ip != nil && ip.To4() != nil {
						out = append(out, ip.To4().String())
					}
				}
			}
		}
	}
	return out
}

func dnsRowToIP(row []any) net.IP {
	if len(row) < 2 {
		return nil
	}
	var family int32
	switch f := row[0].(type) {
	case int32:
		family = f
	case int64:
		family = int32(f)
	case int:
		family = int32(f)
	default:
		return nil
	}
	addr, ok := row[1].([]byte)
	if !ok {
		// sometimes []uint8 already, or dbus.Variant
		if v, ok := row[1].(dbus.Variant); ok {
			addr, _ = v.Value().([]byte)
		}
	}
	if len(addr) == 0 {
		return nil
	}
	switch family {
	case int32(syscallAFInet):
		if len(addr) >= 4 {
			return net.IP(addr[:4])
		}
	case int32(syscallAFInet6):
		if len(addr) >= 16 {
			return net.IP(addr[:16])
		}
	default:
		// Family may already be omitted; try length
		if len(addr) == 4 {
			return net.IP(addr)
		}
		if len(addr) == 16 {
			return net.IP(addr)
		}
	}
	return nil
}

// AF_INET / AF_INET6 without importing syscall names that differ — use constants.
const (
	syscallAFInet  = 2
	syscallAFInet6 = 10
)

// FormatLinkDNS is a debug helper.
func FormatLinkDNS(iface string) string {
	s := LinkDNSServers(iface)
	if len(s) == 0 {
		return fmt.Sprintf("%s: (no link DNS)", iface)
	}
	return fmt.Sprintf("%s: %s", iface, strings.Join(s, ", "))
}
