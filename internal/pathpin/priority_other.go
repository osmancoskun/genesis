//go:build !linux

package pathpin

func readSnaps() ([]RuleSnap, error) { return nil, nil }

// CurrentBand returns the static band off Linux.
func CurrentBand() PriorityBand { return StaticBand() }

// PolicyDrift reports no drift off Linux.
func PolicyDrift() (PriorityBand, bool, bool, error) {
	return StaticBand(), false, false, nil
}

// DeferForeignCatchAlls is a no-op off Linux.
func DeferForeignCatchAlls(PriorityBand) ([]ForeignMove, error) { return nil, nil }
