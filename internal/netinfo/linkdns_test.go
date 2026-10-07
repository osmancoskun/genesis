package netinfo

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestParseResolve1DNS(t *testing.T) {
	// RFC5737 TEST-NET addresses — not real host/corp IPs.
	raw := [][]any{
		{int32(2), []byte{192, 0, 2, 53}},
		{int32(2), []byte{198, 51, 100, 53}},
		{int32(10), make([]byte, 16)}, // skip v6 for now
	}
	got := parseResolve1DNS(raw)
	if len(got) != 2 || got[0] != "192.0.2.53" || got[1] != "198.51.100.53" {
		t.Fatalf("got %#v", got)
	}
	if ip := dnsRowToIP([]any{int32(2), []byte{1, 1, 1, 1}}); !ip.Equal(net.IPv4(1, 1, 1, 1)) {
		t.Fatalf("row ip %v", ip)
	}
}

func TestLinkDNSServersLive(t *testing.T) {
	iface := os.Getenv("GENESIS_LIVE_IFACE")
	name := os.Getenv("GENESIS_LIVE_NAME")
	if iface == "" || name == "" {
		t.Skip("set GENESIS_LIVE_IFACE and GENESIS_LIVE_NAME to exercise systemd-resolved")
	}
	got := LinkDNSServers(iface)
	if len(got) == 0 {
		t.Fatalf("expected link DNS on %s", iface)
	}
	t.Logf("link DNS: %v", got)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := ProbeDomainSmart(ctx, iface, "", "1.1.1.1", name)
	if !res.OK {
		t.Fatalf("smart probe: %+v", res)
	}
	t.Logf("smart probe ok via %s (%s): %s", res.Resolver, res.Source, res.Detail)
}
