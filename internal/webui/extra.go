package webui

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/doctor"
	"dnsredirector/internal/netinfo"
	"dnsredirector/internal/rules"
	"dnsredirector/internal/svcstatus"
)

// RuleInput is a new pin rule from the Web UI.
type RuleInput struct {
	Name      string   `json:"name"`
	Interface string   `json:"interface"`
	DNS       string   `json:"dns"`
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	snap := svcstatus.Collect()
	f := s.hooksSnapshot()
	path := s.activePath()
	kernel := netinfo.DefaultRouteIface()
	rev := buildRev(path, f, snap, kernel)
	out := map[string]any{
		"ok":          true,
		"ts":          time.Now().UTC().Format(time.RFC3339),
		"rev":         rev,
		"agent":       snap,
		"dns_listen":  s.Hooks.DNSListen,
		"ui_listen":   s.Addr,
		"config_path": path,
		"kernel_iface": kernel,
		"commands":    actionCommands(path),
	}
	if f != nil {
		out["pins"] = f.Agent.Pins
		out["default_path_mode"] = f.Agent.DefaultPathMode
		out["default_path"] = f.DefaultPath.Interface
		out["dns_upstream"] = f.Agent.DNSUpstream
		out["rules_n"] = len(f.Rules)
	}
	writeJSON(w, out)
}

