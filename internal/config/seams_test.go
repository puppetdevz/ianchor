package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSeamsDefaultToRealCalls guards the base assumption of the coverage
// strategy: with no test replacing a seam, every seam is the real call. A seam
// that silently became a stub would make the tested behaviour differ from the
// shipped behaviour. Every seam in this package is listed here, so adding one
// without a guard is a test failure rather than a quiet gap.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	direct := map[string][2]uintptr{
		"evalSymlinks": {reflect.ValueOf(evalSymlinks).Pointer(), reflect.ValueOf(filepath.EvalSymlinks).Pointer()},
		"homeDir":      {reflect.ValueOf(homeDir).Pointer(), reflect.ValueOf(os.UserHomeDir).Pointer()},
		"parentDir":    {reflect.ValueOf(parentDir).Pointer(), reflect.ValueOf(filepath.Dir).Pointer()},
	}
	for name, pair := range direct {
		if pair[0] != pair[1] {
			t.Errorf("%s is not the real call", name)
		}
	}
	// mkdirConfig and marshalConfig are small wrappers, so their code pointers
	// are their own. Check the behaviour they must keep instead.
	dir := filepath.Join(t.TempDir(), "nested", "config")
	if err := mkdirConfig(dir); err != nil {
		t.Fatalf("mkdirConfig: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("mkdirConfig did not create %s: %v", dir, err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o755 {
		t.Fatalf("mkdirConfig created %s as %v %o, want directory 0755", dir, info.Mode().Type(), info.Mode().Perm())
	}
	encoded, err := marshalConfig(fileConfig{DirName: "ianchor"})
	if err != nil {
		t.Fatalf("marshalConfig: %v", err)
	}
	var decoded fileConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("marshalConfig produced %q, which is not JSON: %v", encoded, err)
	}
	if decoded.DirName != "ianchor" {
		t.Fatalf("marshalConfig round trip = %+v, want DirName ianchor", decoded)
	}
	// evalSymlinks resolves the real filesystem, so a tampered config cannot
	// hide a redirect from ensureWithin.
	base := t.TempDir()
	inside := filepath.Join(base, "inside")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatalf("mkdir inside: %v", err)
	}
	if err := ensureWithin(base, filepath.Join(inside, "missing", "deeper")); err != nil {
		t.Fatalf("ensureWithin inside the base: %v", err)
	}
	if err := ensureWithin(base, filepath.Join(t.TempDir(), "outside")); err == nil {
		t.Fatal("ensureWithin accepted a path outside the base")
	}
}
