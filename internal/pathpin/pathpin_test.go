package pathpin

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPinAAndExpire(t *testing.T) {
	rec := &RecordingApplier{}
	m := New(rec)
	fixed := time.Unix(1000, 0)
	m.now = func() time.Time { return fixed }

	ip := net.ParseIP("203.0.113.9")
	res, err := m.PinA(ip, "eno1", "corp", 30*time.Second)
	if err != nil || res.Refreshed {
		t.Fatalf("PinA: %#v %v", res, err)
	}
	if len(m.List()) != 1 || rec.Applies != 1 {
		t.Fatalf("want 1 pin/apply, pins=%d applies=%d", len(m.List()), rec.Applies)
	}

	m.now = func() time.Time { return fixed.Add(31 * time.Second) }
	if len(m.List()) != 0 {
		t.Fatalf("want expired, got %d", len(m.List()))
	}
	if rec.Removes != 1 {
		t.Fatalf("want 1 remove, got %d", rec.Removes)
	}
}

func TestPinRefreshExtendsTTL(t *testing.T) {
	rec := &RecordingApplier{}
	m := New(rec)
	fixed := time.Unix(1000, 0)
	m.now = func() time.Time { return fixed }

	ip := net.ParseIP("203.0.113.9")
	if _, err := m.PinA(ip, "eno1", "corp", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return fixed.Add(5 * time.Second) }
	res, err := m.PinA(ip, "eno1", "corp", 30*time.Second)
	if err != nil || !res.Refreshed {
		t.Fatalf("refresh: %#v %v", res, err)
	}
	if rec.Applies != 2 {
		t.Fatalf("want 2 applies, got %d", rec.Applies)
	}
	p, ok := m.Lookup(ip)
	if !ok {
		t.Fatal("missing pin")
	}
	wantExp := fixed.Add(5 * time.Second).Add(30 * time.Second)
	if !p.Expires.Equal(wantExp) {
		t.Fatalf("expires %v want %v", p.Expires, wantExp)
	}
	// Original 10s window would have ended at t=1010; refreshed should still be alive then.
	m.now = func() time.Time { return fixed.Add(15 * time.Second) }
	if _, ok := m.Lookup(ip); !ok {
		t.Fatal("pin should survive past original TTL after refresh")
	}
}

func TestReaperExpires(t *testing.T) {
	rec := &RecordingApplier{}
	m := New(rec)
	fixed := time.Unix(1000, 0)
	var mu sync.Mutex
	now := fixed
	m.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	ip := net.ParseIP("198.51.100.1")
	if _, err := m.PinA(ip, "eno1", "lab", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.RunReaper(ctx, 20*time.Millisecond)

	mu.Lock()
	now = fixed.Add(3 * time.Second)
	mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(m.List()) == 0 && rec.Removes >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("reaper did not expire pin; pins=%d removes=%d", len(m.List()), rec.Removes)
}

func TestSkipIPv6InV4First(t *testing.T) {
	m := New(nil)
	ip := net.ParseIP("2001:db8::1")
	if _, err := m.PinA(ip, "eno1", "corp", time.Minute); err == nil {
		t.Fatal("expected error for AAAA in v4-first MVP")
	}
}

func TestDryRunDescribe(t *testing.T) {
	var buf bytes.Buffer
	d := NewDryRun(&buf)
	m := New(d)
	ip := net.ParseIP("192.0.2.10")
	if _, err := m.PinA(ip, "eno1", "lab", time.Minute); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "dry-run APPLY:") || !strings.Contains(out, "192.0.2.10/32") {
		t.Fatalf("unexpected dry-run output: %s", out)
	}
}

func TestBuildPlanRefusesAuto(t *testing.T) {
	_, err := BuildPlan(Pin{Dst: net.ParseIP("192.0.2.1").To4(), Interface: "auto"}, 0, nil)
	if err == nil {
		t.Fatal("expected refuse")
	}
}

func TestSelectApplierDryRun(t *testing.T) {
	a, label, err := SelectApplier("dry-run")
	if err != nil || label != "dry-run" {
		t.Fatalf("got %v %q %v", a, label, err)
	}
}

func TestOwnedTable(t *testing.T) {
	if !OwnedTable(TableBase) || OwnedTable(254) || OwnedTable(TableBase+TableSpan) {
		t.Fatal("owned table bounds wrong")
	}
}

// RecordingApplier counts Apply/Remove for lifecycle tests.
type RecordingApplier struct {
	mu      sync.Mutex
	Applies int
	Removes int
}

func (r *RecordingApplier) Apply(Pin) error {
	r.mu.Lock()
	r.Applies++
	r.mu.Unlock()
	return nil
}

func (r *RecordingApplier) Remove(Pin) error {
	r.mu.Lock()
	r.Removes++
	r.mu.Unlock()
	return nil
}
