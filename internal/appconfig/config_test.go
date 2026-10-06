package appconfig

import (
	"path/filepath"
	"testing"

	"dnsredirector/internal/rules"
)

func TestParseRoundTrip(t *testing.T) {
	const raw = `
version: 1
agent:
  listen: 127.0.0.1:5553
  pins: dry-run
  default_path_mode: off
  dns_upstream: 1.1.1.1
defaults:
  dns: 1.1.1.1
  interface: auto
  pin_connections: true
  on_iface_down: fail_closed
default_path:
  interface: eno1
rules:
  - name: discord
    match:
      domains: ["discord.com", "*.discord.com"]
    dns: 1.1.1.1
    interface: CloudflareWARP
`
	f, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if f.EffectiveListen() != "127.0.0.1:5553" {
		t.Fatalf("listen %q", f.EffectiveListen())
	}
	if !f.RulesConfig().DefaultPathEnabled() {
		t.Fatal("default_path expected")
	}
	if f.RulesConfig().MatchDomain("a.discord.com") == nil {
		t.Fatal("domain match")
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.yaml")
	f := Default()
	f.DefaultPath.Interface = "eno1"
	pin := true
	f.Rules = []rules.Rule{{
		Name: "lab",
		Match: rules.Match{
			Domains: []string{"example.test"},
		},
		DNS:            "1.1.1.1",
		Interface:      "eno1",
		PinConnections: &pin,
	}}
	if err := SaveFile(path, f); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultPath.Interface != "eno1" || len(got.Rules) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestListenEnvOverride(t *testing.T) {
	f := Default()
	t.Setenv(EnvListen, "127.0.0.1:9999")
	if f.EffectiveListen() != "127.0.0.1:9999" {
		t.Fatal(f.EffectiveListen())
	}
}

func TestFindExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.yaml")
	if err := SaveFile(path, Default()); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfig, path)
	if got := FindExisting(); got != path {
		t.Fatalf("got %q want %q", got, path)
	}
}
