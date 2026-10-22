//go:build linux

package pathpin

import (
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// NetlinkApplier installs /32 destination pins via a dedicated policy-routing table.
// It never changes the main table default route, never toggles links, and only
// mutates tables in the owned range (see plan.go). Rule priority follows CurrentBand.
type NetlinkApplier struct{}

// kernelMu serializes rule/route edits so a pin apply and a WARP realign
// cannot delete each other's rules mid-update.
var kernelMu sync.Mutex

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
	kernelMu.Lock()
	defer kernelMu.Unlock()
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
	broadcast := attrs.Flags&net.FlagBroadcast != 0
	gw, err := gatewayForPin(attrs.Index, p.Dst, broadcast)
	if err != nil {
		return err
	}
	plan, err := BuildPlan(p, attrs.Index, gw)
	if err != nil {
		return err
	}
	plan.Priority = CurrentBand().Pins
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
	kernelMu.Lock()
	defer kernelMu.Unlock()
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
		if !OwnedTable(r.Table) || r.Table == DefaultPathTable {
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

// SweepPinPriorities deletes per-destination pin rules whose priority is not keep.
// The catch-all table is left alone. Called after a band change so orphaned
// pins from the previous priority cannot keep matching ahead of WARP or behind it.
func SweepPinPriorities(keep int) error {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		if isPerm(err) {
			return FormatPrivError(err)
		}
		return err
	}
	for i := range rules {
		r := rules[i]
		if !OwnedTable(r.Table) || r.Table == DefaultPathTable {
			continue
		}
		if r.Dst == nil || r.Dst.IP == nil || r.Priority == keep {
			continue
		}
		_ = netlink.RuleDel(&r)
	}
	return nil
}

func gatewayForLink(ifindex int) net.IP {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil
	}
	var fallback net.IP
	for _, r := range routes {
		if r.LinkIndex != ifindex || r.Gw == nil {
			continue
		}
		if isDefaultDst(r.Dst) {
			// Prefer main-table default (0 / 254); still accept any table.
			if r.Table == 0 || r.Table == unixTableMain {
				return r.Gw.To4()
			}
			if fallback == nil {
				fallback = r.Gw.To4()
			}
		}
	}
	return fallback
}

func gatewayForPin(ifindex int, dst net.IP, broadcast bool) (net.IP, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, fmt.Errorf("pathpin: list routes: %w", err)
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
	return pickGatewayForPin(ifindex, dst, hints, broadcast)
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
