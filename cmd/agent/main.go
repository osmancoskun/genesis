// Command agent runs the genesis client daemon (DNS stub + pin hooks).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/dnsstub"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
)

func main() {
	configPath := flag.String("config", "", "path to local config YAML (agent+rules); preferred over -rules")
	rulesPath := flag.String("rules", "", "path to rules-only YAML (legacy; ignored if -config set)")
	listen := flag.String("listen", "", "UDP listen override (default from config or "+appconfig.DefaultListen+")")
	pinsMode := flag.String("pins", "", "pin backend override: auto|dry-run|netlink|noop")
	defaultPathMode := flag.String("default-path", "", "default-path override: auto|dry-run|netlink|off")
	reaperEvery := flag.Duration("pin-reaper", time.Second, "how often to expire TTL'd pins")
	pidFile := flag.String("pidfile", "", "write PID here (default: runtime dir genesis.pid)")
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
			fmt.Fprintf(os.Stderr, "default-path: %v\n", err)
			os.Exit(1)
		}
		dpLabel = dpName
		dpIface = rulesCfg.DefaultPath.Interface
		if dpApplier != nil {
			if err := dpApplier.ApplyDefaultPath(dpIface); err != nil {
				log.Printf("default-path apply %s via %s: %v", dpIface, dpName, err)
				if rulesCfg.DefaultPath.OnIfaceDown == rules.FailClosed {
					fmt.Fprintf(os.Stderr, "default-path fail_closed: %v\n", err)
					os.Exit(1)
				}
			} else {
				log.Printf("default-path applied iface=%s mode=%s", dpIface, dpName)
			}
			defer func() {
				if err := dpApplier.RemoveDefaultPath(dpIface); err != nil {
					log.Printf("default-path remove: %v", err)
				}
			}()
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Fatalf("dns stub: %v", err)
			}
			return
		case s := <-sig:
			if s == syscall.SIGHUP {
				log.Printf("SIGHUP: reloading %s", loadedPath)
				newFile, newRules, path, err := loadAgentConfig(loadedPath, "")
				if err != nil {
					log.Printf("reload failed: %v (keeping previous rules)", err)
					continue
				}
				loadedPath = path
				_ = newFile
				srv.SetRules(newRules)
				// Re-apply default-path if iface changed and we have a live controller.
				if dpApplier != nil {
					newIface := newRules.DefaultPath.Interface
					if newIface != dpIface {
						if dpIface != "" {
							_ = dpApplier.RemoveDefaultPath(dpIface)
						}
						if newIface != "" {
							if err := dpApplier.ApplyDefaultPath(newIface); err != nil {
								log.Printf("default-path reload apply %s: %v", newIface, err)
							} else {
								log.Printf("default-path reloaded iface=%s", newIface)
								dpIface = newIface
							}
						} else {
							dpIface = ""
						}
					}
				}
				log.Printf("reloaded rules: %s", dnsstub.FormatRuleSummary(newRules))
				continue
			}
			log.Printf("signal %s, shutting down (%d active pin(s))", s, len(pinMgr.List()))
			cancel()
			_ = srv.Shutdown()
			return
		}
	}
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
		rulesPath = "configs/example.rules.yaml"
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
