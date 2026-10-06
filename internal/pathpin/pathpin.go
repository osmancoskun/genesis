// Package pathpin manages answer-driven (and static) destination→iface connection pins.
package pathpin

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Pin is one ephemeral destination policy route intent.
type Pin struct {
	Dst       net.IP
	Interface string
	Expires   time.Time
	RuleName  string
}

// Applier installs or removes pins in the kernel (policy routing).
type Applier interface {
	Apply(p Pin) error
	Remove(p Pin) error
}

// NoopApplier records nothing in the kernel (safe for unprivileged dry-runs).
type NoopApplier struct{}

func (NoopApplier) Apply(Pin) error  { return nil }
func (NoopApplier) Remove(Pin) error { return nil }

// MinTTL is the floor applied to DNS TTLs when installing pins.
const MinTTL = time.Second

// Manager tracks pins and delegates kernel changes to an Applier.
type Manager struct {
	mu      sync.Mutex
	pins    map[string]Pin
	applier Applier
	now     func() time.Time
}

// New returns a manager. If applier is nil, NoopApplier is used.
func New(applier Applier) *Manager {
	if applier == nil {
		applier = NoopApplier{}
	}
	return &Manager{
		pins:    make(map[string]Pin),
		applier: applier,
		now:     time.Now,
	}
}

// SetClock overrides the time source (tests / deterministic TTL).
func (m *Manager) SetClock(now func() time.Time) {
	if now == nil {
		m.now = time.Now
		return
	}
	m.now = now
}

// Result describes what PinA did.
type Result struct {
	Refreshed bool // true if an active pin for dst already existed
	Pin       Pin
}

// PinA installs or refreshes an IPv4 pin for at least ttl (v4-first MVP).
// A re-query for the same destination refreshes Expires to now+ttl and re-Applies.
func (m *Manager) PinA(dst net.IP, iface, ruleName string, ttl time.Duration) (Result, error) {
	var zero Result
	if dst == nil {
		return zero, fmt.Errorf("pathpin: nil destination")
	}
	if iface == "" || iface == "auto" {
		return zero, fmt.Errorf("pathpin: interface %q cannot be pinned", iface)
	}
	if ttl < MinTTL {
		ttl = MinTTL
	}
	ip4 := dst.To4()
	if ip4 == nil {
		return zero, fmt.Errorf("pathpin: v4-first MVP skips non-IPv4 %s", dst)
	}
	p := Pin{
		Dst:       ip4,
		Interface: iface,
		Expires:   m.now().Add(ttl),
		RuleName:  ruleName,
	}
	key := ip4.String()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	_, existed := m.pins[key]
	if err := m.applier.Apply(p); err != nil {
		return zero, err
	}
	m.pins[key] = p
	return Result{Refreshed: existed, Pin: p}, nil
}

// Lookup returns an active (non-expired) pin for dst.
func (m *Manager) Lookup(dst net.IP) (Pin, bool) {
	if dst == nil {
		return Pin{}, false
	}
	ip4 := dst.To4()
	if ip4 == nil {
		return Pin{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	p, ok := m.pins[ip4.String()]
	return p, ok
}

// List returns a snapshot of active (non-expired) pins after sweeping.
func (m *Manager) List() []Pin {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	res := make([]Pin, 0, len(m.pins))
	for _, p := range m.pins {
		res = append(res, p)
	}
	return res
}

// ExpireNow removes expired pins via the applier.
func (m *Manager) ExpireNow() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
}

// RunReaper periodically expires pins until ctx is done.
func (m *Manager) RunReaper(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.ExpireNow()
		}
	}
}

func (m *Manager) sweepLocked() {
	now := m.now()
	for key, p := range m.pins {
		if now.Before(p.Expires) {
			continue
		}
		_ = m.applier.Remove(p)
		delete(m.pins, key)
	}
}
