//go:build linux

package pathpin

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NetlinkApplier installs /32 destination pins via a dedicated policy-routing table.
// It never changes the main table default route, never toggles links, and only
// mutates tables/priorities in the owned ranges (see plan.go).
type NetlinkApplier struct{}

// NewNetlink returns a live applier. Call ProbeNetAdmin first for a clear error.
func NewNetlink() *NetlinkApplier {
	return &NetlinkApplier{}
}

// ProbeNetAdmin checks whether policy routing is readable (needs CAP_NET_ADMIN for writes).
// A successful RuleList does not guarantee write access; Apply still handles EPERM.
func ProbeNetAdmin() error {
	_, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return FormatPrivError(err)
	}
	if os.Geteuid() != 0 && !hasCapNetAdmin() {
		// Best-effort: non-root without cap likely cannot add rules.
		return FormatPrivError(fmt.Errorf("euid=%d and CAP_NET_ADMIN not detected", os.Geteuid()))
	}
	return nil
}

func hasCapNetAdmin() bool {
	// Read effective capability set from /proc; avoid panics on exotic setups.
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	// CapEff: hex bitmask; CAP_NET_ADMIN is bit 12.
	const capNetAdmin = 1 << 12
	var capEff uint64
	for _, line := range splitLines(string(data)) {
		if len(line) > 8 && line[:7] == "CapEff:" {
			_, _ = fmt.Sscanf(line[7:], "%x", &capEff)
			return capEff&capNetAdmin != 0
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func (a *NetlinkApplier) Apply(p Pin) error {
	if p.Interface == "lo" {
		return fmt.Errorf("%w: refusing loopback interface for live pins", ErrRefused)
	}
	link, err := netlink.LinkByName(p.Interface)
	if err != nil {
		return fmt.Errorf("%w: interface %q: %v", ErrRefused, p.Interface, err)
	}
	attrs := link.Attrs()
	if attrs.Flags&net.FlagUp == 0 {
		return fmt.Errorf("%w: interface %q is down", ErrRefused, p.Interface)
	}
	gw := gatewayForLink(attrs.Index)
	plan, err := BuildPlan(p, attrs.Index, gw)
	if err != nil {
		return err
	}
	if !OwnedTable(plan.Table) {
		return fmt.Errorf("%w: refused non-owned table %d", ErrRefused, plan.Table)
	}

	dst := &net.IPNet{IP: plan.Dst, Mask: net.CIDRMask(32, 32)}
	route := &netlink.Route{
		LinkIndex: plan.IfIndex,
		Dst:       dst,
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
		return fmt.Errorf("pathpin: route replace: %w", err)
	}

	rule := netlink.NewRule()
	rule.Family = netlink.FAMILY_V4
	rule.Table = plan.Table
	rule.Priority = plan.Priority
	rule.Dst = dst
	// Idempotent: delete matching owned rule then add.
	_ = deleteOwnedRulesTo(plan.Dst)
	if err := netlink.RuleAdd(rule); err != nil {
		_ = netlink.RouteDel(route)
		if isPerm(err) {
			return FormatPrivError(err)
		}
		return fmt.Errorf("pathpin: rule add: %w", err)
	}
	return nil
}

func (a *NetlinkApplier) Remove(p Pin) error {
	if p.Dst == nil || p.Dst.To4() == nil {
		return fmt.Errorf("%w: IPv4 required", ErrRefused)
	}
	ip := p.Dst.To4()
	if err := deleteOwnedRulesTo(ip); err != nil {
		return err
	}
	table := tableForIface(p.Interface)
	if !OwnedTable(table) {
		return nil
	}
	route := &netlink.Route{
		Dst:   &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)},
		Table: table,
	}
	if err := netlink.RouteDel(route); err != nil && !isNotExists(err) {
		if isPerm(err) {
			return FormatPrivError(err)
		}
		// Best-effort cleanup: missing route is fine.
		return nil
	}
	return nil
}

func isNotExists(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) || err == unix.ESRCH {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "no such process") || strings.Contains(msg, "not found")
}

func deleteOwnedRulesTo(dst net.IP) error {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		if isPerm(err) {
			return FormatPrivError(err)
		}
		return err
	}
	for i := range rules {
		r := rules[i]
		if r.Priority != RulePref {
			continue
		}
		if !OwnedTable(r.Table) {
			continue
		}
		if r.Dst == nil || r.Dst.IP == nil {
			continue
		}
		if r.Dst.IP.Equal(dst) {
			_ = netlink.RuleDel(&r)
		}
	}
	return nil
}

func gatewayForLink(ifindex int) net.IP {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil
	}
	for _, r := range routes {
		if r.LinkIndex != ifindex {
			continue
		}
		if r.Dst == nil && r.Gw != nil { // default via this iface
			return r.Gw
		}
	}
	return nil
}

func isPerm(err error) bool {
	if err == nil {
		return false
	}
	if err == unix.EPERM || err == unix.EACCES || os.IsPermission(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied")
}
