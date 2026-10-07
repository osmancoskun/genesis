// Package netinfo lists host interfaces and probes DNS reachability per iface.
package netinfo

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/unix"

	"dnsredirector/internal/ifacedns"
)

// Iface is a host network interface snapshot.
type Iface struct {
	Name  string
	Up    bool
	Addrs []string
}

// ListIfaces returns non-loopback interfaces, sorted by name.
func ListIfaces() ([]Iface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Iface
	for _, ifi := range all {
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		var ipStrs []string
		for _, a := range addrs {
			ipStrs = append(ipStrs, a.String())
		}
		sort.Strings(ipStrs)
		out = append(out, Iface{
			Name:  ifi.Name,
			Up:    ifi.Flags&net.FlagUp != 0,
			Addrs: ipStrs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DefaultRouteIface best-effort: iface that owns the UDP dial source for 1.1.1.1:53.
func DefaultRouteIface() string {
	conn, err := net.DialTimeout("udp4", "1.1.1.1:53", 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	la, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || la.IP == nil {
		return ""
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifi := range ifaces {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.Equal(la.IP) {
				return ifi.Name
			}
		}
	}
	return ""
}

// ProbeResult is one iface attempt for a name or IP.
type ProbeResult struct {
	Iface    string
	OK       bool
	Detail   string
	Answers  []string
	Resolver string // upstream used for this attempt
	Source   string // link | fallback | explicit
}

// PerAttemptTimeout caps a single UDP DNS probe (keeps multi-iface UI snappy).
const PerAttemptTimeout = 2 * time.Second

// ProbeDomain asks resolver via SO_BINDTODEVICE on iface (or unbound if iface is "auto"/"").
func ProbeDomain(ctx context.Context, iface, resolver, name string) ProbeResult {
	res := ProbeResult{Iface: iface, Resolver: resolver}
	if resolver == "" {
		resolver = "1.1.1.1"
		res.Resolver = resolver
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	raw, err := m.Pack()
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	if iface == "" {
		iface = "auto"
	}
	pctx, cancel := context.WithTimeout(ctx, PerAttemptTimeout)
	defer cancel()
	respRaw, err := ifacedns.UDPExchange(pctx, iface, resolver, raw)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	resp := new(dns.Msg)
	if err := resp.Unpack(respRaw); err != nil {
		res.Detail = err.Error()
		return res
	}
	if resp.Rcode != dns.RcodeSuccess {
		res.Detail = dns.RcodeToString[resp.Rcode]
		return res
	}
	for _, an := range resp.Answer {
		if a, ok := an.(*dns.A); ok {
			res.Answers = append(res.Answers, a.A.String())
		}
	}
	if len(res.Answers) == 0 {
		res.Detail = "NOERROR but no A"
		return res
	}
	res.OK = true
	res.Detail = strings.Join(res.Answers, ", ")
	return res
}

// ProbeDomainSmart picks resolvers per iface: explicit override, else link DNS
// from systemd-resolved, else fallback (typically 1.1.1.1 / agent dns_upstream).
// Does not fall through from link NXDOMAIN to public DNS (avoids false negatives
// for split-horizon / corp names).
func ProbeDomainSmart(ctx context.Context, iface, explicit, fallback, name string) ProbeResult {
	if fallback == "" {
		fallback = "1.1.1.1"
	}
	var resolvers []string
	source := "fallback"
	switch {
	case strings.TrimSpace(explicit) != "":
		resolvers = []string{strings.TrimSpace(explicit)}
		source = "explicit"
	default:
		if link := LinkDNSServers(iface); len(link) > 0 {
			resolvers = link
			source = "link"
		} else {
			resolvers = []string{fallback}
			source = "fallback"
		}
	}
	var last ProbeResult
	for _, r := range resolvers {
		last = ProbeDomain(ctx, iface, r, name)
		last.Source = source
		if last.OK {
			return last
		}
		// Definitive DNS answer from this resolver — stop (don't pollute with public NXDOMAIN).
		if last.Detail == "NXDOMAIN" || last.Detail == "NOERROR but no A" {
			return last
		}
	}
	if last.Iface == "" {
		last.Iface = iface
		last.Detail = "no resolver tried"
		last.Source = source
	}
	return last
}

// ProbeIP checks UDP reachability to dst:53 bound to iface (SO_BINDTODEVICE when named).
func ProbeIP(ctx context.Context, iface string, dst net.IP) ProbeResult {
	res := ProbeResult{Iface: iface}
	if dst == nil || dst.To4() == nil {
		res.Detail = "need IPv4"
		return res
	}
	target := net.JoinHostPort(dst.String(), "53")
	d := &net.Dialer{Timeout: 2 * time.Second}
	bound := iface != "" && iface != "auto"
	if bound {
		ifaceName := iface
		d.Control = func(network, address string, c syscall.RawConn) error {
			var ctrlErr error
			if err := c.Control(func(fd uintptr) {
				ctrlErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, ifaceName)
			}); err != nil {
				return err
			}
			return ctrlErr
		}
	}
	conn, err := d.DialContext(ctx, "udp4", target)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	_ = conn.Close()
	res.OK = true
	res.Detail = "udp dial ok"
	return res
}

// ProbeDomainOnIfaces runs ProbeDomain for each iface name.
func ProbeDomainOnIfaces(ctx context.Context, ifaces []string, resolver, name string) []ProbeResult {
	out := make([]ProbeResult, 0, len(ifaces))
	for _, ifn := range ifaces {
		out = append(out, ProbeDomain(ctx, ifn, resolver, name))
	}
	return out
}

// ProbeIPOnIfaces runs ProbeIP for each iface name.
func ProbeIPOnIfaces(ctx context.Context, ifaces []string, dst net.IP) []ProbeResult {
	out := make([]ProbeResult, 0, len(ifaces))
	for _, ifn := range ifaces {
		out = append(out, ProbeIP(ctx, ifn, dst))
	}
	return out
}

// FormatOpts controls iface table columns.
type FormatOpts struct {
	// ConfigDefaultPath marks the iface chosen in YAML default_path (not kernel).
	ConfigDefaultPath string
	// IncludeLinkLocal keeps fe80:: / 169.254 addresses (default: hide).
	IncludeLinkLocal bool
}

// FormatIfaceList is FormatIfaceTable with default options.
func FormatIfaceList(ifaces []Iface) string {
	return FormatIfaceTable(ifaces, FormatOpts{})
}

// FormatIfaceTable is an aligned table: # | NAME | UP | KERNEL | CONFIG | IPv4 | IPv6.
// KERNEL = current kernel default-route iface. CONFIG = YAML default_path (if set).
// Link-local addresses are omitted unless IncludeLinkLocal. One row per iface.
func FormatIfaceTable(ifaces []Iface, opts FormatOpts) string {
	gw := DefaultRouteIface()
	cfg := strings.TrimSpace(opts.ConfigDefaultPath)
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "#\tNAME\tUP\tKERNEL\tCONFIG\tIPv4\tIPv6\n")
	fmt.Fprintf(w, "-\t----\t--\t------\t------\t----\t----\n")
	for i, ifi := range ifaces {
		kernel := "—"
		if ifi.Name == gw {
			kernel = "default-route"
		}
		config := "—"
		if cfg != "" && ifi.Name == cfg {
			config = "default-path"
		}
		up := "no"
		if ifi.Up {
			up = "yes"
		}
		v4, v6 := splitAddrs(ifi.Addrs)
		if !opts.IncludeLinkLocal {
			v4 = filterLinkLocal(v4)
			v6 = filterLinkLocal(v6)
		}
		a4, a6 := "—", "—"
		if len(v4) > 0 {
			a4 = strings.Join(v4, ", ")
		}
		if len(v6) > 0 {
			a6 = strings.Join(v6, ", ")
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", i+1, ifi.Name, up, kernel, config, a4, a6)
	}
	_ = w.Flush()
	if gw != "" || cfg != "" {
		fmt.Fprintf(&b, "\nKERNEL = live default-route (%s). CONFIG = YAML default_path (%s).\n",
			orDash(gw), orDash(cfg))
		fmt.Fprintf(&b, "Changing CONFIG does not move KERNEL until agent applies with mode=netlink.\n")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func filterLinkLocal(addrs []string) []string {
	var out []string
	for _, a := range addrs {
		ipStr := a
		if i := strings.IndexByte(a, '/'); i >= 0 {
			ipStr = a[:i]
		}
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		out = append(out, a)
	}
	return out
}

// FormatProbeTable prints probe results as an aligned table.
func FormatProbeTable(results []ProbeResult) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "IFACE\tSTATUS\tDETAIL\n")
	fmt.Fprintf(w, "-----\t------\t------\n")
	for _, r := range results {
		status := "FAIL"
		if r.OK {
			status = "OK"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Iface, status, r.Detail)
	}
	_ = w.Flush()
	return b.String()
}

func splitAddrs(addrs []string) (v4, v6 []string) {
	for _, a := range addrs {
		ipStr := a
		if i := strings.IndexByte(a, '/'); i >= 0 {
			ipStr = a[:i]
		}
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, a)
		} else {
			v6 = append(v6, a)
		}
	}
	return v4, v6
}

// UpNames returns names of interfaces that are up (same order as ListIfaces).
func UpNames(ifaces []Iface) []string {
	var out []string
	for _, ifi := range ifaces {
		if ifi.Up {
			out = append(out, ifi.Name)
		}
	}
	return out
}

// ResolveIface picks by 1-based table index or by name.
func ResolveIface(input string, ifaces []Iface) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	var n int
	if _, err := fmt.Sscanf(input, "%d", &n); err == nil && n >= 1 && n <= len(ifaces) {
		return ifaces[n-1].Name
	}
	return input
}
