//go:build !linux

package pathpin

// EnsureDefaultEgress is a no-op off Linux.
func EnsureDefaultEgress(string) error { return nil }

// ClearDefaultEgress is a no-op off Linux.
func ClearDefaultEgress() error { return nil }
