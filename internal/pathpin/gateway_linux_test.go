//go:build linux

package pathpin

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestLiveNeverDefaultUSBGateway(t *testing.T) {
	const iface = "enp0s20f0u5"
	link, err := netlink.LinkByName(iface)
	if err != nil {
		t.Skipf("%s not present: %v", iface, err)
	}
	ifindex := link.Attrs().Index
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		t.Fatal(err)
	}
	hints := make([]pinRouteHint, 0, len(routes))
	for _, r := range routes {
		hints = append(hints, pinRouteHint{
			LinkIndex: r.LinkIndex,
			Dst:       r.Dst,
			Gw:        r.Gw,
			Table:     r.Table,
		})
	}
	dst := net.ParseIP("10.222.12.30")
	gw, err := pickGatewayForPin(ifindex, dst, hints, true)
	if err != nil {
		t.Fatal(err)
	}
	if gw == nil || gw.String() != "10.40.143.1" {
		t.Fatalf("live pick for %s via %s: gw=%v want 10.40.143.1", dst, iface, gw)
	}
}
