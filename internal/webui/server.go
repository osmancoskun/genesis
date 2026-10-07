package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/netinfo"
)

// Hooks lets the agent plug live state into the UI without importing cmd.
type Hooks struct {
	// ActivePath returns the currently loaded config path.
	ActivePath func() string
	// Snapshot returns the current on-disk/in-memory config.
	Snapshot func() *appconfig.File
	// SaveDefaultPath writes iface+mode to the config file and applies in-process.
	SaveDefaultPath func(iface, mode string) error
	// Apply reloads the active config from disk into the agent.
	Apply func() error
	// Select loads path and applies it.
	Select func(path string) error
	// SaveYAML writes raw YAML to the active path and applies.
	SaveYAML func(raw string) error
	// New creates a new config file from defaults (does not auto-select).
	New func(name string) (string, error)
	// Delete removes a config file (not the active one unless forced handled by agent).
	Delete func(path string) error
	// AddRule appends a pin rule, saves, and applies.
	AddRule func(in RuleInput) error
	// SaveSettings updates agent listen/pins/ui/upstream fields.
	SaveSettings func(listen, uiListen, pins, dpMode, dnsUpstream string) error
	DNSListen string
}

// Server is the local Web UI + JSON API (localhost TCP).
type Server struct {
	Addr  string
	Hooks Hooks

	mu   sync.Mutex
	http *http.Server
}

// ListenAndServe starts the UI (blocking).
func (s *Server) ListenAndServe() error {
	if s.Addr == "" {
		s.Addr = appconfig.DefaultUI
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/ifaces", s.handleIfaces)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/raw", s.handleConfigRaw)
	mux.HandleFunc("/api/configs", s.handleConfigs)
	mux.HandleFunc("/api/configs/select", s.handleConfigsSelect)
	mux.HandleFunc("/api/configs/apply", s.handleConfigsApply)
	mux.HandleFunc("/api/configs/save", s.handleConfigsSave)
	mux.HandleFunc("/api/configs/new", s.handleConfigsNew)
	mux.HandleFunc("/api/configs/delete", s.handleConfigsDelete)
	mux.HandleFunc("/api/configs/import", s.handleConfigsImport)
	mux.HandleFunc("/api/default-path", s.handleDefaultPath)
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/doctor", s.handleDoctor)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/probe", s.handleProbe)
	mux.HandleFunc("/api/rules/add", s.handleRulesAdd)
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(static)))

	s.http = &http.Server{Addr: s.Addr, Handler: localhostOnly(mux)}
	return s.http.ListenAndServe()
}

// Shutdown stops the UI server.
func (s *Server) Shutdown() error {
	if s.http == nil {
		return nil
	}
	return s.http.Close()
}

func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "localhost only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) activePath() string {
	if s.Hooks.ActivePath != nil {
		return s.Hooks.ActivePath()
	}
	return ""
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	f := s.hooksSnapshot()
	kernel := netinfo.DefaultRouteIface()
	cfgIface := ""
	mode := ""
	pins := ""
	ruleCount := 0
	if f != nil {
		cfgIface = f.DefaultPath.Interface
		mode = f.Agent.DefaultPathMode
		pins = f.Agent.Pins
		ruleCount = len(f.Rules)
	}
	setup := buildSetupStatus(f, kernel)
	writeJSON(w, map[string]any{
		"config_path":       s.activePath(),
		"dns_listen":        s.Hooks.DNSListen,
		"ui_listen":         s.Addr,
		"pins":              pins,
		"default_path_mode": mode,
		"kernel_iface":      kernel,
		"config_iface":      cfgIface,
		"rule_count":        ruleCount,
		"setup":             setup,
	})
}

func buildSetupStatus(f *appconfig.File, kernel string) map[string]any {
	reasons := []string{}
	missing := []string{}
	incomplete := false
	if f == nil {
		return map[string]any{
			"incomplete":     true,
			"reasons":        []string{"no_config"},
			"missing_ifaces": missing,
			"hint":           "No config loaded.",
		}
	}
	mode := strings.TrimSpace(f.Agent.DefaultPathMode)
	dp := strings.TrimSpace(f.DefaultPath.Interface)
	if len(f.Rules) == 0 && (mode == "" || mode == "off" || dp == "") {
		incomplete = true
		reasons = append(reasons, "blank_config")
	}
	checkIface := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || name == "auto" {
			return
		}
		if _, err := net.InterfaceByName(name); err != nil {
			missing = append(missing, name)
		}
	}
	checkIface(dp)
	for _, r := range f.Rules {
		checkIface(r.Interface)
	}
	if len(missing) > 0 {
		incomplete = true
		reasons = append(reasons, "missing_ifaces")
	}
	hint := ""
	switch {
	case len(missing) > 0:
		hint = "Configured interface(s) not found on this host — pick real ifaces in Default path / Rules, or import a blank config."
	case incomplete:
		hint = "First-boot: set a default path iface and/or add rules in the Web UI. Optional: import the Discord example and edit iface names."
	}
	_ = kernel
	return map[string]any{
		"incomplete":     incomplete,
		"reasons":        reasons,
		"missing_ifaces": missing,
		"hint":           hint,
	}
}

