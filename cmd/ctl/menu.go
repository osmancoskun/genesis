package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/doctor"
	"dnsredirector/internal/netinfo"
	"dnsredirector/internal/pathpin"
)

func runMenu(_ []string) error {
	in := bufio.NewReader(os.Stdin)
	for {
		clearScreen()
		path := appconfig.FindExisting()
		printMenuTable("genesis menu", []menuRow{
			{"1", "setup", "interactive config wizard"},
			{"2", "run", "start agent with current config"},
			{"3", "view config", "print loaded config summary"},
			{"4", "list networks", "show interfaces"},
			{"5", "default-path", "set iface + mode in config"},
			{"6", "doctor", "host readiness checks"},
			{"7", "pin check", "CAP_NET_ADMIN probe"},
			{"8", "service", "up / down / apply (hot reload)"},
			{"0", "quit", ""},
		})
		fmt.Println()
		if path == "" {
			fmt.Println("No config found. Run setup (1) first.")
			fmt.Printf("Looked in: %s\n", strings.Join(appconfig.CandidatePaths(), ", "))
		} else {
			fmt.Printf("Config: %s\n", path)
		}
		fmt.Print("\n> ")
		line, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		choice := strings.TrimSpace(line)
		switch choice {
		case "1", "setup":
			clearScreen()
			if err := runSetup(nil); err != nil {
				fmt.Fprintf(os.Stderr, "setup: %v\n", err)
			}
			pauseEnter(in)
		case "2", "run":
			clearScreen()
			if err := menuRun(); err != nil {
				fmt.Fprintf(os.Stderr, "run: %v\n", err)
			}
			pauseEnter(in)
		case "8", "service", "up", "apply":
			clearScreen()
			printMenuTable("service", []menuRow{
				{"1", "up", "systemctl start genesis"},
				{"2", "down", "systemctl stop"},
				{"3", "apply", "hot-reload config (SIGHUP)"},
				{"4", "status", "service-status"},
				{"0", "back", ""},
			})
			fmt.Print("\n> ")
			sub, err := in.ReadString('\n')
			if err != nil {
				return err
			}
			switch strings.TrimSpace(sub) {
			case "1", "up":
				_ = runService([]string{"up"})
			case "2", "down":
				_ = runService([]string{"down"})
			case "3", "apply":
				_ = runService([]string{"apply"})
			case "4", "status":
				_ = runService([]string{"service-status"})
			}
			pauseEnter(in)
		case "3", "view", "config":
			clearScreen()
			if err := menuViewConfig(); err != nil {
				fmt.Fprintf(os.Stderr, "view: %v\n", err)
			}
			pauseEnter(in)
		case "4", "list", "ifaces":
			clearScreen()
			if err := menuListNetworks(); err != nil {
				fmt.Fprintf(os.Stderr, "list: %v\n", err)
			}
			pauseEnter(in)
		case "5", "gateway", "default-path":
			clearScreen()
			if err := menuSetDefaultPath(in); err != nil {
				fmt.Fprintf(os.Stderr, "default-path: %v\n", err)
			}
			pauseEnter(in)
		case "6", "doctor":
			clearScreen()
			fmt.Print(doctor.Run().Format())
			pauseEnter(in)
		case "7", "pin":
			clearScreen()
			_ = runPin([]string{"check"})
			pauseEnter(in)
		case "0", "q", "quit", "exit":
			clearScreen()
			return nil
		default:
			fmt.Println("unknown choice")
			pauseEnter(in)
		}
	}
}

