package main

import (
	"os"
	"reflect"
	"testing"
)

// TestExitSeamIsRealOsExit guards the coverage seam in main: the wrapper must
// still terminate the process through os.Exit in production.
func TestExitSeamIsRealOsExit(t *testing.T) {
	if got, want := reflect.ValueOf(exit).Pointer(), reflect.ValueOf(os.Exit).Pointer(); got != want {
		t.Fatal("exit is not os.Exit")
	}
}
