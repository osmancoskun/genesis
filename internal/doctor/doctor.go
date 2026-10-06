// Package doctor inspects the host DNS/routing stack for conflicts with genesis.
// Host probes use native files, net APIs, and D-Bus — not subprocesses.
package doctor

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

// Finding is one line item in a doctor report.
type Finding struct {
	Level   string // info, warn, conflict
	Code    string
	Message string
}

// Report is the Phase 0 "who will fight us" summary.
type Report struct {
	Findings []Finding
}

// Run gathers host facts without requiring root and without spawning processes.
func Run() Report {
	var r Report
	r.checkResolvConf()
	r.checkUnit("systemd-resolved.service")
	r.checkUnit("NetworkManager.service")
	r.checkPort53()
	r.checkCapabilities()
	r.checkPinPriv()
	r.checkCoexistence()
	r.checkDoHWarn()
	r.checkInterfaces()
	return r
}

func (r *Report) add(level, code, msg string) {
	r.Findings = append(r.Findings, Finding{Level: level, Code: code, Message: msg})
}

func (r *Report) checkResolvConf() {
	const path = "/etc/resolv.conf"
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		r.add("warn", "resolv.conf", fmt.Sprintf("cannot resolve %s: %v", path, err))
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		r.add("warn", "resolv.conf", err.Error())
		return
	}
	r.add("info", "resolv.conf", fmt.Sprintf("%s → %s", path, target))
	lower := strings.ToLower(string(data))
	if strings.Contains(target, "systemd/resolve") || strings.Contains(lower, "systemd-resolved") {
		r.add("conflict", "resolved-stub",
			"systemd-resolved owns :53 stub — Mode C handoff only; default is Mode A (agent on :5353). See docs/resolved-coexistence.md")
	}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "nameserver") {
			r.add("info", "nameserver", line)
		}
	}
}

func (r *Report) checkUnit(unit string) {
	state, err := systemdActiveState(unit)
	short := strings.TrimSuffix(unit, ".service")
	if err != nil {
		r.add("info", "unit:"+short, fmt.Sprintf("D-Bus unit probe: %v", err))
		return
	}
	level := "info"
	if short == "systemd-resolved" && state == "active" {
		level = "conflict"
	}
	r.add(level, "unit:"+short, fmt.Sprintf("%s is %s", short, state))
}

func systemdActiveState(unit string) (string, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return "", err
	}
	mgr := conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")
	var path dbus.ObjectPath
	if err := mgr.Call("org.freedesktop.systemd1.Manager.GetUnit", 0, unit).Store(&path); err != nil {
		return "", err
	}
	u := conn.Object("org.freedesktop.systemd1", path)
	var v dbus.Variant
	if err := u.Call("org.freedesktop.DBus.Properties.Get", 0,
		"org.freedesktop.systemd1.Unit", "ActiveState").Store(&v); err != nil {
		return "", err
	}
	state, ok := v.Value().(string)
	if !ok {
		return "", fmt.Errorf("unexpected ActiveState type %T", v.Value())
	}
	return state, nil
}

func (r *Report) checkPort53() {
	addrs, err := udpListenersOnPort(53)
	if err != nil {
		r.add("warn", "port53", fmt.Sprintf("/proc/net/udp*: %v", err))
		return
	}
	if len(addrs) == 0 {
		r.add("info", "port53", "/proc/net/udp*: no UDP :53 sockets")
		return
	}
	for _, a := range addrs {
		r.add("conflict", "port53", "UDP listener "+a)
	}
}

// udpListenersOnPort reads /proc/net/udp and /proc/net/udp6 (no ss subprocess).
func udpListenersOnPort(port int) ([]string, error) {
	var out []string
	for _, path := range []string{"/proc/net/udp", "/proc/net/udp6"} {
		rows, err := parseProcNetUDP(path, port)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func parseProcNetUDP(path string, wantPort int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return out, sc.Err()
	}
	for sc.Scan() {
		addr, ok := localAddrFromProcUDPLine(sc.Text(), wantPort)
		if ok {
			out = append(out, addr)
		}
	}
	return out, sc.Err()
}

// localAddrFromProcUDPLine parses a /proc/net/udp{,6} data line.
// local_address is IP:port in hex; IPv4 words are little-endian.
func localAddrFromProcUDPLine(line string, wantPort int) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", false
	}
	local := fields[1] // e.g. 0100007F:0035 or 32-hex:port for v6
	hostPort := strings.Split(local, ":")
	if len(hostPort) != 2 {
		return "", false
	}
	portVal, err := strconv.ParseUint(hostPort[1], 16, 16)
	if err != nil || int(portVal) != wantPort {
		return "", false
	}
	ip, err := parseProcHexIP(hostPort[0])
	if err != nil {
		return "", false
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(wantPort)), true
}

func parseProcHexIP(h string) (net.IP, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return nil, err
	}
	switch len(b) {
	case 4:
		// IPv4 stored little-endian in /proc/net/udp
		return net.IPv4(b[3], b[2], b[1], b[0]), nil
	case 16:
		// IPv6: each 32-bit word little-endian
		out := make([]byte, 16)
		for i := 0; i < 4; i++ {
			word := binary.LittleEndian.Uint32(b[i*4 : i*4+4])
			binary.BigEndian.PutUint32(out[i*4:i*4+4], word)
		}
		return net.IP(out), nil
	default:
		return nil, fmt.Errorf("unexpected IP hex len %d", len(b))
	}
}

func (r *Report) checkCapabilities() {
	if os.Geteuid() == 0 {
		r.add("info", "priv", "running as root (CAP_NET_ADMIN / bind :53 available)")
		return
	}
	r.add("warn", "priv",
		"not root: doctor is read-only; live pins need CAP_NET_ADMIN; :53 needs CAP_NET_BIND_SERVICE — default agent uses :5353 + -pins auto/dry-run")
}

func (r *Report) checkPinPriv() {
	if os.Geteuid() == 0 {
		r.add("info", "pins", "root: -pins netlink may apply policy routes (owned tables 18000–18999 only)")
		return
	}
	r.add("info", "pins", "try: go run ./cmd/ctl pin check  and  go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface <iface>")
}

func (r *Report) checkCoexistence() {
	r.add("info", "coexistence",
		"Mode A default: agent on 127.0.0.1:5353, leave resolved alone — docs/resolved-coexistence.md")
}

func (r *Report) checkDoHWarn() {
	r.add("info", "doh",
		"MVP stance is warn-only: browsers/apps using DoH/DoT bypass the stub; no active DoH blocking yet")
}

func (r *Report) checkInterfaces() {
	ifaces, err := net.Interfaces()
	if err != nil {
		r.add("warn", "iface", err.Error())
		return
	}
	var up []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		up = append(up, iface.Name)
	}
	if len(up) == 0 {
		r.add("warn", "iface", "no non-loopback interfaces are up")
		return
	}
	r.add("info", "iface", "up interfaces: "+strings.Join(up, ", "))
}

// Format returns a human-readable report.
func (r Report) Format() string {
	var b strings.Builder
	b.WriteString("genesis doctor\n")
	b.WriteString("====================\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "[%s] %s: %s\n", strings.ToUpper(f.Level), f.Code, f.Message)
	}
	conflicts := 0
	for _, f := range r.Findings {
		if f.Level == "conflict" {
			conflicts++
		}
	}
	fmt.Fprintf(&b, "\n%d conflict(s) that will fight ownership of DNS/routing.\n", conflicts)
	return b.String()
}
