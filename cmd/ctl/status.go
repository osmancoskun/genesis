package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dnsredirector/internal/appconfig"
)

// statusConfigPath prefers an explicit -config / GENESIS_CONFIG, then the
// system config when the unit is installed (that is what the service reads).
func statusConfigPath() string {
	if strings.TrimSpace(os.Getenv(appconfig.EnvConfig)) != "" {
		return appconfig.FindExisting()
	}
	sys := filepath.Join(appconfig.SystemConfigDir, "config.yaml")
	if unitInstalled() {
		if _, err := os.Stat(sys); err == nil {
			return sys
		}
	}
	if p := appconfig.FindExisting(); p != "" {
		return p
	}
	if _, err := os.Stat(sys); err == nil {
		return sys
	}
	return ""
}

func runStatus() error {
	path := statusConfigPath()
	if path == "" {
		fmt.Println("config:   none found (run: genesis-ctl setup)")
		fmt.Printf("looked in: %s, %s\n", stringsJoin(appconfig.CandidatePaths()),
			filepath.Join(appconfig.SystemConfigDir, "config.yaml"))
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
	fmt.Printf("service:  %s\n", serviceState())
	return nil
}

// printEndpoints shows where the agent listens and whether the Web UI answers.
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
