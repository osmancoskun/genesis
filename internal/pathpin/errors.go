package pathpin

import (
	"errors"
	"fmt"
)

// ErrInsufficientPriv means the process cannot change policy routing (need CAP_NET_ADMIN).
var ErrInsufficientPriv = errors.New("pathpin: insufficient privilege for policy routing (need CAP_NET_ADMIN / root)")

// ErrRefused is returned when a pin target is unsafe or unsupported for kernel apply.
var ErrRefused = errors.New("pathpin: pin refused")

// DoctorHint is appended to privilege errors so operators know the next command.
const DoctorHint = `run: go run ./cmd/ctl doctor
see also: go run ./cmd/ctl pin check`

// FormatPrivError wraps a kernel/permission error with operator guidance.
func FormatPrivError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %v — %s", ErrInsufficientPriv, err, DoctorHint)
}
