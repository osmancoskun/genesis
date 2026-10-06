// Package svcstatus reports local genesis agent / systemd unit state for the Web UI.
package svcstatus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"dnsredirector/internal/pathpin"
)

const SystemdUnit = "genesis.service"

// Snapshot is a point-in-time health view.
type Snapshot struct {
	AgentSelf     bool   `json:"agent_self"` // this process (always true when UI answers)
	AgentPID      int    `json:"agent_pid"`
	PIDFile       string `json:"pid_file"`
	PIDFileAlive  bool   `json:"pid_file_alive"`
	UnitInstalled bool   `json:"unit_installed"`
	UnitActive    bool   `json:"unit_active"`
	UnitState     string `json:"unit_state"`
	HasNetAdmin   bool   `json:"has_net_admin"`
	EUID          int    `json:"euid"`
}

// Collect gathers host/agent status without requiring root.
func Collect() Snapshot {
	s := Snapshot{
		AgentSelf: true,
		AgentPID:  os.Getpid(),
		EUID:      os.Geteuid(),
		PIDFile:   defaultPIDPath(),
	}
	if err := pathpin.ProbeNetAdmin(); err == nil {
		s.HasNetAdmin = true
	}
	if pid, _, err := readPIDFile(); err == nil {
		s.PIDFileAlive = processAlive(pid)
	}
	s.UnitInstalled = unitInstalled()
	if s.UnitInstalled {
		s.UnitState = unitState()
		s.UnitActive = s.UnitState == "active"
	} else {
		s.UnitState = "not-installed"
	}
	return s
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

func unitInstalled() bool {
	return exec.Command("systemctl", "cat", SystemdUnit).Run() == nil
}

func unitState() string {
	out, err := exec.Command("systemctl", "is-active", SystemdUnit).CombinedOutput()
	state := strings.TrimSpace(string(out))
	if err != nil && state == "" {
		return "unknown"
	}
	return state
}
