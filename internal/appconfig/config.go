// Package appconfig loads the local genesis YAML (agent + rules in one file).
package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"dnsredirector/internal/rules"
)

const (
	// EnvConfig is the path to the active config file.
	EnvConfig = "GENESIS_CONFIG"
	// EnvListen overrides agent.listen.
	EnvListen = "GENESIS_LISTEN"
	// DefaultListen avoids UDP 5353 (mDNS/Avahi on many Linux desktops).
	DefaultListen = "127.0.0.1:5553"
	// DefaultDNS is used for rule upstreams and setup probes when unset.
	DefaultDNS = "1.1.1.1"
)

// File is the on-disk local configuration.
type File struct {
	Version     int               `yaml:"version"`
	Agent       Agent             `yaml:"agent"`
	Defaults    rules.Defaults    `yaml:"defaults"`
	DefaultPath rules.DefaultPath `yaml:"default_path"`
	Rules       []rules.Rule      `yaml:"rules"`
}

// Agent holds daemon runtime options.
type Agent struct {
	Listen          string `yaml:"listen"`
	Pins            string `yaml:"pins"`              // auto|dry-run|netlink|noop
	DefaultPathMode string `yaml:"default_path_mode"` // auto|dry-run|netlink|off
	DNSUpstream     string `yaml:"dns_upstream"`      // default upstream for new rules / probes
}

// Default returns a starter config (no rules yet).
func Default() *File {
	return &File{
		Version: 1,
		Agent: Agent{
			Listen:          DefaultListen,
			Pins:            "auto",
			DefaultPathMode: "off",
			DNSUpstream:     DefaultDNS,
		},
		Defaults: rules.Defaults{
			DNS:            DefaultDNS,
			Interface:      "auto",
			PinConnections: true,
			OnIfaceDown:    rules.FailClosed,
		},
	}
}

// CandidatePaths lists where we look for an existing config (first hit wins).
func CandidatePaths() []string {
	var out []string
	if p := strings.TrimSpace(os.Getenv(EnvConfig)); p != "" {
		out = append(out, p)
	}
	out = append(out, "configs/local.yaml")
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "genesis", "config.yaml"))
	}
	return out
}

// FindExisting returns the first readable candidate path, or "".
// When GENESIS_CONFIG is set, only that path is considered.
func FindExisting() string {
	if p := strings.TrimSpace(os.Getenv(EnvConfig)); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		return ""
	}
	for _, p := range CandidatePaths() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// DefaultWritePath is where setup writes a new config.
func DefaultWritePath() string {
	if p := strings.TrimSpace(os.Getenv(EnvConfig)); p != "" {
		return p
	}
	return "configs/local.yaml"
}

// LoadFile reads and validates a config file.
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse unmarshals YAML bytes.
func Parse(data []byte) (*File, error) {
	f := Default()
	if err := yaml.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := f.Normalize(); err != nil {
		return nil, err
	}
	return f, nil
}

// Normalize fills defaults and validates via rules.Config.
func (f *File) Normalize() error {
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Version != 1 {
		return fmt.Errorf("unsupported config version %d (want 1)", f.Version)
	}
	if strings.TrimSpace(f.Agent.Listen) == "" {
		f.Agent.Listen = DefaultListen
	}
	if strings.TrimSpace(f.Agent.Pins) == "" {
		f.Agent.Pins = "auto"
	}
	if strings.TrimSpace(f.Agent.DefaultPathMode) == "" {
		f.Agent.DefaultPathMode = "off"
	}
	if strings.TrimSpace(f.Agent.DNSUpstream) == "" {
		f.Agent.DNSUpstream = DefaultDNS
	}
	if f.Defaults.DNS == "" {
		f.Defaults.DNS = f.Agent.DNSUpstream
	}
	rc := f.RulesConfig()
	if err := rc.Validate(); err != nil {
		return err
	}
	f.Defaults = rc.Defaults
	f.DefaultPath = rc.DefaultPath
	f.Rules = rc.Rules
	return nil
}

// RulesConfig projects the rules portion for the DNS stub / agent.
func (f *File) RulesConfig() *rules.Config {
	return &rules.Config{
		Version:     f.Version,
		Defaults:    f.Defaults,
		DefaultPath: f.DefaultPath,
		Rules:       f.Rules,
	}
}

// EffectiveListen applies GENESIS_LISTEN override.
func (f *File) EffectiveListen() string {
	if v := strings.TrimSpace(os.Getenv(EnvListen)); v != "" {
		return v
	}
	return f.Agent.Listen
}

// SaveFile writes YAML to path (creates parent dirs).
func SaveFile(path string, f *File) error {
	if err := f.Normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	header := "# genesis local config — generated/edited by ctl setup/menu\n"
	return os.WriteFile(path, append([]byte(header), data...), 0o644)
}
