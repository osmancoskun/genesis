package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/netinfo"
	"dnsredirector/internal/rules"
)

type setupPick struct {
	name    string
	domains []string
	ips     []string
	iface   string
	dns     string
}

func runSetup(args []string) error {
	outPath := appconfig.DefaultWritePath()
	for i := 0; i < len(args); i++ {
		if args[i] == "--out" && i+1 < len(args) {
			outPath = args[i+1]
			i++
		}
	}
	in := bufio.NewReader(os.Stdin)
	ask := func(prompt, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", prompt, def)
		} else {
			fmt.Printf("%s: ", prompt)
		}
		line, err := in.ReadString('\n')
		if err != nil {
			return def
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	clearScreen()
	fmt.Println("genesis setup")
	fmt.Println("-------------------")
	fmt.Printf("Config will be written to %s\n", outPath)
	fmt.Println("(override with GENESIS_CONFIG or --out PATH)")
	fmt.Println()

	ifaces, err := netinfo.ListIfaces()
	if err != nil {
		return err
	}
	fmt.Println("Interfaces:")
	fmt.Print(netinfo.FormatIfaceTable(ifaces, netinfo.FormatOpts{}))
	fmt.Println()

	up := netinfo.UpNames(ifaces)
	if len(up) == 0 {
		return fmt.Errorf("no up interfaces found")
	}

	cfg := appconfig.Default()
	cfg.Agent.Listen = ask("Agent listen address", appconfig.DefaultListen)
	cfg.Agent.DNSUpstream = ask("Default DNS upstream", appconfig.DefaultDNS)
	cfg.Defaults.DNS = cfg.Agent.DNSUpstream

	fmt.Println()
	fmt.Println("Default path (policy catch-all for non-pinned IPv4).")
	fmt.Println("Leave empty to disable (recommended while Cloudflare WARP is connected).")
	if hint := netinfo.DefaultRouteIface(); hint != "" {
		fmt.Printf("Current default-route iface (hint): %s\n", hint)
	}
	printChoices("Allowed interfaces", ifaceNames(ifaces))
	dpRaw := ask("Default-path interface (name or #)", "")
	dp := netinfo.ResolveIface(dpRaw, ifaces)
	if dp != "" {
		if !ifaceKnown(ifaces, dp) {
			fmt.Printf("warning: %q not in interface list; writing anyway\n", dp)
		}
		cfg.DefaultPath.Interface = dp
		for {
			printChoices("Allowed default_path_mode", choicesDefaultPathMode)
			raw := ask("default_path_mode (name or #)", "off")
			cfg.Agent.DefaultPathMode = resolveChoice(raw, choicesDefaultPathMode, "off")
			if cfg.Agent.DefaultPathMode == "on" {
				fmt.Println(`"on" is not valid — pick netlink for live apply, or dry-run / off.`)
				continue
			}
			if validDefaultPathMode(cfg.Agent.DefaultPathMode) {
				break
			}
			fmt.Printf("invalid %q\n", cfg.Agent.DefaultPathMode)
		}
	} else {
		cfg.DefaultPath.Interface = ""
		cfg.Agent.DefaultPathMode = "off"
	}

	printChoices("Allowed pins mode", choicesPins)
	for {
		raw := ask("Pins mode (name or #)", "dry-run")
		cfg.Agent.Pins = resolveChoice(raw, choicesPins, "dry-run")
		if validPinsMode(cfg.Agent.Pins) {
			break
		}
		fmt.Printf("invalid %q\n", cfg.Agent.Pins)
	}

	fmt.Println()
	fmt.Println("Enter domains to route (comma-separated). Empty to skip.")
	fmt.Println("Example: discord.com,*.discord.com,discord.app,*.discord.app,discord.gg,*.discord.gg,discordapp.com,*.discordapp.com,discordapp.net,*.discordapp.net")
	domains := splitCSV(ask("Domains", ""))

	fmt.Println()
	fmt.Println("Enter destination IPv4s / CIDRs to pin (comma-separated). Empty to skip.")
	ips := splitCSV(ask("IPs", ""))

	resolver := cfg.Agent.DNSUpstream
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var picks []setupPick

	for _, d := range domains {
		base := strings.TrimPrefix(d, "*.")
		fmt.Printf("\nProbing %q via each up iface (resolver %s)…\n", base, resolver)
		results := netinfo.ProbeDomainOnIfaces(ctx, up, resolver, base)
		printProbe(results)
		okIfaces := okIfaceNames(results)
		defIface := ""
		if len(okIfaces) > 0 {
			defIface = okIfaces[0]
		}
		printChoices("Allowed interfaces", ifaceNames(ifaces))
		chosenRaw := ask(fmt.Sprintf("Interface for %s (name or #)", d), defIface)
		chosen := netinfo.ResolveIface(chosenRaw, ifaces)
		if chosen == "" {
			fmt.Println("skipped")
			continue
		}
		picks = append(picks, setupPick{
			name:    sanitizeName(d),
			domains: []string{d},
			iface:   chosen,
			dns:     resolver,
		})
	}

	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		var probeIP net.IP
		if ip != nil {
			probeIP = ip.To4()
		} else if _, n, err := net.ParseCIDR(ipStr); err == nil {
			probeIP = n.IP.To4()
		}
		fmt.Printf("\nProbing destination %q…\n", ipStr)
		defIface := ""
		if probeIP != nil {
			results := netinfo.ProbeIPOnIfaces(ctx, up, probeIP)
			printProbe(results)
			if ok := okIfaceNames(results); len(ok) > 0 {
				defIface = ok[0]
			}
		} else {
			fmt.Println("  (could not parse for probe; pick iface manually)")
		}
		printChoices("Allowed interfaces", ifaceNames(ifaces))
		chosenRaw := ask(fmt.Sprintf("Interface for %s (name or #)", ipStr), defIface)
		chosen := netinfo.ResolveIface(chosenRaw, ifaces)
		if chosen == "" {
			fmt.Println("skipped")
			continue
		}
		picks = append(picks, setupPick{
			name:  "ip-" + sanitizeName(ipStr),
			ips:   []string{ipStr},
			iface: chosen,
			dns:   resolver,
		})
	}

	cfg.Rules = mergePicks(picks)

	if existing := appconfig.FindExisting(); existing != "" && existing != outPath {
		fmt.Printf("\nNote: another config exists at %s; writing to %s\n", existing, outPath)
	}
	if err := appconfig.SaveFile(outPath, cfg); err != nil {
		return err
	}
	fmt.Printf("\nWrote %s (%d rule(s))\n", outPath, len(cfg.Rules))
	fmt.Println("Next: go run ./cmd/ctl menu   or   go run ./cmd/agent -config", outPath)
	return nil
}

