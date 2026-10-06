// Package dnsstub is a localhost DNS stub that matches rules and forwards via ifacedns.
package dnsstub

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"dnsredirector/internal/ifacedns"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
)

// Server is a minimal UDP DNS stub.
type Server struct {
	Addr     string
	Rules    *rules.Config
	Exchange ifacedns.ExchangeFunc
	Pins     *pathpin.Manager
	Logger   *log.Logger

	mu  sync.RWMutex
	udp *dns.Server
}

// SetRules atomically replaces the active rule pack (hot reload).
func (s *Server) SetRules(cfg *rules.Config) {
	s.mu.Lock()
	s.Rules = cfg
	s.mu.Unlock()
}

func (s *Server) rules() *rules.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Rules
}

// ListenAndServe starts the UDP stub (blocking).
func (s *Server) ListenAndServe() error {
	if s.Addr == "" {
		s.Addr = ListenAddr()
	}
	if s.Exchange == nil {
		s.Exchange = ifacedns.UDPExchange
	}
	if s.Logger == nil {
		s.Logger = log.Default()
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(".", s.handle)
	s.udp = &dns.Server{Addr: s.Addr, Net: "udp", Handler: mux}
	s.Logger.Printf("dns stub listening on udp %s", s.Addr)
	return s.udp.ListenAndServe()
}

// Shutdown stops the stub.
func (s *Server) Shutdown() error {
	if s.udp == nil {
		return nil
	}
	return s.udp.Shutdown()
}

func (s *Server) handle(w dns.ResponseWriter, req *dns.Msg) {
	if s.Logger == nil {
		s.Logger = log.Default()
	}
	msg := new(dns.Msg)
	msg.SetReply(req)
	msg.Authoritative = false
	msg.RecursionAvailable = true

	if len(req.Question) == 0 {
		msg.Rcode = dns.RcodeFormatError
		_ = w.WriteMsg(msg)
		return
	}
	q := req.Question[0]
	name := q.Name

	rule := s.rules().MatchDomain(name)
	if rule == nil {
		s.Logger.Printf("no rule for %s", name)
		msg.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(msg)
		return
	}

	iface := rule.Interface
	if ifaceDown(iface) {
		s.Logger.Printf("iface %s down for rule %s (fail closed)", iface, rule.Name)
		msg.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(msg)
		return
	}

	if rule.DNS == "" {
		s.Logger.Printf("rule %s has no dns server", rule.Name)
		msg.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(msg)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	raw, err := req.Pack()
	if err != nil {
		msg.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(msg)
		return
	}
	respRaw, err := s.Exchange(ctx, iface, rule.DNS, raw)
	if err != nil {
		s.Logger.Printf("upstream %s via %s: %v", rule.DNS, iface, err)
		msg.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(msg)
		return
	}
	resp := new(dns.Msg)
	if err := resp.Unpack(respRaw); err != nil {
		msg.Rcode = dns.RcodeServerFailure
		_ = w.WriteMsg(msg)
		return
	}
	resp.Id = req.Id

	s.pinAnswers(name, iface, rule, resp)

	if err := w.WriteMsg(resp); err != nil {
		s.Logger.Printf("write response: %v", err)
	}
}

// pinAnswers installs/refreshes v4 path pins for successful A answers (v4-first MVP).
func (s *Server) pinAnswers(name, iface string, rule *rules.Rule, resp *dns.Msg) {
	if s.Pins == nil || rule == nil || !rule.EffectivePin() {
		return
	}
	if iface == "" || iface == "auto" {
		s.Logger.Printf("skip pin for %s: interface is %q (name a real iface to pin)", name, iface)
		return
	}
	if resp.Rcode != dns.RcodeSuccess {
		return
	}
	for _, rr := range resp.Answer {
		a, ok := rr.(*dns.A)
		if !ok {
			continue
		}
		ttl := time.Duration(rr.Header().Ttl) * time.Second
		res, err := s.Pins.PinA(a.A, iface, rule.Name, ttl)
		if err != nil {
			s.Logger.Printf("pin %s via %s: %v", a.A, iface, err)
			continue
		}
		action := "install"
		if res.Refreshed {
			action = "refresh"
		}
		s.Logger.Printf("pin %s %s via %s rule=%s ttl=%s expires=%s",
			action, a.A, iface, rule.Name, ttl, res.Pin.Expires.Format(time.RFC3339))
	}
}

func ifaceDown(name string) bool {
	if name == "" || name == "auto" {
		return false
	}
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return true
	}
	return ifi.Flags&net.FlagUp == 0
}

// ListenAddr is the default non-privileged listen address.
// Uses 5553 so it does not collide with mDNS/Avahi on UDP 5353.
func ListenAddr() string {
	return "127.0.0.1:5553"
}

// FormatRuleSummary is a short debug string.
func FormatRuleSummary(cfg *rules.Config) string {
	if cfg == nil {
		return "no rules"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d rule(s)", len(cfg.Rules))
	for _, r := range cfg.Rules {
		fmt.Fprintf(&b, " [%s]", r.Name)
	}
	return b.String()
}
