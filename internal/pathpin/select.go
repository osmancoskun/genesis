package pathpin

import (
	"fmt"
	"strings"
)

// SelectApplier picks a pin backend.
//
//	noop     — memory only, no logs
//	dry-run  — print planned ip rule/route ops, no kernel changes
//	netlink  — live policy routes (requires CAP_NET_ADMIN); errors if unavailable
//	auto     — netlink when privileged, otherwise dry-run (non-destructive default)
func SelectApplier(mode string) (Applier, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "noop":
		return NoopApplier{}, "noop", nil
	case "dry-run", "dryrun":
		return NewDryRun(nil), "dry-run", nil
	case "netlink":
		if err := ProbeNetAdmin(); err != nil {
			return nil, "", err
		}
		return NewNetlink(), "netlink", nil
	case "auto", "":
		if err := ProbeNetAdmin(); err != nil {
			return NewDryRun(nil), "dry-run (auto: lacking CAP_NET_ADMIN)", nil
		}
		return NewNetlink(), "netlink (auto)", nil
	default:
		return nil, "", fmt.Errorf("pathpin: unknown -pins mode %q (want noop|dry-run|netlink|auto)", mode)
	}
}
