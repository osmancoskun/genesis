package pathpin

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// DryRunApplier prints planned kernel ops and never touches netlink.
type DryRunApplier struct {
	Out io.Writer
	mu  sync.Mutex
	log []string
}

// NewDryRun returns a dry-run applier writing to out (default stderr).
func NewDryRun(out io.Writer) *DryRunApplier {
	if out == nil {
		out = os.Stderr
	}
	return &DryRunApplier{Out: out}
}

func (d *DryRunApplier) Apply(p Pin) error {
	plan, err := BuildPlan(p, 0, nil)
	if err != nil {
		return err
	}
	line := "dry-run APPLY: " + plan.Describe()
	d.mu.Lock()
	d.log = append(d.log, line)
	d.mu.Unlock()
	fmt.Fprintln(d.Out, line)
	return nil
}

func (d *DryRunApplier) Remove(p Pin) error {
	if p.Dst == nil || p.Dst.To4() == nil {
		return fmt.Errorf("%w: IPv4 required", ErrRefused)
	}
	line := fmt.Sprintf("dry-run REMOVE: ip rule del to %s/32 priority %d; ip route del %s/32 table %d (iface %s)",
		p.Dst.To4(), RulePref, p.Dst.To4(), tableForIface(p.Interface), p.Interface)
	d.mu.Lock()
	d.log = append(d.log, line)
	d.mu.Unlock()
	fmt.Fprintln(d.Out, line)
	return nil
}

func (d *DryRunApplier) ApplyDefaultPath(iface string) error {
	plan, err := BuildDefaultPathPlan(iface, 0, nil, nil)
	if err != nil {
		return err
	}
	line := "dry-run APPLY default-path: " + plan.Describe()
	d.mu.Lock()
	d.log = append(d.log, line)
	d.mu.Unlock()
	fmt.Fprintln(d.Out, line)
	return nil
}

func (d *DryRunApplier) RemoveDefaultPath(iface string) error {
	line := fmt.Sprintf("dry-run REMOVE default-path: ip rule del priority %d (table %d); ip rule del priority %d (preserve main); ip route del default table %d  # iface=%s",
		DefaultPathPref, DefaultPathTable, MainPreservePref, DefaultPathTable, iface)
	d.mu.Lock()
	d.log = append(d.log, line)
	d.mu.Unlock()
	fmt.Fprintln(d.Out, line)
	return nil
}

// Log returns a copy of dry-run lines (for tests).
func (d *DryRunApplier) Log() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.log))
	copy(out, d.log)
	return out
}
