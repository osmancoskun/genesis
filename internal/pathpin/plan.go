package pathpin

import (
	"fmt"
	"hash/fnv"
	"net"
	"strings"
)

// Owned routing-policy ranges. Only these tables/priorities are written or deleted.
const (
	TableBase = 18000
	TableSpan = 1000 // tables 18000–18999
	RulePref  = 18000
)

// Plan is the exact kernel intent for one pin (used by dry-run and netlink apply).
type Plan struct {
	Dst       net.IP
	Interface string
	IfIndex   int
	Table     int
	Priority  int
	Gateway   net.IP // may be nil for on-link / p2p
	RuleName  string
}

// BuildPlan computes table/priority for a pin without touching the kernel.
// iface must be a real interface name (not "auto"/empty); link lookup is optional
// when ifIndex/gateway are filled by the caller or left zero for dry textual plans.
func BuildPlan(p Pin, ifIndex int, gw net.IP) (Plan, error) {
	if p.Dst == nil || p.Dst.To4() == nil {
		return Plan{}, fmt.Errorf("%w: IPv4 destination required", ErrRefused)
	}
	name := strings.TrimSpace(p.Interface)
	if name == "" || name == "auto" {
		return Plan{}, fmt.Errorf("%w: interface %q cannot be pinned (name a real iface)", ErrRefused, p.Interface)
	}
	// Loopback is allowed in dry-run / memory paths for demos; NetlinkApplier refuses it.
	table := tableForIface(name)
	return Plan{
		Dst:       p.Dst.To4(),
		Interface: name,
		IfIndex:   ifIndex,
		Table:     table,
		Priority:  RulePref,
		Gateway:   gw,
		RuleName:  p.RuleName,
	}, nil
}

func tableForIface(name string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return TableBase + int(h.Sum32()%uint32(TableSpan))
}

// Describe returns a human-readable, non-executing summary of the plan.
func (p Plan) Describe() string {
	gw := "on-link"
	if p.Gateway != nil {
		gw = "via " + p.Gateway.String()
	}
	return fmt.Sprintf(
		"ip route replace %s/32 dev %s %s table %d; ip rule add to %s/32 lookup %d priority %d  # rule=%s",
		p.Dst, p.Interface, gw, p.Table, p.Dst, p.Table, p.Priority, p.RuleName,
	)
}

// OwnedTable reports whether table is in the agent's managed range.
func OwnedTable(table int) bool {
	return table >= TableBase && table < TableBase+TableSpan
}
