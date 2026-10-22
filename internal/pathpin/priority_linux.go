//go:build linux

package pathpin

import "github.com/vishvananda/netlink"

func readSnaps() ([]RuleSnap, error) {
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	out := make([]RuleSnap, 0, len(rules))
	for _, r := range rules {
		out = append(out, snapFromRule(r))
	}
	return out, nil
}

func snapFromRule(r netlink.Rule) RuleSnap {
	s := RuleSnap{
		Priority: r.Priority,
		Table:    r.Table,
		Mark:     r.Mark,
		Invert:   r.Invert,
		HasDev:   r.IifName != "" || r.OifName != "",
	}
	if r.Dst != nil && r.Dst.IP != nil {
		s.HasDst = true
		ones, _ := r.Dst.Mask.Size()
		s.DstBits = ones
	}
	if r.Src != nil && r.Src.IP != nil {
		ones, _ := r.Src.Mask.Size()
		if ones > 0 {
			s.HasSrc = true
		}
	}
	return s
}

// DeferForeignCatchAlls moves non-WARP catch-all rules that sit on or ahead of
// the default path to just behind it. The VPN keeps the rule, so traffic pinned
// to that interface still uses it; everything else is already claimed by a pin
// or by the default path.
func DeferForeignCatchAlls(band PriorityBand) ([]ForeignMove, error) {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	snaps := make([]RuleSnap, len(rules))
	for i := range rules {
		snaps[i] = snapFromRule(rules[i])
	}
	moves := ForeignMoves(snaps, band)
	if len(moves) == 0 {
		return nil, nil
	}
	used := make([]bool, len(rules))
	moved := make([]ForeignMove, 0, len(moves))
	for _, mv := range moves {
		idx := -1
		for i := range rules {
			if used[i] {
				continue
			}
			r := rules[i]
			if r.Priority == mv.From && r.Table == mv.Table && r.Mark == mv.Mark && r.Invert == mv.Invert {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		used[idx] = true
		updated := rules[idx]
		updated.Priority = mv.To
		updated.Family = netlink.FAMILY_V4
		if err := netlink.RuleAdd(&updated); err != nil && !isExists(err) {
			return moved, err
		}
		old := rules[idx]
		old.Family = netlink.FAMILY_V4
		if err := netlink.RuleDel(&old); err != nil && !isNotExists(err) {
			return moved, err
		}
		moved = append(moved, mv)
	}
	return moved, nil
}

// CurrentBand reads the live IPv4 policy rules and returns the band to install.
// On any read error it returns the static band.
func CurrentBand() PriorityBand {
	snaps, err := readSnaps()
	if err != nil {
		return StaticBand()
	}
	return BandFrom(snaps)
}

// PolicyDrift reads live rules. defaultDrift / pinDrift are true when genesis
// rules are not on the band CurrentBand would install.
func PolicyDrift() (PriorityBand, bool, bool, error) {
	snaps, err := readSnaps()
	if err != nil {
		return StaticBand(), false, false, err
	}
	band, defDrift, pinDrift := driftFrom(snaps)
	return band, defDrift, pinDrift, nil
}
