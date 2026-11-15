package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveWriteAndSymlinkResolutionFailures(t *testing.T) {
	home := t.TempDir()
	original := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = original })
	configDir := filepath.Join(home, ".config")
	if err := os.MkdirAll(filepath.Join(configDir, "ianchor.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save("work"); err == nil {
		t.Fatal("Save wrote through a directory occupying the config file")
	}

	base := t.TempDir()
	loop := filepath.Join(base, "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatal(err)
	}
	err := ensureWithin(base, filepath.Join(loop, "child"))
	if err == nil || !strings.Contains(err.Error(), "too many links") {
		t.Fatalf("symlink loop err=%v", err)
	}

	// Every probe above "/" fails on purpose so the walk must stop at the
	// root, the only point where parentDir(probe) == probe. The call sequence
	// is asserted exactly: a change that keeps walking past "/" would either
	// loop forever or silently accept an out-of-tree target.
	originalEval := evalSymlinks
	var probed []string
	evalSymlinks = func(path string) (string, error) {
		probed = append(probed, path)
		if path == base {
			return base, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { evalSymlinks = originalEval })
	if err := ensureWithin(base, "/"); err != nil {
		t.Fatalf("root walk err=%v", err)
	}
	want := []string{base, "/"}
	if strings.Join(probed, ",") != strings.Join(want, ",") {
		t.Fatalf("probed %v, want %v", probed, want)
	}
}
