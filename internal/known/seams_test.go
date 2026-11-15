package known

import (
	"os"
	"reflect"
	"testing"
)

// TestSeamsDefaultToRealCalls guards the base assumption of the coverage
// strategy: with no test replacing a seam, every seam is the real call.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	if got, want := reflect.ValueOf(userHome).Pointer(), reflect.ValueOf(os.UserHomeDir).Pointer(); got != want {
		t.Fatalf("userHome is not os.UserHomeDir")
	}
}
