package textwidth

import "testing"

func TestRuneZeroWidth(t *testing.T) {
	if got := Rune(0); got != 0 {
		t.Fatalf("NUL width = %d, want 0", got)
	}
}
