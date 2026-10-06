//go:build !linux

package pathpin

import "fmt"

// NetlinkApplier is unavailable off Linux.
type NetlinkApplier struct{}

func NewNetlink() *NetlinkApplier { return &NetlinkApplier{} }

func ProbeNetAdmin() error {
	return FormatPrivError(fmt.Errorf("netlink pins require Linux"))
}

func (a *NetlinkApplier) Apply(Pin) error {
	return FormatPrivError(fmt.Errorf("netlink pins require Linux"))
}

func (a *NetlinkApplier) Remove(Pin) error {
	return FormatPrivError(fmt.Errorf("netlink pins require Linux"))
}
