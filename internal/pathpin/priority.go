package pathpin

import "sort"

// Static priorities are the pin / preserve / catch-all band. They do not move.
// WARP's Linux client installs "not fwmark 0x100cf lookup <warp-table>" with
// no priority, and the kernel parks that rule in front of whatever else exists.
// That catch-all is a foreign rule: ForeignMoves pushes it behind the default
// path, same as any other VPN. Per-destination pins stay at RulePref so a
// chosen address can still leave via CloudflareWARP. WARP's filter drops
// packets that are not on its tunnel; the fwmark only skips its routing rule.
// The agent accepts the default interface in that filter. Packets sourced
// from the WARP address stay on WARP's table (priority 5099) so its own
// in-tunnel DNS is not sent out the default NIC.
const (
	// RulePref is the static per-destination pin priority.
	RulePref = 5000
	// MainPreservePref is the static "lookup main" priority for connected prefixes.
	MainPreservePref = 5050
	// DefaultPathPref is the static catch-all priority.
	DefaultPathPref = 5100
	// WarpSourcePref sends packets that originate on the WARP address into
	// WARP's table. They must be evaluated before the default path, which
	// would otherwise send that address out the physical NIC.
	WarpSourcePref = 5099

	// ruleTableMain is the IPv4 main table id (RT_TABLE_MAIN).
	ruleTableMain = 254
	// rulePrioMain is the kernel's built-in lookup-main rule. Never use it.
	rulePrioMain = 32766
)

// PriorityBand is the pin / preserve / default-path priority triple to install.
type PriorityBand struct {
	Pins        int
	Preserve    int
	DefaultPath int
	// Warp is the live WARP catch-all priority, or 0 when no such rule exists.
	Warp int
}

// StaticBand is the band used when WARP is not ahead of us.
func StaticBand() PriorityBand {
	return PriorityBand{
		Pins:        RulePref,
		Preserve:    MainPreservePref,
		DefaultPath: DefaultPathPref,
	}
}

// RuleSnap is the subset of an IPv4 policy rule the band and foreign-rule
// decisions need.
type RuleSnap struct {
	Priority int
	Table    int
	Mark     uint32
	Invert   bool
	HasDst   bool
	// DstBits is the destination prefix length when HasDst is set.
	DstBits int
	HasSrc  bool
	HasDev  bool
}

// ForeignMove is a catch-all that would be evaluated with, or before, the
// default path. To sits just after the default path so that catch-all only
// sees traffic genesis did not already pin. WARP's fwmark catch-all is included.
type ForeignMove struct {
	From, To int
	Table    int
	Mark     uint32
	Invert   bool
}

// BandFrom chooses priorities from a rule snapshot.
// WARP is "not fwmark WarpExemptMark" into a table that is not our catch-all.
// Our own catch-all uses the same mark so the tunnel stays exempt; it is ignored
// when recording Warp. Priorities stay on the static band either way: a WARP
// catch-all that sits on or ahead of the default path is moved behind it.
func BandFrom(snaps []RuleSnap) PriorityBand {
	b := StaticBand()
	b.Warp = warpPriority(snaps)
	return b
}

// warpPriority is the lowest live WARP catch-all priority, or 0 when none exists.
func warpPriority(snaps []RuleSnap) int {
	warp := 0
	for _, s := range snaps {
		if !s.Invert || s.Mark != WarpExemptMark || s.Priority <= 0 {
			continue
		}
		if s.Table == 0 || s.Table == DefaultPathTable {
			continue
		}
		if warp == 0 || s.Priority < warp {
			warp = s.Priority
		}
	}
	return warp
}

// driftFrom reports whether installed genesis rules are not on band.
// defaultDrift covers the catch-all and LAN preserve rules.
// pinDrift covers per-destination rules in owned tables.
func driftFrom(snaps []RuleSnap) (band PriorityBand, defaultDrift, pinDrift bool) {
	band = BandFrom(snaps)
	defSeen := false
	defPrio := 0
	for _, s := range snaps {
		if s.Table == DefaultPathTable {
			defSeen = true
			defPrio = s.Priority
		}
		if OwnedTable(s.Table) && s.Table != DefaultPathTable && s.HasDst && s.Priority != band.Pins {
			pinDrift = true
		}
	}
	if !defSeen {
		return band, false, pinDrift
	}
	if defPrio != band.DefaultPath {
		defaultDrift = true
	}
	for _, s := range snaps {
		if s.Table == ruleTableMain && s.HasDst && s.Mark == 0 && !s.Invert &&
			s.Priority > 0 && s.Priority < 32766 && s.Priority != band.Preserve {
			defaultDrift = true
			break
		}
	}
	return band, defaultDrift, pinDrift
}

// foreignCatchAll reports a policy rule that sends traffic into some other
// interface's table ahead of the default path. WARP's "not fwmark" catch-all
// is included: it must not stay in front of genesis. Marked-only rules (a
// tunnel's own packets), specific destinations, and the built-in tables are
// left alone.
func foreignCatchAll(s RuleSnap) bool {
	if s.Priority <= 0 || s.Priority >= rulePrioMain {
		return false
	}
	if s.Table == 0 || s.Table == ruleTableMain || s.Table == 253 || s.Table == 255 {
		return false
	}
	if OwnedTable(s.Table) {
		return false
	}
	if s.Mark != 0 && !s.Invert {
		return false
	}
	if s.HasSrc || s.HasDev {
		return false
	}
	if s.HasDst && s.DstBits > 1 {
		return false
	}
	return true
}

// ForeignMoves lists catch-alls that sit on or ahead of the default path and
// the priority they should be given so they stay behind it. Relative order of
// those rules is preserved.
func ForeignMoves(snaps []RuleSnap, band PriorityBand) []ForeignMove {
	ahead := make([]RuleSnap, 0)
	for _, s := range snaps {
		if !foreignCatchAll(s) || s.Priority > band.DefaultPath {
			continue
		}
		ahead = append(ahead, s)
	}
	sort.Slice(ahead, func(i, j int) bool {
		if ahead[i].Priority != ahead[j].Priority {
			return ahead[i].Priority < ahead[j].Priority
		}
		return ahead[i].Table < ahead[j].Table
	})
	out := make([]ForeignMove, 0, len(ahead))
	slot := band.DefaultPath + 1
	for _, s := range ahead {
		if slot >= rulePrioMain {
			break
		}
		if s.Priority != slot {
			out = append(out, ForeignMove{
				From: s.Priority, To: slot, Table: s.Table, Mark: s.Mark, Invert: s.Invert,
			})
		}
		slot++
	}
	return out
}