func menuViewConfig() error {
	fmt.Println("view config")
	fmt.Println("-----------")
	path := appconfig.FindExisting()
	if path == "" {
		fmt.Println("No config found. Run setup first: go run ./cmd/ctl setup")
		return nil
	}
	f, err := appconfig.LoadFile(path)
	if err != nil {
		return err
	}
	w := newAligned()
	fmt.Fprintf(w, "file\t%s\n", path)
	listen := f.EffectiveListen()
	if v := strings.TrimSpace(os.Getenv(appconfig.EnvListen)); v != "" {
		listen += " (env " + appconfig.EnvListen + ")"
	}
	fmt.Fprintf(w, "listen\t%s\n", listen)
	fmt.Fprintf(w, "pins\t%s\n", f.Agent.Pins)
	fmt.Fprintf(w, "default_path_mode\t%s\n", f.Agent.DefaultPathMode)
	fmt.Fprintf(w, "dns_upstream\t%s\n", f.Agent.DNSUpstream)
	_ = w.Flush()
	fmt.Println()
	printRules(f.RulesConfig())
	return nil
}

func menuListNetworks() error {
	fmt.Println("list networks")
	fmt.Println("-------------")
	ifaces, err := netinfo.ListIfaces()
	if err != nil {
		return err
	}
	opts := netinfo.FormatOpts{}
	if path := appconfig.FindExisting(); path != "" {
		if f, err := appconfig.LoadFile(path); err == nil {
			opts.ConfigDefaultPath = f.DefaultPath.Interface
		}
	}
	fmt.Print(netinfo.FormatIfaceTable(ifaces, opts))
	return nil
}

func menuSetDefaultPath(in *bufio.Reader) error {
	fmt.Println("set default-path")
	fmt.Println("----------------")
	path := appconfig.FindExisting()
	if path == "" {
		return fmt.Errorf("no config — run setup first")
	}
	f, err := appconfig.LoadFile(path)
	if err != nil {
		return err
	}
	ifaces, err := netinfo.ListIfaces()
	if err != nil {
		return err
	}
	fmt.Println("Interfaces:")
	fmt.Print(netinfo.FormatIfaceTable(ifaces, netinfo.FormatOpts{
		ConfigDefaultPath: f.DefaultPath.Interface,
	}))
	fmt.Println()
	w := newAligned()
	fmt.Fprintf(w, "current CONFIG iface\t%q\n", f.DefaultPath.Interface)
	fmt.Fprintf(w, "current mode\t%s\n", f.Agent.DefaultPathMode)
	fmt.Fprintf(w, "live KERNEL default\t%q\n", netinfo.DefaultRouteIface())
	_ = w.Flush()
	fmt.Println()
	printChoices("Allowed interfaces", ifaceNames(ifaces))
	fmt.Print("New CONFIG interface (name or #, empty disables): ")
	line, err := in.ReadString('\n')
	if err != nil {
		return err
	}
	iface := netinfo.ResolveIface(line, ifaces)
	f.DefaultPath.Interface = iface
	if iface == "" {
		f.Agent.DefaultPathMode = "off"
	} else {
		for {
			printChoices("Allowed default_path_mode", choicesDefaultPathMode)
			fmt.Print("default_path_mode (name or #) [off]: ")
			modeLine, err := in.ReadString('\n')
			if err != nil {
				return err
			}
			mode := resolveChoice(modeLine, choicesDefaultPathMode, "off")
			if mode == "on" {
				fmt.Println(`"on" is not valid — pick netlink for live apply, or dry-run / off.`)
				continue
			}
			if !validDefaultPathMode(mode) {
				fmt.Printf("invalid %q\n", mode)
				continue
			}
			f.Agent.DefaultPathMode = mode
			break
		}
		plan, err := pathpin.BuildDefaultPathPlan(iface, 0, nil, nil)
		if err != nil {
			fmt.Printf("dry-run plan error: %v\n", err)
		} else {
			fmt.Println("planned ops:")
			fmt.Println(plan.Describe())
		}
	}
	if err := appconfig.SaveFile(path, f); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Saved config — CONFIG column updated. KERNEL is unchanged until live apply:")
	fmt.Print(netinfo.FormatIfaceTable(ifaces, netinfo.FormatOpts{
		ConfigDefaultPath: f.DefaultPath.Interface,
	}))

	if f.DefaultPath.Interface != "" && (f.Agent.DefaultPathMode == "netlink" || f.Agent.DefaultPathMode == "auto") {
		fmt.Println()
		fmt.Println("WARNING: live apply can break Cloudflare WARP (prio 5100 < WARP ~5209).")
		fmt.Print("Apply default-path to kernel now? [y/N]: ")
		ans, err := in.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(ans), "y") || strings.EqualFold(strings.TrimSpace(ans), "yes") {
			if err := applyDefaultPathLive(f.DefaultPath.Interface, f.Agent.DefaultPathMode, f.Agent.Pins); err != nil {
				fmt.Fprintf(os.Stderr, "live apply failed: %v\n", err)
			} else {
				fmt.Println("Applied. Re-probing KERNEL column:")
				fmt.Print(netinfo.FormatIfaceTable(ifaces, netinfo.FormatOpts{
					ConfigDefaultPath: f.DefaultPath.Interface,
				}))
			}
		} else {
			fmt.Println("Skipped live apply. KERNEL stays on the current default-route iface.")
		}
	} else if f.Agent.DefaultPathMode == "dry-run" {
		fmt.Println("mode=dry-run: agent will only log plans; KERNEL will not move.")
	} else {
		fmt.Println("mode=off or empty iface: no catch-all; KERNEL stays as-is.")
	}
	return nil
}

