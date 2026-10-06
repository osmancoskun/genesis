package dnsstub

import (
	"bytes"
	"context"
	"log"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"

	"dnsredirector/internal/ifacedns"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
)

func TestHandleMatchAndPin(t *testing.T) {
	cfg, err := rules.Parse([]byte(`
version: 1
defaults:
  dns: 9.9.9.9
  pin_connections: true
  on_iface_down: fail_closed
rules:
  - name: lab
    match:
      domains: ["lab.test"]
    dns: 9.9.9.9
    interface: lo
`))
	if err != nil {
		t.Fatal(err)
	}

	pins := pathpin.New(nil)
	srv := &Server{Rules: cfg, Pins: pins, Exchange: fakeA("203.0.113.50", 60)}

	req := new(dns.Msg)
	req.SetQuestion("lab.test.", dns.TypeA)
	rw := &memRW{}
	srv.handle(rw, req)
	if rw.msg == nil || rw.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("bad response: %#v", rw.msg)
	}
	if len(pins.List()) != 1 {
		t.Fatalf("want pin, got %d", len(pins.List()))
	}
}

func TestAnswerToPinDryRunLifecycle(t *testing.T) {
	// Demo of product loop: successful A answer → dry-run pin install → refresh → TTL expiry.
	cfg, err := rules.Parse([]byte(`
version: 1
rules:
  - name: demo
    match:
      domains: ["pin.demo.test"]
    dns: 9.9.9.9
    interface: lo
    pin_connections: true
    on_iface_down: fail_closed
`))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	dry := pathpin.NewDryRun(&buf)
	pins := pathpin.New(dry)
	fixed := time.Unix(1_700_000_000, 0)
	pins.SetClock(func() time.Time { return fixed })

	var logBuf bytes.Buffer
	srv := &Server{
		Rules:    cfg,
		Pins:     pins,
		Exchange: fakeA("192.0.2.77", 30),
		Logger:   log.New(&logBuf, "", 0),
	}

	query := func() {
		req := new(dns.Msg)
		req.SetQuestion("pin.demo.test.", dns.TypeA)
		rw := &memRW{}
		srv.handle(rw, req)
		if rw.msg == nil || rw.msg.Rcode != dns.RcodeSuccess {
			t.Fatalf("query failed: %#v", rw.msg)
		}
	}

	query()
	out := buf.String()
	if !strings.Contains(out, "dry-run APPLY:") || !strings.Contains(out, "192.0.2.77/32") {
		t.Fatalf("expected dry-run APPLY, got %q", out)
	}
	if !strings.Contains(logBuf.String(), "pin install") {
		t.Fatalf("expected install log, got %q", logBuf.String())
	}
	p, ok := pins.Lookup(net.ParseIP("192.0.2.77"))
	if !ok || !p.Expires.Equal(fixed.Add(30*time.Second)) {
		t.Fatalf("pin %#v ok=%v", p, ok)
	}

	srv.Exchange = fakeA("192.0.2.77", 120)
	fixed2 := fixed.Add(10 * time.Second)
	pins.SetClock(func() time.Time { return fixed2 })
	logBuf.Reset()
	query()
	if !strings.Contains(logBuf.String(), "pin refresh") {
		t.Fatalf("expected refresh log, got %q", logBuf.String())
	}
	p, ok = pins.Lookup(net.ParseIP("192.0.2.77"))
	if !ok || !p.Expires.Equal(fixed2.Add(120*time.Second)) {
		t.Fatalf("refreshed pin %#v", p)
	}

	pins.SetClock(func() time.Time { return fixed2.Add(121 * time.Second) })
	pins.ExpireNow()
	if len(pins.List()) != 0 {
		t.Fatalf("want expired, got %d", len(pins.List()))
	}
	if !strings.Contains(buf.String(), "dry-run REMOVE:") {
		t.Fatalf("expected dry-run REMOVE, got %q", buf.String())
	}
}

func TestNoPinOnNXDOMAIN(t *testing.T) {
	cfg, err := rules.Parse([]byte(`
version: 1
rules:
  - name: demo
    match:
      domains: ["missing.test"]
    dns: 9.9.9.9
    interface: lo
`))
	if err != nil {
		t.Fatal(err)
	}
	pins := pathpin.New(nil)
	srv := &Server{Rules: cfg, Pins: pins, Exchange: func(ctx context.Context, iface, server string, payload []byte) ([]byte, error) {
		req := new(dns.Msg)
		_ = req.Unpack(payload)
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Rcode = dns.RcodeNameError
		return resp.Pack()
	}}
	req := new(dns.Msg)
	req.SetQuestion("missing.test.", dns.TypeA)
	rw := &memRW{}
	srv.handle(rw, req)
	if len(pins.List()) != 0 {
		t.Fatalf("should not pin on NXDOMAIN")
	}
}

func TestFailClosedMissingIface(t *testing.T) {
	cfg, err := rules.Parse([]byte(`
version: 1
rules:
  - name: vpn
    match:
      domains: ["secret.test"]
    dns: 1.1.1.1
    interface: vpn-does-not-exist-xyz
    on_iface_down: fail_closed
`))
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Rules: cfg, Exchange: func(context.Context, string, string, []byte) ([]byte, error) {
		t.Fatal("should not exchange")
		return nil, nil
	}}
	req := new(dns.Msg)
	req.SetQuestion("secret.test.", dns.TypeA)
	rw := &memRW{}
	srv.handle(rw, req)
	if rw.msg == nil || rw.msg.Rcode != dns.RcodeServerFailure {
		t.Fatalf("want SERVFAIL, got %#v", rw.msg)
	}
}

func fakeA(ip string, ttl uint32) ifacedns.ExchangeFunc {
	return func(ctx context.Context, iface, server string, payload []byte) ([]byte, error) {
		req := new(dns.Msg)
		if err := req.Unpack(payload); err != nil {
			return nil, err
		}
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = append(resp.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: ttl},
			A:   net.ParseIP(ip).To4(),
		})
		return resp.Pack()
	}
}

type memRW struct {
	msg *dns.Msg
}

func (m *memRW) LocalAddr() net.Addr         { return &net.UDPAddr{} }
func (m *memRW) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (m *memRW) WriteMsg(msg *dns.Msg) error { m.msg = msg; return nil }
func (m *memRW) Write([]byte) (int, error)   { return 0, nil }
func (m *memRW) Close() error                { return nil }
func (m *memRW) TsigStatus() error           { return nil }
func (m *memRW) TsigTimersOnly(bool)         {}
func (m *memRW) Hijack()                     {}
