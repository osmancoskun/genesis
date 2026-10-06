package ifacedns

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWrapBindErrPrivilege(t *testing.T) {
	err := wrapBindErr(unix.EPERM, "eno1")
	if !errors.Is(err, ErrBindPrivilege) {
		t.Fatalf("got %v", err)
	}
}

func TestWrapBindErrNil(t *testing.T) {
	if wrapBindErr(nil, "eno1") != nil {
		t.Fatal("expected nil")
	}
}
