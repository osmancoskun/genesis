// Package ifacedns provides hooks for sending DNS queries bound to a named interface.
package ifacedns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ExchangeFunc forwards a DNS payload to server (host:port or host) via iface.
type ExchangeFunc func(ctx context.Context, iface, server string, payload []byte) ([]byte, error)

// ErrBindPrivilege means SO_BINDTODEVICE failed due to missing capabilities.
var ErrBindPrivilege = errors.New("ifacedns: SO_BINDTODEVICE needs CAP_NET_RAW (or root); use interface: auto for unbound demo DNS, or grant caps for real bind")

// UDPExchange sends a single UDP DNS query. When iface is non-empty and not "auto"
// it sets SO_BINDTODEVICE. Requires CAP_NET_RAW for bind-to-device; without it returns ErrBindPrivilege.
func UDPExchange(ctx context.Context, iface, server string, payload []byte) ([]byte, error) {
	if server == "" {
		return nil, fmt.Errorf("ifacedns: empty server")
	}
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "53")
	}
	dialTimeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d > 0 && d < dialTimeout {
			dialTimeout = d
		}
	}
	dialer := &net.Dialer{
		Timeout: dialTimeout,
	}
	bound := iface != "" && iface != "auto"
	if bound {
		ifaceName := iface
		dialer.Control = func(network, address string, c syscall.RawConn) error {
			var ctrlErr error
			if err := c.Control(func(fd uintptr) {
				ctrlErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, ifaceName)
			}); err != nil {
				return err
			}
			return wrapBindErr(ctrlErr, ifaceName)
		}
	}
	conn, err := dialer.DialContext(ctx, "udp", server)
	if err != nil {
		if bound {
			if w := wrapBindErr(err, iface); w != nil && errors.Is(w, ErrBindPrivilege) {
				return nil, w
			}
			if isPerm(err) {
				return nil, fmt.Errorf("%w: dial %s via %q: %v", ErrBindPrivilege, server, iface, err)
			}
		}
		return nil, fmt.Errorf("ifacedns: dial %s via %q: %w", server, iface, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func wrapBindErr(err error, iface string) error {
	if err == nil {
		return nil
	}
	if isPerm(err) {
		return fmt.Errorf("%w: iface %q: %v", ErrBindPrivilege, iface, err)
	}
	return err
}

func isPerm(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) || os.IsPermission(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied")
}