func applyDefaultPathLive(iface, mode, pins string) error {
	if mode == "auto" {
		mode = pins
		if mode == "" {
			mode = "auto"
		}
	}
	if mode == "off" || mode == "dry-run" {
		return fmt.Errorf("mode %q does not apply to kernel (need netlink)", mode)
	}
	err := applyDefaultPathNetlink(iface)
	if err == nil {
		return nil
	}
	if !isPrivErr(err) {
		return err
	}
	fmt.Printf("live apply needs root: %v\n", err)
	return runWithSudo("default-path", "apply", "--iface", iface)
}

func applyDefaultPathNetlink(iface string) error {
	a, label, err := pathpin.SelectApplier("netlink")
	if err != nil {
		return err
	}
	c, ok := pathpin.AsDefaultPath(a)
	if !ok {
		return fmt.Errorf("applier %s does not support default-path", label)
	}
	fmt.Printf("Applying via %s…\n", label)
	return c.ApplyDefaultPath(iface)
}

func ifaceNames(ifaces []netinfo.Iface) []string {
	out := make([]string, len(ifaces))
	for i, ifi := range ifaces {
		out[i] = ifi.Name
	}
	return out
}

func menuRun() error {
	fmt.Println("run agent")
	fmt.Println("---------")
	path := appconfig.FindExisting()
	if path == "" {
		fmt.Println("No config found. Run setup first (menu option 1).")
		return nil
	}
	f, err := appconfig.LoadFile(path)
	if err != nil {
		return err
	}
	bin, args := agentLaunch(path)
	bin, args = maybeSudoPrefix(bin, args, f)
	w := newAligned()
	fmt.Fprintf(w, "command\t%s %s\n", bin, strings.Join(args, " "))
	fmt.Fprintf(w, "listen\t%s\n", f.EffectiveListen())
	fmt.Fprintf(w, "pins\t%s\n", f.Agent.Pins)
	fmt.Fprintf(w, "default-path\t%s\n", f.Agent.DefaultPathMode)
	fmt.Fprintf(w, "default_path.iface\t%q\n", f.DefaultPath.Interface)
	_ = w.Flush()
	if wantsLiveNetlink(f) {
		fmt.Println("Config will install live policy routes (pins and/or default-path).")
	} else {
		fmt.Println("Config is dry-run/off for netlink — KERNEL default-route will not move.")
	}
	fmt.Println("Ctrl-C stops the agent.")
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = sudoEnv()
	return cmd.Run()
}

func agentLaunch(configPath string) (string, []string) {
	self, err := os.Executable()
	if err == nil {
		cand := filepath.Join(filepath.Dir(self), "agent")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand, []string{"-config", configPath}
		}
	}
	return "go", []string{"run", "./cmd/agent", "-config", configPath}
}
