package pathpin

import (
	"context"
	"log"
	"sync"
	"time"
)

// WatchPolicy re-applies pins and the default path when the installed
// priorities drift from CurrentBand. Foreign catch-alls, including WARP,
// are moved behind the default path. The WARP address itself keeps WARP's
// table, and the default interface is accepted in WARP's filter.
//
// realign may be nil when no default path is installed. It should re-apply
// the catch-all (and preserve rules) for the current interface.
func WatchPolicy(ctx context.Context, pins *Manager, realign func() error, every time.Duration) {
	if every <= 0 {
		every = 2 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			realignPolicy(pins, realign)
		}
	}
}

func realignPolicy(pins *Manager, realign func() error) {
	policyMu.Lock()
	defer policyMu.Unlock()
	band, defDrift, pinDrift, err := PolicyDrift()
	if err != nil {
		return
	}
	if defDrift || pinDrift {
		realignBand(pins, realign, band, defDrift, pinDrift)
	}
	deferForeign(band)
	if err := EnsureWarpOrigin(); err != nil {
		log.Printf("pathpin: warp source rule: %v", err)
	}
	if iface := currentDefaultIface(); iface != "" {
		if err := EnsureDefaultEgress(iface); err != nil {
			log.Printf("pathpin: default iface through WARP filter: %v", err)
		}
	}
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

func currentDefaultIface() string {
	defaultIfaceMu.Lock()
	defer defaultIfaceMu.Unlock()
	return defaultIfaceLive
}

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
