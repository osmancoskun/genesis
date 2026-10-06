// Package rules defines the declarative path-director rule model and YAML loader.
package rules

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// OnIfaceDown controls behavior when a rule's interface is missing or down.
type OnIfaceDown string

const (
	// FailClosed returns DNS failure and installs no fallback pin (MVP default).
	FailClosed OnIfaceDown = "fail_closed"
	// FailOpen falls back to default DNS and default route (per-rule override later).
	FailOpen OnIfaceDown = "fail_open"
)

// Config is a loaded rule pack.
type Config struct {
	Version  int      `yaml:"version"`
	Defaults Defaults `yaml:"defaults"`
	Rules    []Rule   `yaml:"rules"`
}

// Defaults apply when a rule omits a field.
type Defaults struct {
	DNS            string      `yaml:"dns"`
	Interface      string      `yaml:"interface"` // "auto" means default-route iface
	PinConnections bool        `yaml:"pin_connections"`
	OnIfaceDown    OnIfaceDown `yaml:"on_iface_down"`
}

// Rule is one ordered match → DNS/iface/pin decision. First match wins.
type Rule struct {
	Name           string      `yaml:"name"`
	Match          Match       `yaml:"match"`
	DNS            string      `yaml:"dns"`
	Interface      string      `yaml:"interface"`
	PinConnections *bool       `yaml:"pin_connections"`
	OnIfaceDown    OnIfaceDown `yaml:"on_iface_down"`
	Action         string      `yaml:"action"` // empty = forward; later: nxdomain, …
}

// Match selects domains (suffix) and/or IP/CIDR destinations.
type Match struct {
	Domains []string `yaml:"domains"`
	IPs     []string `yaml:"ips"`
}

// LoadFile reads and validates a YAML rule pack.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse validates YAML bytes into a Config.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse rules: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks structural constraints.
func (c *Config) Validate() error {
	if c.Version != 0 && c.Version != 1 {
		return fmt.Errorf("unsupported rules version %d (want 1)", c.Version)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Defaults.OnIfaceDown == "" {
		c.Defaults.OnIfaceDown = FailClosed
	}
	if c.Defaults.OnIfaceDown != FailClosed && c.Defaults.OnIfaceDown != FailOpen {
		return fmt.Errorf("defaults.on_iface_down: want fail_closed or fail_open, got %q", c.Defaults.OnIfaceDown)
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		if r.Name == "" {
			return fmt.Errorf("rules[%d]: name is required", i)
		}
		if len(r.Match.Domains) == 0 && len(r.Match.IPs) == 0 {
			return fmt.Errorf("rule %q: match.domains or match.ips required", r.Name)
		}
		for _, d := range r.Match.Domains {
			if strings.TrimSpace(d) == "" {
				return fmt.Errorf("rule %q: empty domain", r.Name)
			}
		}
		for _, cidr := range r.Match.IPs {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				if ip := net.ParseIP(cidr); ip == nil {
					return fmt.Errorf("rule %q: invalid ip/cidr %q", r.Name, cidr)
				}
			}
		}
		if r.OnIfaceDown == "" {
			r.OnIfaceDown = c.Defaults.OnIfaceDown
		}
		if r.OnIfaceDown != FailClosed && r.OnIfaceDown != FailOpen {
			return fmt.Errorf("rule %q: on_iface_down invalid", r.Name)
		}
		if r.DNS == "" {
			r.DNS = c.Defaults.DNS
		}
		if r.Interface == "" {
			r.Interface = c.Defaults.Interface
		}
		if r.PinConnections == nil {
			v := c.Defaults.PinConnections
			r.PinConnections = &v
		}
	}
	return nil
}

// EffectivePin returns whether connection pinning is enabled for the rule.
func (r *Rule) EffectivePin() bool {
	if r.PinConnections == nil {
		return true
	}
	return *r.PinConnections
}

// MatchDomain returns the first rule whose domain suffix matches name (FQDN, trailing dot ok).
func (c *Config) MatchDomain(name string) *Rule {
	n := normalizeDomain(name)
	for i := range c.Rules {
		r := &c.Rules[i]
		for _, pattern := range r.Match.Domains {
			if domainMatches(n, normalizeDomain(pattern)) {
				return r
			}
		}
	}
	return nil
}

// MatchIP returns the first rule whose IP/CIDR contains ip.
func (c *Config) MatchIP(ip net.IP) *Rule {
	if ip == nil {
		return nil
	}
	for i := range c.Rules {
		r := &c.Rules[i]
		for _, cidr := range r.Match.IPs {
			if ipInSpec(ip, cidr) {
				return r
			}
		}
	}
	return nil
}

func normalizeDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, ".")
	s = strings.TrimPrefix(s, "*.")
	return s
}

// domainMatches implements suffix matching: pattern "q.com" matches "q.com" and "a.q.com".
func domainMatches(name, pattern string) bool {
	if name == pattern {
		return true
	}
	return strings.HasSuffix(name, "."+pattern)
}

func ipInSpec(ip net.IP, spec string) bool {
	if _, n, err := net.ParseCIDR(spec); err == nil {
		return n.Contains(ip)
	}
	parsed := net.ParseIP(spec)
	return parsed != nil && parsed.Equal(ip)
}
