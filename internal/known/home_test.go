package known

import (
	"testing"
)

func TestGuessReturnsNilWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := Guess(".zshrc"); got != nil {
		t.Fatalf("Guess = %v, want nil when home is unavailable", *got)
	}
	if got := Guess("not-a-known-config"); got != nil {
		t.Fatalf("unknown name = %v, want nil", *got)
	}
}
