// Command verifycheck runs the split-demo DNS assertions (no dig/apk needed).
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
)

func main() {
	agent := "172.30.0.20:5353"
	lanDNS := "172.30.0.11:53"
	warpDNS := "172.30.1.11:53"

	waitUDP(agent)
	waitUDP(lanDNS)
	waitUDP(warpDNS)

	fail := 0
	check := func(label, got, want string) {
		if got == want {
			fmt.Printf("OK  %s => %s\n", label, got)
			return
		}
		fmt.Fprintf(os.Stderr, "FAIL %s => got %q want %q\n", label, got, want)
		fail = 1
	}

	fmt.Println("== direct upstreams ==")
	check("lan www.example A", lookupA(lanDNS, "www.example.test."), "10.30.0.2")
	check("warp www.discord A", lookupA(warpDNS, "www.discord.test."), "10.30.1.9")
	check("warp TXT", lookupTXT(warpDNS, "www.discord.test."), "hello-from-warp-bypass")

	fmt.Println("== cross-upstream empty ==")
	if lookupA(lanDNS, "www.discord.test.") == "" && lookupA(warpDNS, "www.example.test.") == "" {
		fmt.Println("OK  lan DNS does not answer discord; warp DNS does not answer example")
	} else {
		fmt.Fprintln(os.Stderr, "FAIL unexpected cross answers")
		fail = 1
	}

	fmt.Println("== via agent (split steer) ==")
	check("agent→discord A", lookupA(agent, "www.discord.test."), "10.30.1.9")
	check("agent→discord TXT", lookupTXT(agent, "www.discord.test."), "hello-from-warp-bypass")
	check("agent→example A", lookupA(agent, "www.example.test."), "10.30.0.2")
	check("agent→example TXT", lookupTXT(agent, "www.example.test."), "hello-from-lan-example")

	if fail != 0 {
		fmt.Fprintln(os.Stderr, "SPLIT DEMO FAILED")
		os.Exit(1)
	}
	fmt.Println("SPLIT DEMO OK — discord.test via warp-like DNS; example.test via lan; host untouched")
}

func waitUDP(addr string) {
	deadline := time.Now().Add(20 * time.Second)
	c := new(dns.Client)
	c.Timeout = time.Second
	for time.Now().Before(deadline) {
		m := new(dns.Msg)
		m.SetQuestion("nosuch.test.", dns.TypeA)
		if _, _, err := c.Exchange(m, addr); err == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "timeout waiting for %s\n", addr)
	os.Exit(1)
}

func lookupA(server, name string) string {
	c := new(dns.Client)
	c.Timeout = 3 * time.Second
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	r, _, err := c.Exchange(m, server)
	if err != nil || r == nil {
		return ""
	}
	for _, an := range r.Answer {
		if a, ok := an.(*dns.A); ok {
			return a.A.String()
		}
	}
	return ""
}

func lookupTXT(server, name string) string {
	c := new(dns.Client)
	c.Timeout = 3 * time.Second
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeTXT)
	r, _, err := c.Exchange(m, server)
	if err != nil || r == nil {
		return ""
	}
	for _, an := range r.Answer {
		if t, ok := an.(*dns.TXT); ok && len(t.Txt) > 0 {
			return strings.Join(t.Txt, "")
		}
	}
	return ""
}
