// Command ctl is the genesis CLI (menu, setup, doctor, status, rules, pin, default-path).
package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/doctor"
	"dnsredirector/internal/netinfo"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
)

func main() {
	args := os.Args[1:]
	args, err := applyConfigFlag(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctl: %v\n", err)
		os.Exit(2)
	}
	if len(args) < 1 {
		if isInteractive() {
			if err := runMenu(nil); err != nil {
				fmt.Fprintf(os.Stderr, "menu: %v\n", err)
				os.Exit(1)
			}
			return
		}
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "menu":
		if err := runMenu(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "menu: %v\n", err)
			os.Exit(1)
		}
	case "up", "down", "start", "stop", "restart", "apply", "reload", "service-status", "svc-status":
		if err := runService(args); err != nil {
			fmt.Fprintf(os.Stderr, "service: %v\n", err)
			os.Exit(1)
		}
	case "setup":
		if err := runSetup(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "setup: %v\n", err)
			os.Exit(1)
		}
	case "config":
		if err := runConfig(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			os.Exit(1)
		}
	case "ifaces", "networks":
		ifaces, err := netinfo.ListIfaces()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ifaces: %v\n", err)
			os.Exit(1)
		}
		opts := netinfo.FormatOpts{}
		if p := appconfig.FindExisting(); p != "" {
			if f, err := appconfig.LoadFile(p); err == nil {
				opts.ConfigDefaultPath = f.DefaultPath.Interface
			}
		}
		fmt.Print(netinfo.FormatIfaceTable(ifaces, opts))
	case "doctor":
		fmt.Print(doctor.Run().Format())
	case "rules":
		path := ""
		if len(args) > 1 {
			path = args[1]
		} else if p := appconfig.FindExisting(); p != "" {
			path = p
		} else {
			path = "configs/example.rules.yaml"
		}
		if f, err := appconfig.LoadFile(path); err == nil {
			printRules(f.RulesConfig())
			return
		}
		cfg, err := rules.LoadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rules: %v\n", err)
			os.Exit(1)
		}
		printRules(cfg)
	case "status":
		path := appconfig.FindExisting()
		if path == "" {
			fmt.Println("status: no config found — run: go run ./cmd/ctl setup")
			fmt.Printf("looked in: %s\n", stringsJoin(appconfig.CandidatePaths()))
			return
		}
		f, err := appconfig.LoadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "status: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("config: %s\n", path)
		fmt.Printf("listen: %s  pins: %s  default_path_mode: %s\n",
			f.EffectiveListen(), f.Agent.Pins, f.Agent.DefaultPathMode)
		fmt.Println("agent not queried yet (no control socket in MVP)")
		fmt.Println("defaults: iface-down=fail_closed; ipv6=v4-first; doh=warn-only")
		fmt.Println("coexistence: docs/resolved-coexistence.md")
	case "pin":
		if err := runPin(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "pin: %v\n", err)
			os.Exit(1)
		}
	case "default-path":
		if err := runDefaultPath(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "default-path: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		usage()
		os.Exit(2)
	}
}

// applyConfigFlag pulls -config/--config PATH from args and sets GENESIS_CONFIG.
func applyConfigFlag(args []string) ([]string, error) {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-config" || a == "--config":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("%s requires a path", a)
			}
			i++
			if err := os.Setenv(appconfig.EnvConfig, args[i]); err != nil {
				return nil, err
			}
		case strings.HasPrefix(a, "-config="):
			if err := os.Setenv(appconfig.EnvConfig, strings.TrimPrefix(a, "-config=")); err != nil {
				return nil, err
			}
		case strings.HasPrefix(a, "--config="):
			if err := os.Setenv(appconfig.EnvConfig, strings.TrimPrefix(a, "--config=")); err != nil {
				return nil, err
			}
		default:
			out = append(out, a)
		}
	}
	return out, nil
}

func runConfig(args []string) error {
	if len(args) == 0 || args[0] == "show" || args[0] == "view" {
		return menuViewConfig()
	}
	return fmt.Errorf("usage: ctl config show")
}

func isInteractive() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func stringsJoin(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func runPin(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: ctl pin check | ctl pin dry-run --dst IP --iface IFACE")
	}
	switch args[0] {
	case "check":
		if err := pathpin.ProbeNetAdmin(); err != nil {
			fmt.Println("pin check: NOT ready for live netlink pins")
			fmt.Println(err.Error())
			fmt.Println("safe default: go run ./cmd/agent -pins dry-run …")
			return nil
		}
		fmt.Println("pin check: CAP_NET_ADMIN appears available for live netlink pins")
		fmt.Println("still prefer: ctl pin dry-run --dst 192.0.2.10 --iface <iface> before -pins netlink")
		fmt.Println("never point automated tests at live VPN ifaces you need")
		return nil
	case "dry-run":
		dstStr, iface := "192.0.2.10", ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--dst":
				i++
				if i < len(args) {
					dstStr = args[i]
				}
			case "--iface":
				i++
				if i < len(args) {
					iface = args[i]
				}
			}
		}
		if iface == "" {
			return fmt.Errorf("dry-run requires --iface (use a disposable test iface name in the plan text; this command does not change the kernel)")
		}
		ip := net.ParseIP(dstStr)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("invalid IPv4 --dst %q (hint: use TEST-NET 192.0.2.10)", dstStr)
		}
		plan, err := pathpin.BuildPlan(pathpin.Pin{
			Dst:       ip.To4(),
			Interface: iface,
			RuleName:  "ctl-dry-run",
			Expires:   time.Now().Add(time.Minute),
		}, 0, nil)
		if err != nil {
			return err
		}
		fmt.Println("no kernel changes — planned ops:")
		fmt.Println(plan.Describe())
		fmt.Printf("owned tables %d–%d; rule priority %d\n", pathpin.TableBase, pathpin.TableBase+pathpin.TableSpan-1, pathpin.RulePref)
		return nil
	default:
		return fmt.Errorf("unknown pin subcommand %q", args[0])
	}
}

