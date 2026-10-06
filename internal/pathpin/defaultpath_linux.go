//go:build linux

package pathpin

import (
	"fmt"
	"net"
	"strings"

	"github.com/vishvananda/netlink"
)

// ApplyDefaultPath installs 0.0.0.0/0 in DefaultPathTable via iface and a catch-all
// policy rule. It never edits the main-table default route or toggles links.
// Connected IPv4 prefixes already in main get preserve rules (lookup main) so LAN
// stays reachable before the catch-all.
func (a *NetlinkApplier) ApplyDefaultPath(iface string) error {
	if iface == "lo" {
		return fmt.Errorf("%w: refusing loopback for default path", ErrRefused)
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("%w: interface %q: %v", ErrRefused, iface, err)
	}
	attrs := link.Attrs()
	if attrs.Flags&net.FlagUp == 0 {
		return fmt.Errorf("%w: interface %q is down (fail closed)", ErrRefused, iface)
	}
	gw := gatewayForLink(attrs.Index)
	if gw == nil && link.Attrs().Flags&net.FlagBroadcast != 0 {
		// Ethernet defaults need a next-hop. scope-link 0.0.0.0/0 blackholes the Internet.
		return fmt.Errorf("%w: no IPv4 gateway found for %q in main (refusing on-link default)", ErrRefused, iface)
	}
	preserve := mainConnectedIPv4()
	plan, err := BuildDefaultPathPlan(iface, attrs.Index, gw, preserve)
	if err != nil {
		return err
	}

	dstDefault := &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)}
	route := &netlink.Route{
		LinkIndex: plan.IfIndex,
		Dst:       dstDefault,
		Table:     plan.Table,
		Scope:     netlink.SCOPE_UNIVERSE,
	}
	if plan.Gateway != nil {
		route.Gw = plan.Gateway
	} else {
		route.Scope = netlink.SCOPE_LINK
	}
	if err := netlink.RouteReplace(route); err != nil {
		if isPerm(err) {
			return FormatPrivError(err)
		}
		return fmt.Errorf("pathpin: default-path route replace: %w", err)
	}

	_ = deleteOwnedDefaultPathRules()
	for _, pr := range plan.Preserve {
		if pr.Dst == nil {
			continue
		}
		rule := netlink.NewRule()
		rule.Family = netlink.FAMILY_V4
		rule.Table = unixTableMain
		rule.Priority = pr.Priority
		rule.Dst = pr.Dst
		if err := netlink.RuleAdd(rule); err != nil {
			if isPerm(err) {
				_ = a.RemoveDefaultPath(iface)
				return FormatPrivError(err)
			}
			// Duplicate rule is fine on re-apply.
			if !isExists(err) {
				_ = a.RemoveDefaultPath(iface)
				return fmt.Errorf("pathpin: preserve rule add: %w", err)
			}
		}
	}

	catch := netlink.NewRule()
	catch.Family = netlink.FAMILY_V4
	catch.Table = plan.Table
	catch.Priority = plan.Priority
	if err := netlink.RuleAdd(catch); err != nil {
		_ = a.RemoveDefaultPath(iface)
		if isPerm(err) {
			return FormatPrivError(err)
		}
		if isExists(err) {
			return nil
		}
		return fmt.Errorf("pathpin: default-path rule add: %w", err)
	}
	return nil
}

// RemoveDefaultPath removes owned default-path rules/routes for iface's table.
func (a *NetlinkApplier) RemoveDefaultPath(iface string) error {
	_ = deleteOwnedDefaultPathRules()
	route := &netlink.Route{
		Dst:   &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
		Table: DefaultPathTable,
	}
	if err := netlink.RouteDel(route); err != nil && !isNotExists(err) {
		if isPerm(err) {
			return FormatPrivError(err)
		}
	}
	_ = iface // table is reserved globally for default-path in MVP
	return nil
}

const unixTableMain = 254

func mainConnectedIPv4() []PreserveSpec {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil
	}
	var out []PreserveSpec
	seen := map[string]struct{}{}
	for _, r := range routes {
		if r.Table != unixTableMain && r.Table != 0 {
			// RouteList often returns main as 0 or 254 depending on kernel/netlink.
			continue
		}
		if r.Dst == nil || r.Dst.IP == nil {
			continue // skip default
		}
		ip4 := r.Dst.IP.To4()
		if ip4 == nil {
			continue
		}
		ones, bits := r.Dst.Mask.Size()
		if bits != 32 || ones == 0 {
			continue
		}
		key := r.Dst.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		// Copy IPNet — netlink may reuse buffers.
		n := &net.IPNet{
			IP:   append(net.IP(nil), ip4...),
			Mask: append(net.IPMask(nil), r.Dst.Mask...),
		}
		out = append(out, PreserveSpec{Dst: n, Priority: MainPreservePref})
	}
	return out
}

func deleteOwnedDefaultPathRules() error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		if isPerm(err) {
			return FormatPrivError(err)
		}
		return err
	}
	for i := range rules {
		r := rules[i]
		switch r.Priority {
		case DefaultPathPref:
			if r.Table == DefaultPathTable {
				_ = netlink.RuleDel(&r)
			}
		case MainPreservePref:
			_ = netlink.RuleDel(&r)
		}
	}
	return nil
}

func isExists(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "file exists") || strings.Contains(msg, "exists")
}