func printProbe(results []netinfo.ProbeResult) {
	fmt.Print(netinfo.FormatProbeTable(results))
}

func okIfaceNames(results []netinfo.ProbeResult) []string {
	var out []string
	for _, r := range results {
		if r.OK {
			out = append(out, r.Iface)
		}
	}
	return out
}

func ifaceKnown(ifaces []netinfo.Iface, name string) bool {
	for _, ifi := range ifaces {
		if ifi.Name == name {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	repl := strings.NewReplacer("*", "star", ".", "-", "/", "-", ":", "-", " ", "-")
	s = repl.Replace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		s = "rule"
	}
	return s
}

func mergePicks(picks []setupPick) []rules.Rule {
	type key struct{ iface, dns string }
	groups := map[key]*rules.Rule{}
	var order []key
	pin := true
	for i, p := range picks {
		k := key{iface: p.iface, dns: p.dns}
		r, ok := groups[k]
		if !ok {
			name := p.name
			if name == "" {
				name = "rule-" + strconv.Itoa(i+1)
			}
			r = &rules.Rule{
				Name:           name,
				DNS:            p.dns,
				Interface:      p.iface,
				PinConnections: &pin,
			}
			groups[k] = r
			order = append(order, k)
		}
		r.Match.Domains = append(r.Match.Domains, p.domains...)
		r.Match.IPs = append(r.Match.IPs, p.ips...)
	}
	out := make([]rules.Rule, 0, len(order))
	for _, k := range order {
		r := groups[k]
		if r == nil {
			continue
		}
		out = append(out, *r)
	}
	return out
}
