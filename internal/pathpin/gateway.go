package pathpin

import (
	"fmt"
	"net"
)

// tableMain is RT_TABLE_MAIN. Netlink may also report main as 0.
const tableMain = 254

// pinRouteHint is a kernel IPv4 route subset used to pick a pin next-hop.
type pinRouteHint struct {
	LinkIndex int
	Dst       *net.IPNet
	Gw        net.IP
	Table     int
}

func tableIsMain(t int) bool {
	return t == 0 || t == tableMain
}

func isDefaultDst(dst *net.IPNet) bool {
	if dst == nil || dst.IP == nil {
		return true // netlink often encodes default as nil Dst
	}
	ip4 := dst.IP.To4()
	if ip4 == nil {
		return false
	}
	ones, bits := dst.Mask.Size()
	return bits == 32 && ones == 0 && ip4.IsUnspecified()
}

func dstContains(dst *net.IPNet, ip net.IP) bool {
	if isDefaultDst(dst) {
		return true
	}
	return dst.Contains(ip)
}

func prefixLen(dst *net.IPNet) int {
	if isDefaultDst(dst) {
		return 0
	}
	ones, _ := dst.Mask.Size()
	return ones
}

// pickGatewayForPin chooses a next-hop for dst on ifindex.
// Connected/on-link prefixes in main yield a nil gateway. Covering via-routes
// in main win over a default. Owned pin tables (18000–18999) are ignored so a
// leftover on-link /32 cannot poison selection. On Ethernet (broadcast), no
// next-hop is fail-closed; point-to-point may stay on-link.
func pickGatewayForPin(ifindex int, dst net.IP, routes []pinRouteHint, broadcast bool) (net.IP, error) {
	dst4 := dst.To4()
	if dst4 == nil {
		return nil, fmt.Errorf("%w: IPv4 destination required", ErrRefused)
	}

	onLink := false
	var bestGw net.IP
	bestLen := -1
	var defaultMain net.IP
	var defaultAny net.IP

	for _, r := range routes {
		if r.LinkIndex != ifindex {
			continue
		}
		if OwnedTable(r.Table) {
			continue
		}
		if !dstContains(r.Dst, dst4) {
			continue
		}
		main := tableIsMain(r.Table)
		plen := prefixLen(r.Dst)
		gw := r.Gw.To4()
		if gw == nil {
			if main && plen > 0 {
				onLink = true
			}
			continue
		}
		if isDefaultDst(r.Dst) {
			if main && defaultMain == nil {
				defaultMain = gw
			} else if defaultAny == nil {
				defaultAny = gw
			}
			continue
		}
		if !main {
			continue
		}
		if plen > bestLen {
			bestLen = plen
			bestGw = gw
		}
	}

	if onLink {
		return nil, nil
	}
	if bestGw != nil {
		return bestGw, nil
	}
	if defaultMain != nil {
		return defaultMain, nil
	}
	if defaultAny != nil {
		return defaultAny, nil
	}
	if !broadcast {
		return nil, nil
	}
	return nil, fmt.Errorf("%w: no IPv4 gateway for pin to %s (refusing on-link)", ErrRefused, dst4)
}
