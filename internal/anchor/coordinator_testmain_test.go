package anchor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

var originalCoordinatorDirectory = coordinatorDirectory

// Most anchor fault-injection tests exercise their own temporary trees. Keep
// them independent of whether the caller has run the real CLI previously.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "ianchor-anchor-coordinator-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	coordinatorDirectory = func() string { return filepath.Join(root, "absent") }
	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
