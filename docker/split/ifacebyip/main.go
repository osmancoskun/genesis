// Command ifacebyip prints the interface name that owns an IPv4 in CIDR/prefix form.
// Usage: ifacebyip 172.30.0.0/24
package main

import (
	"fmt"
	"net"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: ifacebyip CIDR\n")
		os.Exit(2)
	}
	_, netw, err := net.ParseCIDR(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "cidr: %v\n", err)
		os.Exit(1)
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ifaces: %v\n", err)
		os.Exit(1)
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
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
			if ip == nil || ip.To4() == nil {
				continue
			}
			if netw.Contains(ip.To4()) {
				fmt.Println(iface.Name)
				return
			}
		}
	}
	os.Exit(1)
}
