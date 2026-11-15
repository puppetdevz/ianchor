package fsx

import (
	"os"
	"testing"
)

// requireUnprivileged skips tests whose premise is that the current user is
// denied by file modes; root ignores those modes and would pass them.
func requireUnprivileged(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file-mode denial is not enforced for root")
	}
}
