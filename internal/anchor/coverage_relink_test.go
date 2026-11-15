package anchor

import (
	"os"
	"path/filepath"
	"testing"
)

func relinkFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	local := f.local(".zshrc", "local")
	if _, err := f.add(local, ".zshrc", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	return f, local
}

func TestRelinkEntryErrors(t *testing.T) {
	t.Run("database", func(t *testing.T) {
		f := newFixture(t)
		f.db.Close()
		if err := Relink(f.db, f.configDir, ".zshrc"); err == nil {
			t.Fatal("expected database failure")
		}
	})
	t.Run("invalid name", func(t *testing.T) {
		f := newFixture(t)
		original := f.local(".zshrc", "local")
		if err := f.db.AddEntry("bad/name", &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		if err := Relink(f.db, f.configDir, "bad/name"); err == nil {
			t.Fatal("expected name validation failure")
		}
	})
	t.Run("missing cloud copy", func(t *testing.T) {
		f, _ := relinkFixture(t)
		if err := os.Remove(filepath.Join(f.configDir, ".zshrc")); err != nil {
			t.Fatal(err)
		}
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), `".zshrc" not found in iCloud directory.`)
		if entry := f.mustEntry(".zshrc"); entry.Status != "linked" {
			t.Fatalf("status=%s", entry.Status)
		}
	})
	t.Run("unreadable link", func(t *testing.T) {
		f, local := relinkFixture(t)
		if err := os.Remove(local); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.configDir, ".zshrc"), local); err != nil {
			t.Fatal(err)
		}
		override(t, &readlinkPath, func(string) (string, error) { return "", errSeam })
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
		// A refused relink must not disturb the existing link or the row.
		assertSymlink(t, local, filepath.Join(f.configDir, ".zshrc"))
		if entry := f.mustEntry(".zshrc"); entry.Status != "linked" {
			t.Fatalf("status=%s", entry.Status)
		}
		assertNoStagingResidue(t, filepath.Dir(local))
	})
}

func prepareDir(t *testing.T) (*fixture, string, string) {
	t.Helper()
	f, local := relinkFixture(t)
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(local, 0o755); err != nil {
		t.Fatal(err)
	}
	return f, local, local + ".bak"
}

func TestRelinkBackupErrors(t *testing.T) {
	prepare := func(t *testing.T) (*fixture, string, string) {
		t.Helper()
		f, local := relinkFixture(t)
		if err := os.Remove(local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, local, "replacement")
		backup := local + ".bak"
		writeFile(t, backup, "old backup")
		return f, local, backup
	}
	t.Run("source lstat", func(t *testing.T) {
		f, local, _ := prepare(t)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
		_ = local
	})
	t.Run("backup lstat", func(t *testing.T) {
		f, _, backup := prepare(t)
		realLstat := lstatPath
		seen := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == backup {
				seen++
				if seen == 2 {
					return nil, errSeam
				}
			}
			return realLstat(path)
		})
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
	})
	t.Run("backup listing", func(t *testing.T) {
		f, local, backup := prepareDir(t)
		if err := os.Mkdir(backup, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = local
		override(t, &readDirPath, func(string) ([]os.DirEntry, error) { return nil, errSeam })
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
	})
	t.Run("nonempty backup", func(t *testing.T) {
		f, local, backup := prepareDir(t)
		if err := os.Mkdir(backup, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(backup, "child"), "x")
		_ = local
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), "cannot replace nonempty backup directory")
	})
	t.Run("staging", func(t *testing.T) {
		f, _, _ := prepare(t)
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
	})
	t.Run("stash rename", func(t *testing.T) {
		f, _, _ := prepare(t)
		setHook(t, func(op, path string) error {
			if op == "rename" && filepath.Base(path) == ".zshrc.bak" {
				return errSeam
			}
			return nil
		})
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
	})
	t.Run("local identity", func(t *testing.T) {
		f, local := relinkFixture(t)
		if err := os.Remove(local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, local, "replacement")
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), errSeam.Error())
	})
	t.Run("created link", func(t *testing.T) {
		f, local := relinkFixture(t)
		if err := os.Remove(local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, local, "replacement")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			info, err := realLstat(path)
			if err == nil && info.Mode()&os.ModeSymlink != 0 && path == local {
				return fakeInfoFor(t, 777), nil
			}
			return info, err
		})
		mustContain(t, Relink(f.db, f.configDir, ".zshrc"), "cannot verify created symlink")
	})
	t.Run("cleanup", func(t *testing.T) {
		f, _, _ := prepare(t)
		setHook(t, func(op, _ string) error {
			if op == "removeall" {
				return errSeam
			}
			return nil
		})
		var cleanup *committedCleanupError
		err := Relink(f.db, f.configDir, ".zshrc")
		c, ok := err.(*committedCleanupError)
		if !ok {
			t.Fatalf("error %v is not committedCleanupError", err)
		}
		cleanup = c
		if cleanup.operation != "relink" {
			t.Fatalf("operation %q", cleanup.operation)
		}
	})
}
