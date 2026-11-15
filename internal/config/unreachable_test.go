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

	originalEval := evalSymlinks
	calls := 0
	evalSymlinks = func(path string) (string, error) {
		calls++
		if calls == 1 {
			return base, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { evalSymlinks = originalEval })
	if err := ensureWithin(base, "/"); err != nil {
		t.Fatalf("root walk err=%v", err)
	}
	if calls < 2 {
		t.Fatalf("eval calls=%d", calls)
	}
}
