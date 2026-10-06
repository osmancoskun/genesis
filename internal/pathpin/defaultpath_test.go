package pathpin

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildDefaultPathPlan(t *testing.T) {
	p, err := BuildDefaultPathPlan("wg0", 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Table != DefaultPathTable || p.Priority != DefaultPathPref {
		t.Fatalf("plan=%+v", p)
	}
	desc := p.Describe()
	if !strings.Contains(desc, "default") || !strings.Contains(desc, "wg0") || !strings.Contains(desc, "18990") {
		t.Fatalf("describe: %s", desc)
	}
}

func TestBuildDefaultPathRefusesAuto(t *testing.T) {
	if _, err := BuildDefaultPathPlan("auto", 0, nil, nil); err == nil {
		t.Fatal("expected refuse")
	}
}

func TestDryRunDefaultPath(t *testing.T) {
	var buf bytes.Buffer
	d := NewDryRun(&buf)
	if err := d.ApplyDefaultPath("wg0"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveDefaultPath("wg0"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "dry-run APPLY default-path:") || !strings.Contains(out, "dry-run REMOVE default-path:") {
		t.Fatalf("output: %s", out)
	}
	if c, ok := AsDefaultPath(d); !ok || c == nil {
		t.Fatal("DryRun should implement DefaultPathController")
	}
}
