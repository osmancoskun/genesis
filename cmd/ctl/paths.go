package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dnsredirector/internal/appconfig"
)

// activeConfigPath is the config the CLI treats as current — aligned with the
// systemd unit when it is installed ( /etc/genesis/config.yaml ).
func activeConfigPath() string {
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
	return ""
}

// defaultConfigWritePath is where setup writes when --out / GENESIS_CONFIG unset.
func defaultConfigWritePath() string {
	if p := strings.TrimSpace(os.Getenv(appconfig.EnvConfig)); p != "" {
		return p
	}
	if unitInstalled() {
		return filepath.Join(appconfig.SystemConfigDir, "config.yaml")
	}
	return appconfig.DefaultWritePath()
}

// ctlCmd is how we refer to this CLI in user-facing tips.
func ctlCmd() string {
	base := filepath.Base(os.Args[0])
	if base == "genesis-ctl" || base == "ctl" {
		return "genesis-ctl"
	}
	if _, err := exec.LookPath("genesis-ctl"); err == nil {
		return "genesis-ctl"
	}
	return "go run ./cmd/ctl"
}

func agentCmd() string {
	if _, err := exec.LookPath("genesis-agent"); err == nil {
		return "genesis-agent"
	}
	return "go run ./cmd/agent"
}
