package pathpin

import (
	"context"
	"log"
	"sync"
	"time"
)

// WatchPolicy re-applies pins and the default path when the installed
// priorities drift from CurrentBand. It does not move the catch-all in front
// of WARP: that client drops off-tunnel egress and the host loses IPv4.
//
// realign may be nil when no default path is installed. It should re-apply
// the catch-all (and preserve rules) for the current interface.
func WatchPolicy(ctx context.Context, pins *Manager, realign func() error, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	var warnedLow bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			realignPolicy(pins, realign, &warnedLow)
		}
	}
}

func realignPolicy(pins *Manager, realign func() error, warnedLow *bool) {
	policyMu.Lock()
	defer policyMu.Unlock()
	band, defDrift, pinDrift, err := PolicyDrift()
	if err != nil {
		return
	}
	if band.Warp > 0 && band.DefaultPath > band.Warp && warnedLow != nil && !*warnedLow {
		log.Printf("pathpin: WARP fwmark rule at %d stays ahead of default-path %d (off-tunnel egress is dropped)", band.Warp, band.DefaultPath)
		*warnedLow = true
	}
	if defDrift || pinDrift {
		realignBand(pins, realign, band, defDrift, pinDrift)
	}
	deferForeign(band)
}

func realignBand(pins *Manager, realign func() error, band PriorityBand, defDrift, pinDrift bool) {
	moved := false
	if defDrift && realign != nil {
		if err := realign(); err != nil {
			log.Printf("pathpin: realign default-path (warp %d): %v", band.Warp, err)
		} else {
			moved = true
		}
	}
	if pinDrift && pins != nil {
		if err := pins.Reapply(); err != nil {
			log.Printf("pathpin: realign pins (warp %d): %v", band.Warp, err)
		}
		if err := SweepPinPriorities(band.Pins); err != nil {
			log.Printf("pathpin: sweep stale pin priorities: %v", err)
		}
		moved = true
	}
	if moved {
		if band.Warp > 0 {
			log.Printf("pathpin: policy band pins %d, preserve %d, default %d (WARP %d)",
				band.Pins, band.Preserve, band.DefaultPath, band.Warp)
		} else {
			log.Printf("pathpin: policy band reset (pins %d, preserve %d, default %d)",
				band.Pins, band.Preserve, band.DefaultPath)
		}
	}
}

func deferForeign(band PriorityBand) {
	foreign, err := DeferForeignCatchAlls(band)
	if err != nil {
		log.Printf("pathpin: defer foreign catch-all: %v", err)
		return
	}
	for _, mv := range foreign {
		log.Printf("pathpin: foreign catch-all table %d moved %d -> %d (behind default-path %d)",
			mv.Table, mv.From, mv.To, band.DefaultPath)
	}
}

var (
	defaultIfaceMu   sync.Mutex
	defaultIfaceLive string
	// policyMu is held for the whole realign. HaltPolicy acquires it and does
	// not release, so a shutdown remove cannot race a realign that would put
	// the rules back.
	policyMu sync.Mutex
)

// HaltPolicy blocks until any in-flight realign finishes, then stops further ones.
// Call it before removing the default path during process shutdown.
func HaltPolicy() { policyMu.Lock() }

func rememberDefaultIface(iface string) {
	defaultIfaceMu.Lock()
	defaultIfaceLive = iface
	defaultIfaceMu.Unlock()
}

func clearDefaultIface(iface string) {
	defaultIfaceMu.Lock()
	if defaultIfaceLive == iface {
		defaultIfaceLive = ""
	}
	defaultIfaceMu.Unlock()
}

// RealignDefaultPath re-installs the catch-all on the interface last applied.
// It is a no-op until ApplyDefaultPath has succeeded at least once in this process.
func RealignDefaultPath() error {
	defaultIfaceMu.Lock()
	iface := defaultIfaceLive
	defaultIfaceMu.Unlock()
	if iface == "" {
		return nil
	}
	return NewNetlink().ApplyDefaultPath(iface)
}
