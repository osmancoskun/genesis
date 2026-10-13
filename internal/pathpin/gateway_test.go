package pathpin

import (
	"errors"
	"net"
	"testing"
)

func cidr(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestPickGatewayNeverDefaultSplit(t *testing.T) {
	const ifindex = 2
	routes := []pinRouteHint{
		{LinkIndex: ifindex, Dst: cidr("10.40.143.0/24"), Table: tableMain},
		{LinkIndex: ifindex, Dst: cidr("10.222.0.0/16"), Gw: net.ParseIP("10.40.143.1"), Table: tableMain},
		{LinkIndex: ifindex, Dst: cidr("10.252.0.0/16"), Gw: net.ParseIP("10.40.143.1"), Table: tableMain},
		{LinkIndex: ifindex, Dst: cidr("10.222.12.11/32"), Gw: net.ParseIP("10.40.143.1"), Table: tableMain},
		// leftover broken pin must not win
		{LinkIndex: ifindex, Dst: cidr("10.222.12.30/32"), Table: 18865},
		// default lives on another NIC
		{LinkIndex: 3, Dst: nil, Gw: net.ParseIP("193.140.85.97"), Table: tableMain},
	}
	gw, err := pickGatewayForPin(ifindex, net.ParseIP("10.222.12.30"), routes, true)
	if err != nil {
		t.Fatal(err)
	}
	if gw == nil || gw.String() != "10.40.143.1" {
		t.Fatalf("gw=%v want 10.40.143.1", gw)
	}
}

func TestPickGatewayOnLinkLAN(t *testing.T) {
	const ifindex = 2
	routes := []pinRouteHint{
		{LinkIndex: ifindex, Dst: cidr("10.40.143.0/24"), Table: tableMain},
		{LinkIndex: ifindex, Dst: cidr("10.222.0.0/16"), Gw: net.ParseIP("10.40.143.1"), Table: tableMain},
		{LinkIndex: ifindex, Dst: nil, Gw: net.ParseIP("10.40.143.1"), Table: tableMain},
	}
	gw, err := pickGatewayForPin(ifindex, net.ParseIP("10.40.143.50"), routes, true)
	if err != nil {
		t.Fatal(err)
	}
	if gw != nil {
		t.Fatalf("want on-link, got via %v", gw)
	}
}

func TestPickGatewayDefaultOnly(t *testing.T) {
	const ifindex = 3
	routes := []pinRouteHint{
		{LinkIndex: ifindex, Dst: cidr("193.140.85.96/27"), Table: tableMain},
		{LinkIndex: ifindex, Dst: nil, Gw: net.ParseIP("193.140.85.97"), Table: tableMain},
	}
	gw, err := pickGatewayForPin(ifindex, net.ParseIP("1.1.1.1"), routes, true)
	if err != nil {
		t.Fatal(err)
	}
	if gw == nil || gw.String() != "193.140.85.97" {
		t.Fatalf("gw=%v want 193.140.85.97", gw)
	}
}

func TestPickGatewayMissingFailClosed(t *testing.T) {
	const ifindex = 2
	routes := []pinRouteHint{
		{LinkIndex: ifindex, Dst: cidr("10.40.143.0/24"), Table: tableMain},
	}
	_, err := pickGatewayForPin(ifindex, net.ParseIP("10.222.12.30"), routes, true)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("want ErrRefused, got %v", err)
	}
}

func TestPickGatewayPointToPointAllowsOnLink(t *testing.T) {
	const ifindex = 4
	gw, err := pickGatewayForPin(ifindex, net.ParseIP("203.0.113.9"), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if gw != nil {
		t.Fatalf("want nil gw for p2p, got %v", gw)
	}
}

func TestPickGatewayMoreSpecificViaWins(t *testing.T) {
	const ifindex = 2
	routes := []pinRouteHint{
		{LinkIndex: ifindex, Dst: cidr("10.222.0.0/16"), Gw: net.ParseIP("10.40.143.1"), Table: tableMain},
		{LinkIndex: ifindex, Dst: cidr("10.222.12.0/24"), Gw: net.ParseIP("10.40.143.254"), Table: tableMain},
	}
	gw, err := pickGatewayForPin(ifindex, net.ParseIP("10.222.12.30"), routes, true)
	if err != nil {
		t.Fatal(err)
	}
	if gw == nil || gw.String() != "10.40.143.254" {
		t.Fatalf("gw=%v want 10.40.143.254", gw)
	}
}
