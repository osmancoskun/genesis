package pathpin

import "testing"

func TestBandStaysBehindWarpCatchAll(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 5049, Table: 65743, Mark: WarpExemptMark, Invert: true},
		{Priority: 5050, Table: ruleTableMain, HasDst: true},
		{Priority: 5100, Table: DefaultPathTable, Mark: WarpExemptMark, Invert: true},
	}
	b := BandFrom(snaps)
	if b.Warp != 5049 || b.Pins != RulePref || b.Preserve != MainPreservePref || b.DefaultPath != DefaultPathPref {
		t.Fatalf("band=%+v", b)
	}
	if !(b.DefaultPath > b.Warp && b.Preserve > b.Warp) {
		t.Fatalf("catch-all must be after WARP: %+v", b)
	}
}

func TestBandIgnoresOwnCatchAll(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 5100, Table: DefaultPathTable, Mark: WarpExemptMark, Invert: true},
		{Priority: 32766, Table: ruleTableMain},
	}
	b := BandFrom(snaps)
	if b.Warp != 0 || b.Pins != RulePref || b.Preserve != MainPreservePref || b.DefaultPath != DefaultPathPref {
		t.Fatalf("band=%+v", b)
	}
}

func TestBandParksCatchAllAfterHighWarp(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 5209, Table: 65743, Mark: WarpExemptMark, Invert: true},
	}
	b := BandFrom(snaps)
	if b.Warp != 5209 || b.Pins != RulePref || b.Preserve != 5210 || b.DefaultPath != 5211 {
		t.Fatalf("band=%+v", b)
	}
	if b.DefaultPath <= b.Warp || b.Preserve <= b.Warp {
		t.Fatalf("catch-all overtook WARP: %+v", b)
	}
}

func TestBandUsesLowestWarpRule(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 32765, Table: 65743, Mark: WarpExemptMark, Invert: true},
		{Priority: 5049, Table: 65743, Mark: WarpExemptMark, Invert: true},
	}
	b := BandFrom(snaps)
	if b.Warp != 5049 || b.DefaultPath != DefaultPathPref || b.DefaultPath <= b.Warp {
		t.Fatalf("band=%+v", b)
	}
}

func TestDriftPullsCatchAllBackBehindWarp(t *testing.T) {
	installed := []RuleSnap{
		{Priority: 5046, Table: 18001, HasDst: true},
		{Priority: 5047, Table: ruleTableMain, HasDst: true},
		{Priority: 5048, Table: DefaultPathTable, Mark: WarpExemptMark, Invert: true},
		{Priority: 5049, Table: 65743, Mark: WarpExemptMark, Invert: true},
	}
	band, defDrift, pinDrift := driftFrom(installed)
	if !defDrift || !pinDrift {
		t.Fatalf("5048 catch-all must move back, def=%v pin=%v band=%+v", defDrift, pinDrift, band)
	}
	if band.DefaultPath != DefaultPathPref || band.DefaultPath <= band.Warp || band.Pins != RulePref {
		t.Fatalf("band=%+v", band)
	}
}

func TestForeignCatchAllMovesBehindDefaultPath(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 4999, Table: 51820, Invert: true, Mark: 0xca6c},
		{Priority: 5049, Table: 65743, Mark: WarpExemptMark, Invert: true},
		{Priority: 5100, Table: DefaultPathTable, Mark: WarpExemptMark, Invert: true},
	}
	band := BandFrom(snaps)
	moves := ForeignMoves(snaps, band)
	if len(moves) != 1 || moves[0].From != 4999 || moves[0].To != 5101 || moves[0].Table != 51820 {
		t.Fatalf("moves=%+v band=%+v", moves, band)
	}
	if band.DefaultPath <= band.Warp {
		t.Fatalf("default path overtook WARP: %+v", band)
	}
}

func TestWarpAndMarkedRulesStay(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 100, Table: 52, Mark: 0x80000},
		{Priority: 200, Table: 9000, HasDst: true, DstBits: 24},
		{Priority: 5049, Table: 65743, Mark: WarpExemptMark, Invert: true},
		{Priority: 5200, Table: 52},
	}
	moves := ForeignMoves(snaps, StaticBand())
	if len(moves) != 0 {
		t.Fatalf("moves=%+v", moves)
	}
}

func TestTwoForeignCatchAllsKeepOrder(t *testing.T) {
	snaps := []RuleSnap{
		{Priority: 3000, Table: 200},
		{Priority: 4000, Table: 201, HasDst: true, DstBits: 1},
	}
	moves := ForeignMoves(snaps, StaticBand())
	if len(moves) != 2 || moves[0].To != 5101 || moves[1].To != 5102 || moves[0].Table != 200 || moves[1].Table != 201 {
		t.Fatalf("moves=%+v", moves)
	}
}
