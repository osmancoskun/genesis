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
	// EnvUI overrides agent.ui_listen.
	EnvUI = "GENESIS_UI"
	// DefaultUI is the local Web UI (TCP); DNS stays on DefaultListen (UDP).
	DefaultUI = "127.0.0.1:8787"
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
	UIListen        string `yaml:"ui_listen"`         // empty = disabled; default in UI helper
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

// EffectiveUIListen returns the Web UI bind address, or "" if disabled.
// GENESIS_UI overrides. Explicit agent.ui_listen: "off" disables.
func (f *File) EffectiveUIListen() string {
	if v := strings.TrimSpace(os.Getenv(EnvUI)); v != "" {
		if v == "off" || v == "disabled" {
			return ""
		}
		return v
	}
	u := strings.TrimSpace(f.Agent.UIListen)
	if u == "off" || u == "disabled" {
		return ""
	}
	if u == "" {
		return DefaultUI
	}
	return u
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

// ConfigEntry is a discoverable config file for the Web UI / ctl.
type ConfigEntry struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

// ConfigsDir is the repo-local directory for genesis agent configs (not examples/).
const ConfigsDir = "configs"

// UserConfigDirName is under $HOME/.config.
const UserConfigDirName = "genesis"

// isListedConfigFile reports whether name belongs in the Web UI / ctl config list.
// Excludes *.rules.yaml (examples / rules-only packs live under examples/).
func isListedConfigFile(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(low, ".yaml") && !strings.HasSuffix(low, ".yml") {
		return false
	}
	if strings.Contains(low, ".rules.") {
		return false
	}
	return true
}

// UserConfigDir returns ~/.config/genesis (may not exist yet).
func UserConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", UserConfigDirName)
}

// ListConfigFiles returns genesis YAML configs under configs/ and ~/.config/genesis/,
// plus the active path if elsewhere. Skips *.rules.yaml.
func ListConfigFiles(active string) ([]ConfigEntry, error) {
	active = strings.TrimSpace(active)
	seen := map[string]bool{}
	var out []ConfigEntry

	add := func(path string) {
		path = filepath.Clean(path)
		if path == "" || path == "." || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, ConfigEntry{
			Path:   path,
			Name:   filepath.Base(path),
			Active: active != "" && filepath.Clean(active) == path,
		})
	}

	scanDir := func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !isListedConfigFile(name) {
				continue
			}
			add(filepath.Join(dir, name))
		}
		return nil
	}

	if err := scanDir(ConfigsDir); err != nil {
		return nil, err
	}
	if ud := UserConfigDir(); ud != "" {
		if err := scanDir(ud); err != nil {
			return nil, err
		}
	}
	if active != "" {
		add(active)
	}
	return out, nil
}

// AssertAllowedConfigPath rejects path traversal outside configs/ or ~/.config/genesis/.
func AssertAllowedConfigPath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return "", fmt.Errorf("empty config path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	configsAbs := filepath.Join(cwd, ConfigsDir)
	if isUnder(abs, configsAbs) {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		userDir := filepath.Join(home, ".config", "genesis")
		if isUnder(abs, userDir) {
			return path, nil
		}
	}
	return "", fmt.Errorf("config path not allowed: %s (must be under %s/ or ~/.config/genesis/)", path, ConfigsDir)
}

func isUnder(abs, root string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// NewConfigPath builds configs/<name>.yaml (adds .yaml if missing).
func NewConfigPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = filepath.Base(name)
	if name == "" || name == "." || name == ".." {
		return "", fmt.Errorf("invalid config name")
	}
	low := strings.ToLower(name)
	if !strings.HasSuffix(low, ".yaml") && !strings.HasSuffix(low, ".yml") {
		name += ".yaml"
	}
	path := filepath.Join(ConfigsDir, name)
	return AssertAllowedConfigPath(path)
}
