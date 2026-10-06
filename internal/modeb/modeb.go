// Package modeb syncs systemd-resolved split-DNS Domains= to the genesis agent.
package modeb

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/rules"
)

const (
	dropDir  = "/etc/systemd/resolved.conf.d"
	dropFile = "genesis-domains.conf"
)

// RoutingDomains extracts unique ~suffix values for resolved Domains= from rules.
// Patterns like "*.cdn.example" and "example.com" both become "~example.com".
func RoutingDomains(rs []rules.Rule) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, r := range rs {
		for _, d := range r.Match.Domains {
			base := strings.TrimSpace(strings.ToLower(d))
			base = strings.TrimPrefix(base, "*.")
			base = strings.TrimSuffix(base, ".")
			if base == "" || strings.Contains(base, "*") {
				continue
			}
			key := "~" + base
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// SyncFile writes/reloads the resolved drop-in so listed Domains hit listen (e.g. 127.0.0.1:5553).
// No-op (nil) when domains is empty: removes our drop-in if present.
func SyncFile(listen string, domains []string) error {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		listen = appconfig.DefaultListen
	}
	path := filepath.Join(dropDir, dropFile)
	if len(domains) == 0 {
		return removeDropIn(path)
	}
	if err := os.MkdirAll(dropDir, 0o755); err != nil {
		return fmt.Errorf("modeb: mkdir %s: %w (need root for Mode B)", dropDir, err)
	}
	body := fmt.Sprintf(
		"# genesis Mode B — managed; do not edit by hand\n"+
			"# Domains below are forwarded to the agent DNS stub.\n"+
			"[Resolve]\n"+
			"DNS=%s\n"+
			"Domains=%s\n",
		listen, strings.Join(domains, " "),
	)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("modeb: write %s: %w (need root for Mode B)", path, err)
	}
	if err := reloadResolved(); err != nil {
		return err
	}
	return nil
}

// SyncFromConfig updates Mode B from a loaded config file.
func SyncFromConfig(f *appconfig.File) error {
	if f == nil {
		return nil
	}
	return SyncFile(f.EffectiveListen(), RoutingDomains(f.Rules))
}

func removeDropIn(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("modeb: remove %s: %w", path, err)
	}
	return reloadResolved()
}

func reloadResolved() error {
	cmd := exec.Command("systemctl", "reload", "systemd-resolved")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("modeb: systemctl reload systemd-resolved: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