func runDefaultPath(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: ctl default-path check | dry-run --iface IFACE | apply --iface IFACE")
	}
	switch args[0] {
	case "check":
		if err := pathpin.ProbeNetAdmin(); err != nil {
			fmt.Println("default-path check: NOT ready for live netlink apply")
			fmt.Println(err.Error())
			fmt.Println("safe default: ctl default-path dry-run --iface <vpn-or-nic>")
			return nil
		}
		fmt.Println("default-path check: CAP_NET_ADMIN appears available")
		fmt.Println("live apply uses owned table", pathpin.DefaultPathTable, "priority", pathpin.DefaultPathPref)
		fmt.Println("never edits main-table default; never toggles VPN/Tailscale links")
		fmt.Println("prefer dry-run first; do not point at tunnels you cannot afford to mis-route")
		return nil
	case "dry-run":
		iface := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--iface" {
				i++
				if i < len(args) {
					iface = args[i]
				}
			}
		}
		if iface == "" {
			return fmt.Errorf("dry-run requires --iface (real iface name in the plan text; no kernel changes)")
		}
		plan, err := pathpin.BuildDefaultPathPlan(iface, 0, nil, nil)
		if err != nil {
			return err
		}
		fmt.Println("no kernel changes — planned ops:")
		fmt.Println(plan.Describe())
		fmt.Printf("pins win at priority %d; preserve-main at %d; default-path catch-all at %d (table %d)\n",
			pathpin.RulePref, pathpin.MainPreservePref, pathpin.DefaultPathPref, pathpin.DefaultPathTable)
		fmt.Println("note: priorities sit below typical Cloudflare WARP catch-all (~5209)")
		fmt.Println("note: live apply also adds lookup-main preserve rules for connected IPv4 prefixes in main")
		return nil
	case "apply":
		iface := ""
		for i := 1; i < len(args); i++ {
			if args[i] == "--iface" {
				i++
				if i < len(args) {
					iface = args[i]
				}
			}
		}
		if iface == "" {
			return fmt.Errorf("apply requires --iface")
		}
		if err := pathpin.ProbeNetAdmin(); err != nil {
			return err
		}
		a, label, err := pathpin.SelectApplier("netlink")
		if err != nil {
			return err
		}
		c, ok := pathpin.AsDefaultPath(a)
		if !ok {
			return fmt.Errorf("applier %s does not support default-path", label)
		}
		fmt.Printf("Applying default-path iface=%s via %s…\n", iface, label)
		if err := c.ApplyDefaultPath(iface); err != nil {
			return err
		}
		fmt.Println("default-path applied")
		return nil
	default:
		return fmt.Errorf("unknown default-path subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `genesis ctl

Usage:
  go run ./cmd/ctl [-config PATH]              # interactive menu (TTY)
  go run ./cmd/ctl [-config PATH] menu
  go run ./cmd/ctl [-config PATH] run          # start agent with config (foreground)
  go run ./cmd/ctl up|down|restart|apply       # systemd service / SIGHUP hot-reload
  go run ./cmd/ctl service-status
  go run ./cmd/ctl [-config PATH] setup [--out PATH]
  go run ./cmd/ctl [-config PATH] config show
  go run ./cmd/ctl ifaces
  go run ./cmd/ctl doctor
  go run ./cmd/ctl rules [path]
  go run ./cmd/ctl status
  go run ./cmd/ctl pin check
  go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface eno1
  go run ./cmd/ctl default-path check
  go run ./cmd/ctl default-path dry-run --iface wg0
  go run ./cmd/ctl default-path apply --iface eno1   # needs CAP_NET_ADMIN / sudo

Examples:
  go run ./cmd/ctl -config configs/discord.config.yaml menu
  go run ./cmd/ctl -config configs/discord.config.yaml run
  go run ./cmd/agent -config configs/discord.config.yaml

Env (same as -config / -listen on agent):
  GENESIS_CONFIG   config file path (default configs/local.yaml)
  GENESIS_LISTEN   override agent listen (default 127.0.0.1:5553)
`)
}

func printRules(cfg *rules.Config) {
	if cfg.DefaultPathEnabled() {
		fmt.Printf("default_path: iface=%s on_down=%s (catch-all via owned table %d)\n",
			cfg.DefaultPath.Interface, cfg.DefaultPath.OnIfaceDown, pathpin.DefaultPathTable)
	} else {
		fmt.Println("default_path: disabled")
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tDNS\tIFACE\tPIN\tON_DOWN\tMATCH\n")
	for _, r := range cfg.Rules {
		pin := "true"
		if r.PinConnections != nil && !*r.PinConnections {
			pin = "false"
		}
		match := fmt.Sprintf("domains=%v ips=%v", r.Match.Domains, r.Match.IPs)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.DNS, r.Interface, pin, r.OnIfaceDown, match)
	}
	_ = w.Flush()
}
