// Command agent runs the genesis client daemon (DNS stub + pin hooks).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/dnsstub"
	"dnsredirector/internal/modeb"
	"dnsredirector/internal/netinfo"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
	"dnsredirector/internal/webui"
)

func main() {
	configPath := flag.String("config", "", "path to local config YAML (agent+rules); preferred over -rules")
	rulesPath := flag.String("rules", "", "path to rules-only YAML (legacy; ignored if -config set)")
	listen := flag.String("listen", "", "UDP listen override (default from config or "+appconfig.DefaultListen+")")
	pinsMode := flag.String("pins", "", "pin backend override: auto|dry-run|netlink|noop")
	defaultPathMode := flag.String("default-path", "", "default-path override: auto|dry-run|netlink|off")
	reaperEvery := flag.Duration("pin-reaper", time.Second, "how often to expire TTL'd pins")
	pidFile := flag.String("pidfile", "", "write PID here (default: runtime dir genesis.pid)")
	uiListen := flag.String("ui", "", "Web UI listen (TCP). Default from config or "+appconfig.DefaultUI+"; off disables")
	flag.Parse()

	cfgFile, rulesCfg, loadedPath, err := loadAgentConfig(*configPath, *rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	listenAddr := cfgFile.EffectiveListen()
	if *listen != "" {
		listenAddr = *listen
	}
	pins := cfgFile.Agent.Pins
	if *pinsMode != "" {
		pins = *pinsMode
	}
	dpMode := cfgFile.Agent.DefaultPathMode
	if *defaultPathMode != "" {
		dpMode = *defaultPathMode
	}

	applier, label, err := pathpin.SelectApplier(pins)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pins: %v\n", err)
		os.Exit(1)
	}
	pinMgr := pathpin.New(applier)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pinMgr.RunReaper(ctx, *reaperEvery)

	var dpApplier pathpin.DefaultPathController
	dpLabel := "off"
	dpIface := ""
	if rulesCfg.DefaultPathEnabled() {
		var dpName string
		dpApplier, dpName, err = selectDefaultPath(dpMode, pins)
		if err != nil {
			// Keep the agent (and Web UI) up; operator can fix via UI / apply.
			log.Printf("default-path: applier unavailable (%v) — continuing degraded", err)
			dpLabel = "degraded"
		} else {
			dpLabel = dpName
			dpIface = resolveDefaultPathIface(rulesCfg.DefaultPath.Interface)
			if dpIface == "" {
				log.Printf("default-path: interface %q unresolved (no kernel default route) — continuing", rulesCfg.DefaultPath.Interface)
			} else if dpApplier != nil {
				if err := dpApplier.ApplyDefaultPath(dpIface); err != nil {
					log.Printf("default-path apply %s via %s: %v — continuing (fix via Web UI)", dpIface, dpName, err)
					dpIface = ""
				} else {
					log.Printf("default-path applied iface=%s mode=%s", dpIface, dpName)
				}
				defer func() {
					if dpApplier != nil && dpIface != "" {
						if err := dpApplier.RemoveDefaultPath(dpIface); err != nil {
							log.Printf("default-path remove: %v", err)
						}
					}
				}()
			}
		}
	} else if dpMode != "auto" && dpMode != "off" && dpMode != "" {
		log.Printf("default-path: rules have no default_path.interface; ignoring mode=%s", dpMode)
	}

	srv := &dnsstub.Server{
		Addr:   listenAddr,
		Rules:  rulesCfg,
		Pins:   pinMgr,
		Logger: log.Default(),
	}

	pf := *pidFile
	if pf == "" {
		pf = defaultPIDFile()
	}
	if err := writePID(pf); err != nil {
		log.Printf("pidfile %s: %v (continuing)", pf, err)
	} else {
		defer func() { _ = os.Remove(pf) }()
		log.Printf("pidfile %s", pf)
	}

	log.Printf("genesis agent: %s", dnsstub.FormatRuleSummary(rulesCfg))
	log.Printf("listen %s (Mode A coexistence; docs/resolved-coexistence.md)", listenAddr)
	log.Printf("pins=%s default-path=%s reaper=%s; iface-down=%s; ipv6=v4-first; doh=warn-only",
		label, dpLabel, *reaperEvery, rulesCfg.Defaults.OnIfaceDown)
	log.Printf("SIGHUP reloads config from %s (ctl apply)", loadedPath)
	if err := modeb.SyncFromConfig(cfgFile); err != nil {
		log.Printf("mode B Domains sync: %v", err)
	} else if doms := modeb.RoutingDomains(cfgFile.Rules); len(doms) > 0 {
		log.Printf("mode B Domains synced (%d): %s", len(doms), strings.Join(doms, " "))
	}

	state := &agentState{file: cfgFile, path: loadedPath, pins: pins, dpMode: dpMode}

	uiAddr := cfgFile.EffectiveUIListen()
	if *uiListen != "" {
		if *uiListen == "off" || *uiListen == "disabled" {
			uiAddr = ""
		} else {
			uiAddr = *uiListen
		}
	}
	var uiSrv *webui.Server
	if uiAddr != "" {
		applyLive := func() error {
			return state.applyLive(srv, &dpApplier, &dpIface)
		}
		uiSrv = &webui.Server{
			Addr: uiAddr,
			Hooks: webui.Hooks{
				DNSListen: listenAddr,
				ActivePath: func() string {
					return state.pathLocked()
				},
				Snapshot: state.get,
				SaveDefaultPath: func(iface, mode string) error {
					return state.saveDefaultPath(iface, mode, srv, &dpApplier, &dpIface)
				},
				Apply: applyLive,
				Select: func(path string) error {
					return state.selectConfig(path, srv, &dpApplier, &dpIface)
				},
				SaveYAML: func(raw string) error {
					return state.saveYAML(raw, srv, &dpApplier, &dpIface)
				},
				New: func(name string) (string, error) {
					return state.newConfig(name)
				},
				Delete: func(path string) error {
					return state.deleteConfig(path)
				},
				AddRule: func(in webui.RuleInput) error {
					return state.addRule(in, srv, pinMgr, &dpApplier, &dpIface)
				},
				SaveSettings: func(listen, uiListen, pinsMode, dpModeVal, dnsUpstream string) error {
					return state.saveSettings(listen, uiListen, pinsMode, dpModeVal, dnsUpstream, srv, &dpApplier, &dpIface)
				},
			},
		}
		go func() {
			log.Printf("web UI http://%s (localhost only)", uiAddr)
			if err := uiSrv.ListenAndServe(); err != nil {
				log.Printf("web UI: %v", err)
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case err := <-errCh:
			if uiSrv != nil {
				_ = uiSrv.Shutdown()
			}
			if err != nil {
				log.Fatalf("dns stub: %v", err)
			}
			return
		case s := <-sig:
			if s == syscall.SIGHUP {
				log.Printf("SIGHUP: reloading %s", state.pathLocked())
				if err := state.applyLive(srv, &dpApplier, &dpIface); err != nil {
					log.Printf("reload failed: %v (keeping previous rules)", err)
					continue
				}
				log.Printf("reloaded rules: %s", dnsstub.FormatRuleSummary(state.get().RulesConfig()))
				continue
			}
			log.Printf("signal %s, shutting down (%d active pin(s))", s, len(pinMgr.List()))
			cancel()
			if uiSrv != nil {
				_ = uiSrv.Shutdown()
			}
			_ = srv.Shutdown()
			return
		}
	}
}

type agentState struct {
	mu     sync.Mutex
	file   *appconfig.File
	path   string
	pins   string
	dpMode string
}

func (s *agentState) get() *appconfig.File {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file
}

func (s *agentState) pathLocked() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

func (s *agentState) set(f *appconfig.File, path, pins, dpMode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.file = f
	s.path = path
	s.pins = pins
	s.dpMode = dpMode
}

func (s *agentState) applyLive(dns *dnsstub.Server, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	s.mu.Lock()
	path := s.path
	pins := s.pins
	s.mu.Unlock()
	if path == "" {
		return fmt.Errorf("no config path")
	}
	newFile, err := appconfig.LoadFile(path)
	if err != nil {
		return err
	}
	newRules := newFile.RulesConfig()
	s.set(newFile, path, pins, newFile.Agent.DefaultPathMode)
	dns.SetRules(newRules)
	if err := syncDefaultPath(newFile, pins, dpApplier, dpIface); err != nil {
		// Keep DNS rules + Web UI usable; operator fixes ifaces without a dead agent.
		log.Printf("default-path apply degraded: %v", err)
	}
	// Browser traffic only hits the stub if systemd-resolved Domains= includes the rule.
	if err := modeb.SyncFromConfig(newFile); err != nil {
		log.Printf("mode B Domains sync: %v", err)
	} else if doms := modeb.RoutingDomains(newFile.Rules); len(doms) > 0 {
		log.Printf("mode B Domains synced (%d): %s", len(doms), strings.Join(doms, " "))
	}
	return nil
}

func (s *agentState) selectConfig(path string, dns *dnsstub.Server, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	path, err := appconfig.AssertAllowedConfigPath(path)
	if err != nil {
		return err
	}
	newFile, err := appconfig.LoadFile(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	pins := s.pins
	if p := strings.TrimSpace(newFile.Agent.Pins); p != "" {
		pins = p
	}
	s.mu.Unlock()
	s.set(newFile, path, pins, newFile.Agent.DefaultPathMode)
	dns.SetRules(newFile.RulesConfig())
	log.Printf("selected config %s", path)
	if err := syncDefaultPath(newFile, pins, dpApplier, dpIface); err != nil {
		log.Printf("default-path apply degraded: %v", err)
	}
	return nil
}

func (s *agentState) saveYAML(raw string, dns *dnsstub.Server, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	if path == "" {
		return fmt.Errorf("no active config path")
	}
	if _, err := appconfig.AssertAllowedConfigPath(path); err != nil {
		return err
	}
	f, err := appconfig.Parse([]byte(raw))
	if err != nil {
		return err
	}
	if err := appconfig.SaveFile(path, f); err != nil {
		return err
	}
	return s.applyLive(dns, dpApplier, dpIface)
}

func (s *agentState) newConfig(name string) (string, error) {
	path, err := appconfig.NewConfigPath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("already exists: %s", path)
	}
	if err := appconfig.SaveFile(path, appconfig.Default()); err != nil {
		return "", err
	}
	return path, nil
}

func (s *agentState) deleteConfig(path string) error {
	path, err := appconfig.AssertAllowedConfigPath(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	active := s.path
	s.mu.Unlock()
	if filepath.Clean(active) == filepath.Clean(path) {
		return fmt.Errorf("cannot delete active config %s — select another first", path)
	}
	return os.Remove(path)
}

func (s *agentState) addRule(in webui.RuleInput, dns *dnsstub.Server, pins *pathpin.Manager, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	s.mu.Lock()
	f := s.file
	path := s.path
	s.mu.Unlock()
	if f == nil || path == "" {
		return fmt.Errorf("no active config")
	}
	in = expandRuleInput(in)
	rule := webui.RuleFromInput(in, f)
	clone := *f
	clone.Rules = append(append([]rules.Rule{}, f.Rules...), rule)
	if err := appconfig.SaveFile(path, &clone); err != nil {
		return err
	}
	if err := s.applyLive(dns, dpApplier, dpIface); err != nil {
		return err
	}
	// Static IP matches never go through DNS — pin them now.
	if pins != nil {
		for _, raw := range in.IPs {
			ip := net.ParseIP(strings.TrimSpace(raw))
			if ip == nil {
				if _, n, err := net.ParseCIDR(raw); err == nil {
					ip = n.IP
				}
			}
			if ip == nil || ip.To4() == nil {
				continue
			}
			if _, err := pins.PinA(ip.To4(), rule.Interface, rule.Name, 24*time.Hour); err != nil {
				log.Printf("pin static %s via %s: %v", ip, rule.Interface, err)
			} else {
				log.Printf("pinned static %s via %s (rule %s)", ip, rule.Interface, rule.Name)
			}
		}
	}
	return nil
}

// expandRuleInput adds *.base when the user enters a bare domain (browser hits www/cdn hosts).
func expandRuleInput(in webui.RuleInput) webui.RuleInput {
	seen := map[string]struct{}{}
	var domains []string
	for _, d := range in.Domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			domains = append(domains, d)
		}
		if strings.HasPrefix(d, "*.") {
			continue
		}
		wild := "*." + strings.TrimPrefix(strings.ToLower(d), "*.")
		if _, ok := seen[wild]; !ok {
			seen[wild] = struct{}{}
			domains = append(domains, wild)
		}
	}
	in.Domains = domains
	return in
}