func (s *Server) handleIfaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	ifaces, err := netinfo.ListIfaces()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f := s.hooksSnapshot()
	cfg := ""
	if f != nil {
		cfg = f.DefaultPath.Interface
	}
	kernel := netinfo.DefaultRouteIface()
	type row struct {
		Name   string   `json:"name"`
		Up     bool     `json:"up"`
		Kernel bool     `json:"kernel"`
		Config bool     `json:"config"`
		IPv4   []string `json:"ipv4"`
		IPv6   []string `json:"ipv6"`
	}
	out := make([]row, 0, len(ifaces))
	for _, ifi := range ifaces {
		v4, v6 := splitGlobal(ifi.Addrs)
		out = append(out, row{
			Name:   ifi.Name,
			Up:     ifi.Up,
			Kernel: ifi.Name == kernel,
			Config: cfg != "" && ifi.Name == cfg,
			IPv4:   v4,
			IPv6:   v6,
		})
	}
	writeJSON(w, out)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	f := s.hooksSnapshot()
	if f == nil {
		http.Error(w, "no config", http.StatusNotFound)
		return
	}
	type rule struct {
		Name      string   `json:"name"`
		Interface string   `json:"interface"`
		DNS       string   `json:"dns"`
		Domains   []string `json:"domains"`
		IPs       []string `json:"ips"`
	}
	rules := make([]rule, 0, len(f.Rules))
	for _, r := range f.Rules {
		rules = append(rules, rule{
			Name: r.Name, Interface: r.Interface, DNS: r.DNS,
			Domains: r.Match.Domains, IPs: r.Match.IPs,
		})
	}
	writeJSON(w, map[string]any{
		"path":              s.activePath(),
		"listen":            f.EffectiveListen(),
		"ui_listen":         f.EffectiveUIListen(),
		"pins":              f.Agent.Pins,
		"default_path_mode": f.Agent.DefaultPathMode,
		"dns_upstream":      f.Agent.DNSUpstream,
		"default_path":      f.DefaultPath.Interface,
		"rules":             rules,
	})
}

func (s *Server) handleConfigRaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	path := s.activePath()
	if path == "" {
		http.Error(w, "no active config", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"path": path, "yaml": string(data)})
}

func (s *Server) handleConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	items, err := appconfig.ListConfigFiles(s.activePath())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	examples, err := appconfig.ListShareExamples()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"active":   s.activePath(),
		"items":    items,
		"examples": examples,
	})
}

func (s *Server) handleConfigsImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.Hooks.SaveYAML == nil {
		http.Error(w, "save not wired", http.StatusServiceUnavailable)
		return
	}
	src, err := appconfig.FindShareExample(body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(src)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Hooks.SaveYAML(string(data)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{
		"ok":     "true",
		"path":   s.activePath(),
		"source": src,
	})
}

func (s *Server) handleConfigsSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.Hooks.Select == nil {
		http.Error(w, "select not wired", http.StatusServiceUnavailable)
		return
	}
	if err := s.Hooks.Select(strings.TrimSpace(body.Path)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "path": s.activePath()})
}

func (s *Server) handleConfigsApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if s.Hooks.Apply == nil {
		http.Error(w, "apply not wired", http.StatusServiceUnavailable)
		return
	}
	if err := s.Hooks.Apply(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "path": s.activePath()})
}

func (s *Server) handleConfigsSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		YAML string `json:"yaml"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.Hooks.SaveYAML == nil {
		http.Error(w, "save not wired", http.StatusServiceUnavailable)
		return
	}
	if err := s.Hooks.SaveYAML(body.YAML); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "path": s.activePath()})
}

func (s *Server) handleConfigsNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.Hooks.New == nil {
		http.Error(w, "new not wired", http.StatusServiceUnavailable)
		return
	}
	path, err := s.Hooks.New(strings.TrimSpace(body.Name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "path": path})
}

func (s *Server) handleConfigsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.Hooks.Delete == nil {
		http.Error(w, "delete not wired", http.StatusServiceUnavailable)
		return
	}
	if err := s.Hooks.Delete(strings.TrimSpace(body.Path)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"ok": "true"})
}

func (s *Server) handleDefaultPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Interface string `json:"interface"`
		Mode      string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	body.Mode = strings.TrimSpace(body.Mode)
	if body.Mode == "" {
		body.Mode = "off"
	}
	switch body.Mode {
	case "auto", "dry-run", "netlink", "off":
	default:
		http.Error(w, "mode must be auto|dry-run|netlink|off", http.StatusBadRequest)
		return
	}
	if s.Hooks.SaveDefaultPath == nil {
		http.Error(w, "save not wired", http.StatusServiceUnavailable)
		return
	}
	if err := s.Hooks.SaveDefaultPath(strings.TrimSpace(body.Interface), body.Mode); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"ok": "true", "interface": body.Interface, "mode": body.Mode})
}

func (s *Server) hooksSnapshot() *appconfig.File {
	if s.Hooks.Snapshot == nil {
		return nil
	}
	return s.Hooks.Snapshot()
}

func splitGlobal(addrs []string) (v4, v6 []string) {
	for _, a := range addrs {
		ipStr := a
		if i := strings.IndexByte(a, '/'); i >= 0 {
			ipStr = a[:i]
		}
		ip := net.ParseIP(ipStr)
		if ip == nil || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, a)
		} else {
			v6 = append(v6, a)
		}
	}
	return v4, v6
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
	}
}