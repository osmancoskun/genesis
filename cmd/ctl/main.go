// Command ctl is the path-director CLI (doctor, status, rules, pin).
package main

import (
	"fmt"
	"net"
	"os"
	"text/tabwriter"
	"time"

	"dnsredirector/internal/doctor"
	"dnsredirector/internal/pathpin"
	"dnsredirector/internal/rules"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "doctor":
		fmt.Print(doctor.Run().Format())
	case "rules":
		path := "configs/example.rules.yaml"
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		cfg, err := rules.LoadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rules: %v\n", err)
			os.Exit(1)
		}
		printRules(cfg)
	case "status":
		fmt.Println("status: agent not queried yet (no control socket in MVP)")
		fmt.Println("defaults: iface-down=fail_closed; ipv6=v4-first; doh=warn-only; listen=127.0.0.1:5353 (Mode A)")
		fmt.Println("coexistence: docs/resolved-coexistence.md")
	case "pin":
		if err := runPin(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "pin: %v\n", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runPin(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: ctl pin check | ctl pin dry-run --dst IP --iface IFACE")
	}
	switch args[0] {
	case "check":
		if err := pathpin.ProbeNetAdmin(); err != nil {
			fmt.Println("pin check: NOT ready for live netlink pins")
			fmt.Println(err.Error())
			fmt.Println("safe default: go run ./cmd/agent -pins dry-run …")
			return nil
		}
		fmt.Println("pin check: CAP_NET_ADMIN appears available for live netlink pins")
		fmt.Println("still prefer: ctl pin dry-run --dst 192.0.2.10 --iface <iface> before -pins netlink")
		fmt.Println("never point automated tests at live VPN ifaces you need")
		return nil
	case "dry-run":
		dstStr, iface := "192.0.2.10", ""
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--dst":
				i++
				if i < len(args) {
					dstStr = args[i]
				}
			case "--iface":
				i++
				if i < len(args) {
					iface = args[i]
				}
			}
		}
		if iface == "" {
			return fmt.Errorf("dry-run requires --iface (use a disposable test iface name in the plan text; this command does not change the kernel)")
		}
		ip := net.ParseIP(dstStr)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("invalid IPv4 --dst %q (hint: use TEST-NET 192.0.2.10)", dstStr)
		}
		plan, err := pathpin.BuildPlan(pathpin.Pin{
			Dst:       ip.To4(),
			Interface: iface,
			RuleName:  "ctl-dry-run",
			Expires:   time.Now().Add(time.Minute),
		}, 0, nil)
		if err != nil {
			return err
		}
		fmt.Println("no kernel changes — planned ops:")
		fmt.Println(plan.Describe())
		fmt.Printf("owned tables %d–%d; rule priority %d\n", pathpin.TableBase, pathpin.TableBase+pathpin.TableSpan-1, pathpin.RulePref)
		return nil
	default:
		return fmt.Errorf("unknown pin subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `path-director ctl

Usage:
  go run ./cmd/ctl doctor
  go run ./cmd/ctl rules [path]
  go run ./cmd/ctl status
  go run ./cmd/ctl pin check
  go run ./cmd/ctl pin dry-run --dst 192.0.2.10 --iface eno1
`)
}

func printRules(cfg *rules.Config) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tDNS\tIFACE\tPIN\tON_DOWN\tMATCH\n")
	for _, r := range cfg.Rules {
		pin := "true"
		if r.PinConnections != nil && !*r.PinConnections {
			pin = "false"
		}
		match := fmt.Sprintf("domains=%v ips=%v", r.Match.Domains, r.Match.IPs)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Name, r.DNS, r.Interface, pin, r.OnIfaceDown, match)
	}
	_ = w.Flush()
}