func (s *agentState) saveSettings(listen, uiListen, pinsMode, dpModeVal, dnsUpstream string, dns *dnsstub.Server, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	s.mu.Lock()
	f := s.file
	path := s.path
	s.mu.Unlock()
	if f == nil || path == "" {
		return fmt.Errorf("no active config")
	}
	clone := *f
	if strings.TrimSpace(listen) != "" {
		clone.Agent.Listen = strings.TrimSpace(listen)
	}
	if uiListen != "" {
		clone.Agent.UIListen = strings.TrimSpace(uiListen)
	}
	if strings.TrimSpace(pinsMode) != "" {
		clone.Agent.Pins = strings.TrimSpace(pinsMode)
		s.mu.Lock()
		s.pins = clone.Agent.Pins
		s.mu.Unlock()
	}
	if strings.TrimSpace(dpModeVal) != "" {
		clone.Agent.DefaultPathMode = strings.TrimSpace(dpModeVal)
	}
	if strings.TrimSpace(dnsUpstream) != "" {
		clone.Agent.DNSUpstream = strings.TrimSpace(dnsUpstream)
	}
	if err := appconfig.SaveFile(path, &clone); err != nil {
		return err
	}
	return s.applyLive(dns, dpApplier, dpIface)
}

func resolveDefaultPathIface(iface string) string {
	iface = strings.TrimSpace(iface)
	if iface == "" {
		return ""
	}
	if iface == "auto" {
		return netinfo.DefaultRouteIface()
	}
	return iface
}

