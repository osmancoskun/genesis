package pathpin

import "sort"

// Static priorities are the normal pin / preserve / catch-all band.
// WARP's Linux client installs "not fwmark 0x100cf lookup <warp-table>" with
// no priority, and the kernel parks that rule in front of whatever else exists.
// It also drops packets that leave on any device other than CloudflareWARP.
// A catch-all placed before that rule therefore blackholes the host: every
// unmarked flow is forced onto eno1 and then rejected. BandFrom keeps the
// catch-all numerically after the live WARP rule. Per-destination pins stay
// at RulePref so they can still win.
const (
	// RulePref is the static per-destination pin priority.
	RulePref = 5000
	// MainPreservePref is the static "lookup main" priority for connected prefixes.
	MainPreservePref = 5050
	// DefaultPathPref is the static catch-all priority.
	DefaultPathPref = 5100

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

// ForeignMove is a non-WARP catch-all that would be evaluated with, or before,
// the default path. To sits just after the default path so that catch-all only
// sees traffic genesis did not already pin.
type ForeignMove struct {
	From, To int
	Table    int
	Mark     uint32
	Invert   bool
}

// BandFrom chooses priorities from a rule snapshot.
// WARP is "not fwmark WarpExemptMark" into a table that is not our catch-all.
// Our own catch-all uses the same mark so the tunnel stays exempt; it is ignored.
func BandFrom(snaps []RuleSnap) PriorityBand {
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
	b := StaticBand()
	b.Warp = warp
	if warp == 0 {
		return b
	}
	// Park preserve + catch-all just after WARP when the static numbers would
	// be evaluated first. Pins stay put: they match a single destination.
	if b.DefaultPath <= warp || b.Preserve <= warp {
		if warp+2 < rulePrioMain {
			b.Preserve = warp + 1
			b.DefaultPath = warp + 2
		}
	}
	return b
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

// foreignCatchAll reports a policy rule that sends unmarked traffic into some
// other interface's table. WARP's own fwmark rule is not one of these: that
// client drops off-tunnel egress, so it is left where it is. Marked-only rules
// (a tunnel's own packets), specific destinations, and the built-in tables are
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
	if s.Invert && s.Mark == WarpExemptMark {
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
