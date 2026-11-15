//go:build darwin

package fsx

import (
	"testing"

	"golang.org/x/sys/unix"
)

// setImmutablePath marks a fixture immutable so chmod-based permission handling
// must fail; the flag is cleared when the test ends.
func setImmutablePath(t *testing.T, path string) {
	t.Helper()
	if err := unix.Chflags(path, unix.UF_IMMUTABLE); err != nil {
		t.Fatalf("chflags %s: %v", path, err)
	}
	t.Cleanup(func() { _ = unix.Chflags(path, 0) })
}