func syncDefaultPath(f *appconfig.File, pins string, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	mode := f.Agent.DefaultPathMode
	iface := resolveDefaultPathIface(f.DefaultPath.Interface)
	live := mode == "netlink" || (mode == "auto" && pins == "netlink")
	c := *dpApplier
	if live && c == nil {
		var err error
		c, _, err = selectDefaultPath(mode, pins)
		if err != nil {
			return err
		}
		*dpApplier = c
	}
	if c == nil {
		return nil
	}
	if !live || iface == "" {
		if *dpIface != "" {
			_ = c.RemoveDefaultPath(*dpIface)
			*dpIface = ""
		}
		if live && strings.TrimSpace(f.DefaultPath.Interface) != "" && iface == "" {
			return fmt.Errorf("default_path.interface %q could not be resolved", f.DefaultPath.Interface)
		}
		return nil
	}
	if *dpIface != "" && *dpIface != iface {
		_ = c.RemoveDefaultPath(*dpIface)
	}
	if err := c.ApplyDefaultPath(iface); err != nil {
		return err
	}
	*dpIface = iface
	return nil
}

func (s *agentState) saveDefaultPath(iface, mode string, dns *dnsstub.Server, dpApplier *pathpin.DefaultPathController, dpIface *string) error {
	s.mu.Lock()
	if s.path == "" {
		s.mu.Unlock()
		return fmt.Errorf("no config path to write")
	}
	f := s.file
	if f == nil {
		s.mu.Unlock()
		return fmt.Errorf("no config loaded")
	}
	f.DefaultPath.Interface = iface
	f.Agent.DefaultPathMode = mode
	if iface == "" {
		f.Agent.DefaultPathMode = "off"
		mode = "off"
	}
	path := s.path
	pins := s.pins
	if err := appconfig.SaveFile(path, f); err != nil {
		s.mu.Unlock()
		return err
	}
	reloaded, err := appconfig.LoadFile(path)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.file = reloaded
	s.dpMode = reloaded.Agent.DefaultPathMode
	s.mu.Unlock()

	dns.SetRules(reloaded.RulesConfig())
	return syncDefaultPath(reloaded, pins, dpApplier, dpIface)
}

