package doctor

import (
	"net"
	"testing"
)

func TestLocalAddrFromProcUDPLine(t *testing.T) {
	// 127.0.0.1:53 — 0100007F:0035
	addr, ok := localAddrFromProcUDPLine("   0: 0100007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 12345 2 ffff", 53)
	if !ok {
		t.Fatal("expected match")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port != "53" || host != "127.0.0.1" {
		t.Fatalf("got %q", addr)
	}

	// 5353 must not match wantPort 53
	if _, ok := localAddrFromProcUDPLine("   1: 00000000:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 1 2 ffff", 53); ok {
		t.Fatal("5353 should not match port 53")
	}
}

func TestParseProcHexIP(t *testing.T) {
	ip, err := parseProcHexIP("0100007F")
	if err != nil || !ip.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("got %v %v", ip, err)
	}
}
