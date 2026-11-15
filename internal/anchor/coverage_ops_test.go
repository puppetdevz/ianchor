package anchor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAllWithinRootErrors(t *testing.T) {
	requireUnprivileged(t)
	mkTree := func(t *testing.T) (dir, child string) {
		t.Helper()
		dir = filepath.Join(t.TempDir(), "tree")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		child = filepath.Join(dir, "child")
		if err := os.WriteFile(child, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir, child
	}
	t.Run("opened directory replaced", func(t *testing.T) {
		dir, _ := mkTree(t)
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 1 {
				return fakeInfoFor(t, 4242), nil
			}
			return realStat(file)
		})
		mustContain(t, removeAll(dir), "cleanup directory replaced")
	})
	t.Run("listing failure", func(t *testing.T) {
		dir, _ := mkTree(t)
		override(t, &readDirFile, func(*os.File, int) ([]os.DirEntry, error) { return nil, errSeam })
		mustContain(t, removeAll(dir), "seam failure")
	})
	t.Run("directory replaced after listing", func(t *testing.T) {
		dir, _ := mkTree(t)
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 4 {
				return fakeInfoFor(t, 4243), nil
			}
			return realStat(file)
		})
		mustContain(t, removeAll(dir), "cleanup directory replaced")
	})
	t.Run("parent stat failure", func(t *testing.T) {
		_, child := mkTree(t)
		override(t, &statFile, func(*os.File) (os.FileInfo, error) { return nil, errSeam })
		mustContain(t, removeAll(child), "seam failure")
	})
	t.Run("parent chmod failure", func(t *testing.T) {
		dir, child := mkTree(t)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		override(t, &chmodFile, func(*os.File, os.FileMode) error { return errSeam })
		mustContain(t, removeAll(child), "seam failure")
	})
	t.Run("parent replaced", func(t *testing.T) {
		dir, child := mkTree(t)
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		realStat := statFile
		stats := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			stats++
			if stats == 2 {
				return fakeInfoFor(t, 4244), nil
			}
			return realStat(file)
		})
		mustContain(t, removeAll(child), "cleanup parent replaced")
	})
}

func TestRemoveOwnedEntryStagingFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entry")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
	if err := removeOwnedEntry(path, info, false); err == nil {
		t.Fatal("expected staging failure")
	}
}

func TestMoveOwnedEntryStagingFailure(t *testing.T) {
	source := filepath.Join(t.TempDir(), "entry")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
	if err := moveOwnedEntry(source, filepath.Join(t.TempDir(), "dest"), info); err == nil {
		t.Fatal("expected staging failure")
	}
}
