package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const systemdUnit = "genesis.service"

func runService(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: ctl up|down|start|stop|restart|apply|reload|service-status")
	}
	cmd := args[0]
	switch cmd {
	case "up", "start":
		return serviceUp()
	case "down", "stop":
		return serviceDown()
	case "restart":
		_ = serviceDown()
		return serviceUp()
	case "apply", "reload":
		return serviceApply()
	case "service-status", "svc-status":
		return serviceStatus()
	default:
		return fmt.Errorf("unknown service command %q (want up|down|restart|apply|service-status)", cmd)
	}
}

func serviceUp() error {
	if unitInstalled() {
		fmt.Println("Starting systemd unit", systemdUnit, "…")
		return runSystemctl("start", systemdUnit)
	}
	fmt.Println("No systemd unit installed — see docs/service.md")
	fmt.Println("Falling back to foreground tip:")
	fmt.Println("  go run ./cmd/ctl run")
	fmt.Println("Install: sudo make install ENABLE=1")
	return fmt.Errorf("systemd unit %s not found", systemdUnit)
}

func serviceDown() error {
	if unitInstalled() {
		fmt.Println("Stopping systemd unit", systemdUnit, "…")
		return runSystemctl("stop", systemdUnit)
	}
	return signalPIDFile(syscall.SIGTERM)
}

func serviceApply() error {
	if unitInstalled() && unitActive() {
		fmt.Println("Reloading", systemdUnit, "(SIGHUP)…")
		return runSystemctl("reload", systemdUnit)
	}
	fmt.Println("Sending SIGHUP to agent pidfile for config hot-reload…")
	return signalPIDFile(syscall.SIGHUP)
}

func serviceStatus() error {
	if unitInstalled() {
		_ = runSystemctl("status", systemdUnit)
		return nil
	}
	pid, path, err := readPIDFile()
	if err != nil {
		fmt.Println("agent: not running (no unit, no pidfile)")
		return nil
	}
	if processAlive(pid) {
		fmt.Printf("agent: running pid=%d pidfile=%s\n", pid, path)
	} else {
		fmt.Printf("agent: stale pidfile %s (pid %d)\n", path, pid)
	}
	return nil
}

func unitInstalled() bool {
	cmd := exec.Command("systemctl", "cat", systemdUnit)
	return cmd.Run() == nil
}

func unitActive() bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", systemdUnit)
	return cmd.Run() == nil
}

func runSystemctl(args ...string) error {
	full := append([]string{}, args...)
	cmd := exec.Command("systemctl", full...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		// Retry with sudo for privileged unit ops.
		fmt.Println("Retrying with sudo…")
		scmd := exec.Command("sudo", append([]string{"systemctl"}, full...)...)
		scmd.Stdout = os.Stdout
		scmd.Stderr = os.Stderr
		scmd.Stdin = os.Stdin
		return scmd.Run()
	}
	return nil
}

func defaultPIDPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "genesis.pid")
	}
	return filepath.Join(os.TempDir(), "genesis.pid")
}

func readPIDFile() (int, string, error) {
	path := defaultPIDPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, path, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, path, err
	}
	return pid, path, nil
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func signalPIDFile(sig syscall.Signal) error {
	pid, path, err := readPIDFile()
	if err != nil {
		return fmt.Errorf("no agent pidfile at %s (start with ctl run or systemctl): %w", path, err)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := p.Signal(sig); err != nil {
		return fmt.Errorf("signal %v to pid %d: %w", sig, pid, err)
	}
	fmt.Printf("sent %v to pid %d (%s)\n", sig, pid, path)
	return nil
}
