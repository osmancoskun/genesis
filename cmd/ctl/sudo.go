package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"dnsredirector/internal/appconfig"
	"dnsredirector/internal/pathpin"
)

func hasNetAdmin() bool {
	return pathpin.ProbeNetAdmin() == nil
}

// wantsLiveNetlink reports whether config asks for real policy routing.
func wantsLiveNetlink(f *appconfig.File) bool {
	pins := strings.ToLower(strings.TrimSpace(f.Agent.Pins))
	if pins == "netlink" {
		return true
	}
	if strings.TrimSpace(f.DefaultPath.Interface) == "" {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(f.Agent.DefaultPathMode))
	if mode == "" || mode == "auto" {
		mode = pins
		if mode == "" {
			mode = "auto"
		}
	}
	return mode == "netlink" || mode == "auto"
}

func isPrivErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "CAP_NET_ADMIN") ||
		strings.Contains(msg, "insufficient privilege") ||
		strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "permission denied")
}

// runWithSudo re-execs this ctl binary under sudo -E so the TTY can ask for a password.
func runWithSudo(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Println("Elevating with sudo (enter password if prompted)…")
	cmd := exec.Command("sudo", append([]string{"-E", self}, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = sudoEnv()
	return cmd.Run()
}

// maybeSudoPrefix wraps bin/args with sudo -E when live netlink is required and missing.
func maybeSudoPrefix(bin string, args []string, f *appconfig.File) (string, []string) {
	if !wantsLiveNetlink(f) || hasNetAdmin() {
		return bin, args
	}
	fmt.Println("Config wants live netlink but CAP_NET_ADMIN is missing.")
	fmt.Println("Elevating with sudo (enter password if prompted)…")
	// Preserve PATH so `go` remains visible under sudo on Fedora.
	return "sudo", append([]string{"-E", "env", "PATH=" + os.Getenv("PATH"), bin}, args...)
}

func sudoEnv() []string {
	env := os.Environ()
	// Ensure PATH is present for child even if sudo filters -E oddly.
	path := os.Getenv("PATH")
	if path == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			out = append(out, "PATH="+path)
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, "PATH="+path)
	}
	return out
}
