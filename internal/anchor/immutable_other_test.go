//go:build !darwin

package anchor

import "testing"

// setImmutable has no portable equivalent outside darwin, so tests that need
// an immutable file skip instead of pretending the failure branch was covered.
func setImmutable(t *testing.T, path string) {
	t.Helper()
	t.Skipf("immutable flag is darwin-only: %s", path)
}
