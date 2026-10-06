package rules

import (
	"net"
	"testing"
)

func TestParseAndMatchDomain(t *testing.T) {
	const yaml = `
version: 1
defaults:
  dns: 9.9.9.9
  interface: auto
  pin_connections: true
  on_iface_down: fail_closed
rules:
  - name: corp
    match:
      domains: ["corp.example", "*.corp.example"]
    dns: 10.0.0.53
    interface: vpn0
  - name: printer
    match:
      ips: ["192.168.10.20/32"]
    interface: enp2
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.MatchDomain("a.corp.example."); got == nil || got.Name != "corp" {
		t.Fatalf("MatchDomain corp: got %#v", got)
	}
	if got := cfg.MatchDomain("other.example"); got != nil {
		t.Fatalf("unexpected match: %#v", got)
	}
	ip := net.ParseIP("192.168.10.20")
	if got := cfg.MatchIP(ip); got == nil || got.Name != "printer" {
		t.Fatalf("MatchIP: got %#v", got)
	}
	if cfg.Rules[0].OnIfaceDown != FailClosed {
		t.Fatalf("default on_iface_down: %q", cfg.Rules[0].OnIfaceDown)
	}
	if !cfg.Rules[0].EffectivePin() {
		t.Fatal("pin should default true")
	}
}

func TestFailClosedDefault(t *testing.T) {
	cfg, err := Parse([]byte(`
version: 1
defaults:
  dns: 1.1.1.1
rules:
  - name: x
    match:
      domains: ["x.test"]
    interface: vpn0
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.OnIfaceDown != FailClosed {
		t.Fatalf("want fail_closed, got %q", cfg.Defaults.OnIfaceDown)
	}
}
