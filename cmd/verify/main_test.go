package main

import "testing"

func TestSplitPackagesDefault(t *testing.T) {
	got := splitPackages("")
	if len(got) != 1 || got[0] != "./..." {
		t.Fatalf("splitPackages(\"\") = %v, want [./...]", got)
	}
}

func TestSplitPackagesArgs(t *testing.T) {
	got := splitPackages("./cmd/verify ./internal/...")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != "./cmd/verify" || got[1] != "./internal/..." {
		t.Fatalf("got %v", got)
	}
}
