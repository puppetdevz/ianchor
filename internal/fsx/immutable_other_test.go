//go:build !darwin

package fsx

import "testing"

// setImmutablePath has no portable equivalent outside darwin.
func setImmutablePath(t *testing.T, path string) {
	t.Helper()
	t.Skipf("immutable flag is darwin-only: %s", path)
}
