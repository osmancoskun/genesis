package main

import (
	"fmt"
	"net"
	"strings"
	"time"

	"dnsredirector/internal/appconfig"
)

func runStatus() error {
	path := activeConfigPath()
	if path == "" {
		fmt.Println("config:   none found (run: " + ctlCmd() + " setup)")
		fmt.Printf("looked in: %s\n", strings.Join(appconfig.CandidatePaths(), ", "))
		return nil
	}
	f, err := appconfig.LoadFile(path)
	if err != nil {
		return err
	}
	fmt.Printf("config:   %s\n", path)
	printEndpoints(f)
	fmt.Printf("pins:     %s\n", f.Agent.Pins)
	fmt.Printf("default:  %s (mode %s)\n", orDash(f.DefaultPath.Interface), f.Agent.DefaultPathMode)
	fmt.Printf("rules:    %d\n", len(f.Rules))
	for _, r := range f.Rules {
		match := strings.Join(r.Match.Domains, ",")
		if len(r.Match.IPs) > 0 {
			if match != "" {
				match += " "
			}
			match += "ips=" + strings.Join(r.Match.IPs, ",")
		}
		if match == "" {
			match = "-"
		}
		fmt.Printf("  - %-16s iface=%-16s dns=%-16s match=%s\n",
			orDash(r.Name), orDash(r.Interface), orDash(r.DNS), match)
	}
	fmt.Printf("service:  %s\n", serviceState())
	printConfigInventory(path)
	return nil
}

func printConfigInventory(active string) {
	items, err := appconfig.ListConfigFiles(active)
	if err != nil || len(items) == 0 {
		return
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		p := it.Path
		if it.Active {
			p += " *"
		}
		parts = append(parts, p)
	}
	fmt.Printf("configs:  %s\n", strings.Join(parts, ", "))
}

func printEndpoints(f *appconfig.File) {
	if ui := f.EffectiveUIListen(); ui != "" {
		fmt.Printf("web ui:   http://%s (%s)\n", ui, reachable(ui))
	} else {
		fmt.Println("web ui:   off")
	}
	fmt.Printf("dns:      udp %s\n", f.EffectiveListen())
}

func reachable(addr string) string {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return "not reachable"
	}
	_ = c.Close()
	return "reachable"
}

func serviceState() string {
	if !unitInstalled() {
		return "unit not installed"
	}
	if unitActive() {
		return systemdUnit + " active"
	}
	return systemdUnit + " inactive"
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