func buildRev(path string, f *appconfig.File, snap svcstatus.Snapshot, kernel string) string {
	var b strings.Builder
	b.WriteString(path)
	b.WriteByte('|')
	if path != "" {
		if st, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%d|%d|", st.ModTime().UnixNano(), st.Size())
		}
	}
	if f != nil {
		fmt.Fprintf(&b, "%s|%s|%s|%d|", f.DefaultPath.Interface, f.Agent.DefaultPathMode, f.Agent.Pins, len(f.Rules))
		for _, r := range f.Rules {
			fmt.Fprintf(&b, "%s>%s;", r.Name, r.Interface)
		}
	}
	fmt.Fprintf(&b, "%s|%t|%s|%t", kernel, snap.UnitActive, snap.UnitState, snap.HasNetAdmin)
	sum := sha1.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	rep := doctor.Run()
	writeJSON(w, rep)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		f := s.hooksSnapshot()
		if f == nil {
			http.Error(w, "no config", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{
			"path":              s.activePath(),
			"listen":            f.EffectiveListen(),
			"ui_listen":         f.EffectiveUIListen(),
			"pins":              f.Agent.Pins,
			"default_path_mode": f.Agent.DefaultPathMode,
			"dns_upstream":      f.Agent.DNSUpstream,
			"defaults_dns":      f.Defaults.DNS,
			"defaults_iface":    f.Defaults.Interface,
			"on_iface_down":     f.Defaults.OnIfaceDown,
			"default_path":      f.DefaultPath.Interface,
			"presets":           presets(),
			"commands":          actionCommands(s.activePath()),
			"ports": map[string]string{
				"dns_udp": s.Hooks.DNSListen,
				"ui_tcp":  s.Addr,
				"note":    "DNS is UDP; Web UI is TCP — same port number is OK across protocols",
			},
		})
	case http.MethodPost:
		if s.Hooks.SaveSettings == nil {
			http.Error(w, "settings not wired", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			Listen          string `json:"listen"`
			UIListen        string `json:"ui_listen"`
			Pins            string `json:"pins"`
			DefaultPathMode string `json:"default_path_mode"`
			DNSUpstream     string `json:"dns_upstream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if err := s.Hooks.SaveSettings(body.Listen, body.UIListen, body.Pins, body.DefaultPathMode, body.DNSUpstream); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"ok": "true"})
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Kind     string `json:"kind"`     // domain | ip
		Value    string `json:"value"`
		Iface    string `json:"iface"`    // optional; empty = all up ifaces
		Resolver string `json:"resolver"` // optional explicit override
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	body.Kind = strings.ToLower(strings.TrimSpace(body.Kind))
	body.Value = strings.TrimSpace(body.Value)
	if body.Value == "" {
		http.Error(w, "value required", http.StatusBadRequest)
		return
	}
	fallback := appconfig.DefaultDNS
	if f := s.hooksSnapshot(); f != nil && f.Agent.DNSUpstream != "" {
		fallback = f.Agent.DNSUpstream
	}
	explicit := strings.TrimSpace(body.Resolver)

	ifaces, err := netinfo.ListIfaces()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var names []string
	want := strings.TrimSpace(body.Iface)
	for _, ifi := range ifaces {
		if !ifi.Up || ifi.Name == "lo" {
			continue
		}
		if want != "" && ifi.Name != want {
			continue
		}
		names = append(names, ifi.Name)
	}
	if len(names) == 0 {
		http.Error(w, "no matching up interfaces", http.StatusBadRequest)
		return
	}

	// Parallel per-iface probes; each attempt is capped at PerAttemptTimeout.
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	type row struct {
		Iface    string   `json:"iface"`
		OK       bool     `json:"ok"`
		Detail   string   `json:"detail"`
		Answers  []string `json:"answers,omitempty"`
		Resolver string   `json:"resolver,omitempty"`
		Source   string   `json:"source,omitempty"` // link | fallback | explicit
	}
	out := make([]row, len(names))

	switch body.Kind {
	case "domain", "domains", "":
		probeName := strings.TrimPrefix(body.Value, "*.")
		var wg sync.WaitGroup
		for i, n := range names {
			wg.Add(1)
			go func(i int, n string) {
				defer wg.Done()
				res := netinfo.ProbeDomainSmart(ctx, n, explicit, fallback, probeName)
				out[i] = row{
					Iface: res.Iface, OK: res.OK, Detail: res.Detail, Answers: res.Answers,
					Resolver: res.Resolver, Source: res.Source,
				}
			}(i, n)
		}
		wg.Wait()
	case "ip", "ips":
		ip := net.ParseIP(body.Value)
		var probeIP net.IP
		if ip != nil {
			probeIP = ip.To4()
		} else if _, n, err := net.ParseCIDR(body.Value); err == nil {
			probeIP = n.IP.To4()
		}
		if probeIP == nil {
			http.Error(w, "need IPv4 or CIDR for ip probe", http.StatusBadRequest)
			return
		}
		var wg sync.WaitGroup
		for i, n := range names {
			wg.Add(1)
			go func(i int, n string) {
				defer wg.Done()
				res := netinfo.ProbeIP(ctx, n, probeIP)
				out[i] = row{Iface: res.Iface, OK: res.OK, Detail: res.Detail}
			}(i, n)
		}
		wg.Wait()
	default:
		http.Error(w, "kind must be domain|ip", http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"kind":     body.Kind,
		"value":    body.Value,
		"fallback": fallback,
		"explicit": explicit,
		"results":  out,
	})
}

func (s *Server) handleRulesAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body RuleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	body.Interface = strings.TrimSpace(body.Interface)
	body.Name = strings.TrimSpace(body.Name)
	if body.Interface == "" {
		http.Error(w, "interface required", http.StatusBadRequest)
		return
	}
	if len(body.Domains) == 0 && len(body.IPs) == 0 {
		http.Error(w, "domains or ips required", http.StatusBadRequest)
		return
	}
	if s.Hooks.AddRule == nil {
		http.Error(w, "add rule not wired", http.StatusServiceUnavailable)
		return
	}
	if err := s.Hooks.AddRule(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func actionCommands(configPath string) map[string]string {
	cfg := configPath
	if cfg == "" {
		cfg = "configs/local.yaml"
	}
	return map[string]string{
		"agent_fg":       "go run ./cmd/agent -config " + cfg,
		"agent_dry_run":  "go run ./cmd/agent -config " + cfg + " -pins dry-run -default-path dry-run",
		"agent_sudo":     "sudo go run ./cmd/agent -config " + cfg,
		"ctl_run":        "go run ./cmd/ctl -config " + cfg + " run",
		"ctl_menu":       "go run ./cmd/ctl -config " + cfg + " menu",
		"ctl_doctor":     "go run ./cmd/ctl doctor",
		"service_up":     "go run ./cmd/ctl up",
		"service_down":   "go run ./cmd/ctl down",
		"service_apply":  "go run ./cmd/ctl apply",
		"service_status": "go run ./cmd/ctl service-status",
		"install_unit":   "sudo make install ENABLE=1",
	}
}

func presets() []map[string]string {
	return []map[string]string{
		{"id": "discord", "label": "Discord via WARP", "config": "configs/discord.config.yaml"},
		{"id": "local", "label": "Local editable", "config": "configs/local.yaml"},
		{"id": "listen_5553", "label": "DNS UDP 127.0.0.1:5553", "listen": appconfig.DefaultListen},
		{"id": "ui_8787", "label": "UI TCP 127.0.0.1:8787", "ui": appconfig.DefaultUI},
		{"id": "pins_dry", "label": "Pins dry-run", "pins": "dry-run"},
		{"id": "pins_netlink", "label": "Pins netlink (needs root)", "pins": "netlink"},
	}
}

// RuleFromInput builds a rules.Rule from UI input.
func RuleFromInput(in RuleInput, f *appconfig.File) rules.Rule {
	return ruleFromInput(in, f)
}

func ruleFromInput(in RuleInput, f *appconfig.File) rules.Rule {
	name := in.Name
	if name == "" {
		if len(in.Domains) > 0 {
			name = sanitizeName(in.Domains[0])
		} else {
			name = "ip-" + sanitizeName(in.IPs[0])
		}
	}
	dns := in.DNS
	if dns == "" && f != nil {
		dns = f.Agent.DNSUpstream
	}
	if dns == "" {
		dns = appconfig.DefaultDNS
	}
	return rules.Rule{
		Name:      name,
		Interface: in.Interface,
		DNS:       dns,
		Match: rules.Match{
			Domains: in.Domains,
			IPs:     in.IPs,
		},
	}
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "*.")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "rule"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}