func loadAgentConfig(configPath, rulesPath string) (*appconfig.File, *rules.Config, string, error) {
	if configPath == "" {
		configPath = appconfig.FindExisting()
	}
	if configPath != "" {
		f, err := appconfig.LoadFile(configPath)
		if err != nil {
			return nil, nil, "", err
		}
		log.Printf("loaded config %s", configPath)
		return f, f.RulesConfig(), configPath, nil
	}
	if rulesPath == "" {
		rulesPath = "examples/example.rules.yaml"
		fmt.Fprintf(os.Stderr, "warning: no config found (tried GENESIS_CONFIG, configs/local.yaml, ~/.config/genesis/config.yaml)\n")
		fmt.Fprintf(os.Stderr, "warning: falling back to -rules %s — run: go run ./cmd/ctl setup\n", rulesPath)
	}
	rc, err := rules.LoadFile(rulesPath)
	if err != nil {
		return nil, nil, "", err
	}
	f := appconfig.Default()
	f.Defaults = rc.Defaults
	f.DefaultPath = rc.DefaultPath
	f.Rules = rc.Rules
	f.Version = rc.Version
	return f, rc, rulesPath, nil
}

func selectDefaultPath(mode, pinsMode string) (pathpin.DefaultPathController, string, error) {
	if mode == "on" {
		return nil, "", fmt.Errorf("default_path_mode %q is invalid; use auto|dry-run|netlink|off", mode)
	}
	if mode == "" || mode == "auto" {
		mode = pinsMode
		if mode == "" {
			mode = "auto"
		}
	}
	if mode == "off" {
		return nil, "off", nil
	}
	a, label, err := pathpin.SelectApplier(mode)
	if err != nil {
		return nil, "", err
	}
	c, ok := pathpin.AsDefaultPath(a)
	if !ok {
		return nil, "", fmt.Errorf("applier %s does not support default-path", label)
	}
	return c, label, nil
}

func defaultPIDFile() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "genesis.pid")
	}
	return filepath.Join(os.TempDir(), "genesis.pid")
}

func writePID(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
}
