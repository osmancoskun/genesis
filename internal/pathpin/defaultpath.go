package pathpin

import (
	"fmt"
	"net"
	"strings"
)

// Reserved owned table / priorities for the catch-all default path.
// Per-destination pins use RulePref and win first. All of these must stay
// numerically below Cloudflare WARP's typical catch-all (~5209).
const (
	// MainPreservePref steers connected/prefix destinations back to main before the catch-all.
	MainPreservePref = 5050
	// DefaultPathPref is the catch-all lookup (after pins + preserve).
	DefaultPathPref = 5100
	// DefaultPathTable holds 0.0.0.0/0 via the chosen iface (never the main table).
	DefaultPathTable = 18990
	// WarpExemptMark is Cloudflare WARP's fwmark (rule 5209: "not fwmark 0x100cf").
	// Our catch-all must use the same invert-mark so WARP tunnel/control packets
	// still fall through to main/WARP instead of being forced onto eno1 (which
	// leaves warp-cli stuck in Reconnecting).
	WarpExemptMark = 0x100cf
)

// DefaultPathPlan is the kernel intent for "default traffic via iface" (dry-run + netlink).
type DefaultPathPlan struct {
	Interface string
	IfIndex   int
	Table     int
	Priority  int
	Gateway   net.IP
	// Preserve lists main-table prefixes that should keep using main (LAN/on-link).
	Preserve []PreserveSpec
}

// PreserveSpec is "ip rule add to <dst> lookup main priority …".
type PreserveSpec struct {
	Dst      *net.IPNet
	Priority int
}

// BuildDefaultPathPlan validates iface and builds a plan. ifIndex/gw/preserve may be zero for textual dry-run.
func BuildDefaultPathPlan(iface string, ifIndex int, gw net.IP, preserve []PreserveSpec) (DefaultPathPlan, error) {
	name := strings.TrimSpace(iface)
	if name == "" || name == "auto" {
		return DefaultPathPlan{}, fmt.Errorf("%w: default path needs a real interface name (not %q)", ErrRefused, iface)
	}
	if !OwnedTable(DefaultPathTable) {
		return DefaultPathPlan{}, fmt.Errorf("%w: default path table %d outside owned range", ErrRefused, DefaultPathTable)
	}
	return DefaultPathPlan{
		Interface: name,
		IfIndex:   ifIndex,
		Table:     DefaultPathTable,
		Priority:  DefaultPathPref,
		Gateway:   gw,
		Preserve:  preserve,
	}, nil
}

// Describe returns non-executing planned ops.
func (p DefaultPathPlan) Describe() string {
	gw := "on-link"
	if p.Gateway != nil {
		gw = "via " + p.Gateway.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ip route replace default dev %s %s table %d", p.Interface, gw, p.Table)
	for _, pr := range p.Preserve {
		if pr.Dst == nil {
			continue
		}
		fmt.Fprintf(&b, "; ip rule add to %s lookup main priority %d", pr.Dst.String(), pr.Priority)
	}
	fmt.Fprintf(&b, "; ip rule add not from all fwmark 0x%x lookup %d priority %d  # default-path iface=%s (WARP-exempt)",
		WarpExemptMark, p.Table, p.Priority, p.Interface)
	return b.String()
}

// DefaultPathController installs/removes the catch-all default path.
type DefaultPathController interface {
	ApplyDefaultPath(iface string) error
	RemoveDefaultPath(iface string) error
}

// AsDefaultPath returns a controller if the applier supports default-path ops.
func AsDefaultPath(a Applier) (DefaultPathController, bool) {
	c, ok := a.(DefaultPathController)
	return c, ok
}
