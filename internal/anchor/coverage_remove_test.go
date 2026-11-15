package anchor

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func removeFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	local := f.local(".zshrc", "local")
	if _, err := f.add(local, ".zshrc", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	return f, local
}

func TestRemoveErrors(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		f, _ := removeFixture(t)
		f.db.Close()
		if _, err := Remove(f.db, f.configDir, ".zshrc"); err == nil {
			t.Fatal("expected database failure")
		}
	})
	t.Run("backup root", func(t *testing.T) {
		f, _ := removeFixture(t)
		override(t, &mkdirPath, func(string, os.FileMode) error { return errSeam })
		override(t, &rootMkdir, func(*os.Root, string, os.FileMode) error { return errSeam })
		if _, err := Remove(f.db, f.configDir, ".zshrc"); err == nil {
			t.Fatal("expected backup root failure")
		}
	})
	t.Run("cloud lstat", func(t *testing.T) {
		f, _ := removeFixture(t)
		cloud := filepath.Join(f.configDir, ".zshrc")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == cloud {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, removeErr(t, f), errSeam.Error())
		assertRemoveIntact(t, f)
	})
	t.Run("staging", func(t *testing.T) {
		f, _ := removeFixture(t)
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		mustContain(t, removeErr(t, f), errSeam.Error())
		assertRemoveIntact(t, f)
	})
	t.Run("cross volume staging", func(t *testing.T) {
		f, _ := removeFixture(t)
		override(t, &renameAt, func(int, string, int, string) error {
			return &os.LinkError{Op: "rename", Err: syscall.EXDEV}
		})
		realStaging := stagingPath
		calls := 0
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			calls++
			if calls == 2 {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, removeErr(t, f), errSeam.Error())
		assertRemoveIntact(t, f)
	})
	t.Run("stage lstat", func(t *testing.T) {
		f, _ := removeFixture(t)
		realLstat := rootLstat
		calls := 0
		override(t, &rootLstat, func(root *os.Root, path string) (os.FileInfo, error) {
			calls++
			if calls == 4 {
				return nil, errSeam
			}
			return realLstat(root, path)
		})
		mustContain(t, removeErr(t, f), errSeam.Error())
	})
	t.Run("stage replaced", func(t *testing.T) {
		f, _ := removeFixture(t)
		realLstat := rootLstat
		calls := 0
		override(t, &rootLstat, func(root *os.Root, path string) (os.FileInfo, error) {
			calls++
			if calls == 4 {
				return fakeInfoFor(t, 4242), nil
			}
			return realLstat(root, path)
		})
		mustContain(t, removeErr(t, f), "staged entry replaced")
		assertRemoveIntact(t, f)
	})
	t.Run("cross volume cleanup", func(t *testing.T) {
		f, _ := removeFixture(t)
		override(t, &renameAt, func(int, string, int, string) error {
			return &os.LinkError{Op: "rename", Err: syscall.EXDEV}
		})
		setHook(t, func(op, _ string) error {
			if op == "removeall" {
				return errSeam
			}
			return nil
		})
		if _, err := Remove(f.db, f.configDir, ".zshrc"); err == nil {
			t.Fatal("expected cleanup failure")
		}
	})
	t.Run("symlink removal", func(t *testing.T) {
		f, local := removeFixture(t)
		setHook(t, func(op, path string) error {
			if op == "remove" && path == local {
				return errSeam
			}
			return nil
		})
		mustContain(t, removeErr(t, f), errSeam.Error())
		assertRemoveIntact(t, f)
	})
	t.Run("database delete", func(t *testing.T) {
		f, _ := removeFixture(t)
		setHook(t, func(op, _ string) error {
			if op == "db.delete" {
				return errSeam
			}
			return nil
		})
		mustContain(t, removeErr(t, f), errSeam.Error())
	})
	t.Run("backup cleanup", func(t *testing.T) {
		f, _ := removeFixture(t)
		setHook(t, func(op, _ string) error {
			if op == "removeall" {
				return errSeam
			}
			return nil
		})
		if _, err := Remove(f.db, f.configDir, ".zshrc"); err == nil {
			t.Fatal("expected cleanup failure")
		}
	})
	t.Run("created backup cleanup", func(t *testing.T) {
		f, _ := removeFixture(t)
		if err := os.RemoveAll(filepath.Join(f.configDir, bakDirName)); err != nil {
			t.Fatal(err)
		}
		if _, err := Remove(f.db, f.configDir, ".zshrc"); err != nil {
			t.Fatalf("remove: %v", err)
		}
	})
}

func removeErr(t *testing.T, f *fixture) error {
	t.Helper()
	_, err := Remove(f.db, f.configDir, ".zshrc")
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

// assertRemoveIntact requires a failed remove to leave the tracked entry fully
// usable: cloud content, the local symlink into it, and a linked DB row.
func assertRemoveIntact(t *testing.T, f *fixture) {
	t.Helper()
	cloud := filepath.Join(f.configDir, ".zshrc")
	assertFileContent(t, cloud, "local")
	entry := f.mustEntry(".zshrc")
	if entry.OriginalPath == nil {
		t.Fatal("original path cleared by a failed remove")
	}
	assertSymlink(t, *entry.OriginalPath, cloud)
	if entry.Status != "linked" {
		t.Fatalf("status=%s", entry.Status)
	}
	assertNoStagingResidue(t, f.configDir)
	assertNoStagingResidue(t, filepath.Dir(*entry.OriginalPath))
}
