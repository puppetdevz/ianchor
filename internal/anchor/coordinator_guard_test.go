package anchor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCoordinatorPathGuard(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lock")
	override(t, &coordinatorDirectory, func() string { return dir })
	if err := validateCoordinatorPath(root, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateCoordinatorPath(outside, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCoordinatorConfig(outside); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, dir, filepath.Join(dir, "nested")} {
		if err := validateCoordinatorPath(path, true); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	alias := filepath.Join(outside, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateCoordinatorPath(alias, true); err == nil {
		t.Fatal("followed alias accepted")
	}
	if err := validateCoordinatorPath(alias, false); err != nil {
		t.Fatal(err)
	}
	insideFile := filepath.Join(dir, "db")
	if err := os.WriteFile(insideFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fileAlias := filepath.Join(outside, "db-alias")
	if err := os.Symlink(insideFile, fileAlias); err != nil {
		t.Fatal(err)
	}
	if err := validateCoordinatorPath(fileAlias, true); err == nil {
		t.Fatal("file alias accepted")
	}
	original := filepath.Join(dir, "entry")
	if err := validateOriginal(outside, &original); err == nil {
		t.Fatal("unsafe original accepted")
	}
	if _, err := Add(nil, AddOptions{Source: dir, ConfigDir: outside}); err == nil {
		t.Fatal("unsafe source accepted")
	}
	if _, err := Add(nil, AddOptions{Source: outside, ConfigDir: dir}); err == nil {
		t.Fatal("unsafe config accepted")
	}
	if _, err := Init(dir, "cloud"); err == nil {
		t.Fatal("unsafe init accepted")
	}
}

func TestCoordinatorPathGuardErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "lock")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	override(t, &coordinatorDirectory, func() string { return dir })
	failure := errors.New("guard stat failure")
	t.Run("lock stat", func(t *testing.T) {
		old := statPath
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == dir {
				return nil, failure
			}
			return old(path)
		})
		if err := validateCoordinatorPath(root, true); !errors.Is(err, failure) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("resolution", func(t *testing.T) {
		override(t, &evalSymlinksPath, func(string) (string, error) { return "", failure })
		if err := validateCoordinatorPath(root, true); !errors.Is(err, failure) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("candidate stat", func(t *testing.T) {
		old := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == root {
				return nil, failure
			}
			return old(path)
		})
		if err := validateCoordinatorPath(root, false); !errors.Is(err, failure) {
			t.Fatalf("got %v", err)
		}
	})
}
