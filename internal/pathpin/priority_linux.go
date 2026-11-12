//go:build linux

package pathpin

import (
	"log"
	"net"

	"github.com/vishvananda/netlink"
)

const warpLinkName = "CloudflareWARP"

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

// DeferForeignCatchAlls moves catch-all rules, including WARP's fwmark rule,
// that sit on or ahead of the default path to just behind it. The VPN keeps
// the rule, so traffic pinned to that interface still uses it; everything else
// is already claimed by a pin or by the default path.
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

// EnsureWarpOrigin keeps traffic sourced from the WARP interface address in
// WARP's table. That is the client's in-tunnel DNS. The default path would
// otherwise send it out the physical NIC, and the lookup times out.
// No WARP catch-all means any such rule is removed.
func EnsureWarpOrigin() error {
	kernelMu.Lock()
	defer kernelMu.Unlock()
	rules, err := netlink.RuleList(netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	table := warpTableFromRules(rules)
	want := map[string]net.IP{}
	if table != 0 {
		for _, ip := range warpV4Addrs() {
			want[ip.String()] = ip
		}
	}
	seen := map[string]bool{}
	for _, r := range rules {
		if !ownsWarpSource(r) {
			continue
		}
		key := r.Src.IP.String()
		if _, ok := want[key]; ok && r.Table == table {
			seen[key] = true
			continue
		}
		r.Family = netlink.FAMILY_V4
		if err := netlink.RuleDel(&r); err != nil && !isNotExists(err) {
			return err
		}
		log.Printf("pathpin: removed warp source %s lookup %d", key, r.Table)
	}
	for key, ip := range want {
		if seen[key] {
			continue
		}
		rule := netlink.NewRule()
		rule.Family = netlink.FAMILY_V4
		rule.Priority = WarpSourcePref
		rule.Table = table
		rule.Src = &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)}
		if err := netlink.RuleAdd(rule); err != nil && !isExists(err) {
			return err
		}
		log.Printf("pathpin: warp source %s lookup %d at %d", ip, table, WarpSourcePref)
	}
	return nil
}

func warpTableFromRules(rules []netlink.Rule) int {
	table := 0
	prio := 0
	for _, r := range rules {
		if !r.Invert || r.Mark != WarpExemptMark || r.Table == 0 || r.Table == DefaultPathTable {
			continue
		}
		if table == 0 || r.Priority < prio {
			table = r.Table
			prio = r.Priority
		}
	}
	return table
}

func warpV4Addrs() []net.IP {
	link, err := netlink.LinkByName(warpLinkName)
	if err != nil {
		return nil
	}
	addrs, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return nil
	}
	out := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ip := a.IP.To4()
		if ip == nil {
			continue
		}
		out = append(out, ip)
	}
	return out
}

func ownsWarpSource(r netlink.Rule) bool {
	return r.Priority == WarpSourcePref && r.Src != nil && r.Src.IP != nil && r.Dst == nil && r.Mark == 0 && !r.Invert
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
