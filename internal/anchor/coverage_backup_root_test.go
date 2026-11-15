package anchor

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func newBackupFixture(t *testing.T) (*backupRoot, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "cfg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	root, err := openBackupRoot(dir)
	if err != nil {
		t.Fatalf("open backup root: %v", err)
	}
	t.Cleanup(root.Close)
	return root, dir
}

func realBackupRootLstat() func(*os.Root, string) (os.FileInfo, error) {
	return func(root *os.Root, name string) (os.FileInfo, error) { return root.Lstat(name) }
}

func TestOpenBackupRootFailures(t *testing.T) {
	newDir := func(t *testing.T) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "cfg")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	t.Run("config root open", func(t *testing.T) {
		dir := newDir(t)
		override(t, &openRootPath, func(string) (*os.Root, error) { return nil, errSeam })
		if _, err := openBackupRoot(dir); err == nil {
			t.Fatal("expected config root failure")
		}
	})
	t.Run("mkdir failure", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootMkdir, func(*os.Root, string, os.FileMode) error { return syscall.EACCES })
		if _, err := openBackupRoot(dir); err == nil {
			t.Fatal("expected mkdir failure")
		}
	})
	t.Run("created then lstat missing", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootLstat, func(*os.Root, string) (os.FileInfo, error) { return nil, os.ErrNotExist })
		if _, err := openBackupRoot(dir); err == nil {
			t.Fatal("expected lstat failure")
		}
	})
	t.Run("created then lstat failure", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootLstat, func(*os.Root, string) (os.FileInfo, error) { return nil, syscall.EACCES })
		if _, err := openBackupRoot(dir); err == nil {
			t.Fatal("expected lstat failure")
		}
	})
	t.Run("preexisting lstat failure", func(t *testing.T) {
		dir := newDir(t)
		if err := os.Mkdir(filepath.Join(dir, bakDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		override(t, &rootLstat, func(*os.Root, string) (os.FileInfo, error) { return nil, syscall.EACCES })
		if _, err := openBackupRoot(dir); err == nil {
			t.Fatal("expected lstat failure")
		}
	})
	t.Run("child root open", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		override(t, &rootOpen, func(*os.Root, string) (*os.File, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup staging", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup rename", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		override(t, &renameAt, func(int, string, int, string) error { return errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup identity restore failure", func(t *testing.T) {
		dir := newDir(t)
		realLstat := realBackupRootLstat()
		calls := 0
		override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
			calls++
			if calls == 3 {
				return fakeInfoFor(t, 987654), nil
			}
			return realLstat(root, name)
		})
		realRename := renameAt
		renames := 0
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			renames++
			if renames == 2 {
				return errSeam
			}
			return realRename(fromFD, from, toFD, to)
		})
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup identity mismatch", func(t *testing.T) {
		dir := newDir(t)
		realLstat := realBackupRootLstat()
		calls := 0
		override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
			calls++
			if calls == 3 {
				return fakeInfoFor(t, 12345), nil
			}
			return realLstat(root, name)
		})
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup remove restore failure", func(t *testing.T) {
		dir := newDir(t)
		realRename := renameAt
		renames := 0
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			renames++
			if renames == 2 {
				return errSeam
			}
			return realRename(fromFD, from, toFD, to)
		})
		override(t, &rootRemove, func(*os.Root, string) error { return errSeam })
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup remove", func(t *testing.T) {
		dir := newDir(t)
		override(t, &rootRemove, func(*os.Root, string) error { return errSeam })
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup directory may remain")
	})
	t.Run("cleanup hook", func(t *testing.T) {
		dir := newDir(t)
		setHook(t, func(op, _ string) error {
			if op == "remove-failed-backup-root" {
				return errSeam
			}
			return nil
		})
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "backup cleanup")
	})
	t.Run("attached failure", func(t *testing.T) {
		dir := newDir(t)
		override(t, &statPath, func(string) (os.FileInfo, error) { return nil, errSeam })
		_, err := openBackupRoot(dir)
		mustContain(t, err, "config directory is detached")
	})
}

func TestRollbackCreationFailures(t *testing.T) {
	rollback := func(t *testing.T, want string, apply func(t *testing.T)) {
		t.Helper()
		root, _ := newBackupFixture(t)
		if apply != nil {
			apply(t)
		}
		j := &journal{}
		root.rollbackCreation(j)
		failures := j.rollback()
		if len(failures) == 0 {
			t.Fatal("expected rollback failure")
		}
		if !strings.Contains(strings.Join(failures, "; "), want) {
			t.Fatalf("failures %v, want %q", failures, want)
		}
		// A reported failure must leave the backup directory in place: it is
		// either still named .bak or parked under a staging name the report
		// names. Silently deleting it would contradict the reported error.
		configDir := filepath.Dir(root.display)
		if _, err := os.Lstat(root.display); err == nil {
			return
		}
		matches, err := filepath.Glob(filepath.Join(configDir, ".*backup-root-undo*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("backup directory vanished after reported failure %v", failures)
		}
	}
	t.Run("attached", func(t *testing.T) {
		rollback(t, "detached", func(t *testing.T) {
			override(t, &statPath, func(string) (os.FileInfo, error) { return nil, errSeam })
		})
	})
	t.Run("root open", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			realOpen := rootOpen
			calls := 0
			override(t, &rootOpen, func(root *os.Root, name string) (*os.File, error) {
				calls++
				if calls == 3 {
					return nil, errSeam
				}
				return realOpen(root, name)
			})
		})
	})
	t.Run("pinned stat", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			realStat := statFile
			calls := 0
			override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
				calls++
				if calls == 3 {
					return nil, errSeam
				}
				return realStat(file)
			})
		})
	})
	t.Run("hook", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			setHook(t, func(op, _ string) error {
				if op == "remove-backup-root" {
					return errSeam
				}
				return nil
			})
		})
	})
	t.Run("config open", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			realOpen := rootOpen
			calls := 0
			override(t, &rootOpen, func(root *os.Root, name string) (*os.File, error) {
				calls++
				if calls == 4 {
					return nil, errSeam
				}
				return realOpen(root, name)
			})
		})
	})
	t.Run("staging", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		})
	})
	t.Run("rename", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			override(t, &renameAt, func(int, string, int, string) error { return errSeam })
		})
	})
	t.Run("identity restore failure", func(t *testing.T) {
		rollback(t, "backup replacement retained", func(t *testing.T) {
			realLstat := realBackupRootLstat()
			calls := 0
			override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
				calls++
				if calls == 2 {
					return fakeInfoFor(t, 55), nil
				}
				return realLstat(root, name)
			})
			realRename := renameAt
			renames := 0
			override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
				renames++
				if renames == 2 {
					return errSeam
				}
				return realRename(fromFD, from, toFD, to)
			})
		})
	})
	t.Run("identity mismatch", func(t *testing.T) {
		rollback(t, "backup directory was replaced", func(t *testing.T) {
			realLstat := realBackupRootLstat()
			calls := 0
			override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
				calls++
				if calls == 2 {
					return fakeInfoFor(t, 56), nil
				}
				return realLstat(root, name)
			})
		})
	})
	t.Run("remove restore failure", func(t *testing.T) {
		rollback(t, "backup directory retained", func(t *testing.T) {
			realRename := renameAt
			renames := 0
			override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
				renames++
				if renames == 2 {
					return errSeam
				}
				return realRename(fromFD, from, toFD, to)
			})
			override(t, &rootRemove, func(*os.Root, string) error { return errSeam })
		})
	})
	t.Run("remove", func(t *testing.T) {
		rollback(t, "seam failure", func(t *testing.T) {
			override(t, &rootRemove, func(*os.Root, string) error { return errSeam })
		})
	})
}

func TestAttachedFailures(t *testing.T) {
	check := func(t *testing.T, apply func(t *testing.T)) error {
		t.Helper()
		root, _ := newBackupFixture(t)
		if apply != nil {
			apply(t)
		}
		return root.attached()
	}
	t.Run("config stat", func(t *testing.T) {
		err := check(t, func(t *testing.T) {
			override(t, &statPath, func(string) (os.FileInfo, error) { return nil, errSeam })
		})
		if err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("config open", func(t *testing.T) {
		err := check(t, func(t *testing.T) {
			override(t, &rootOpen, func(*os.Root, string) (*os.File, error) { return nil, errSeam })
		})
		if err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("config stat file", func(t *testing.T) {
		err := check(t, func(t *testing.T) {
			override(t, &statFile, func(*os.File) (os.FileInfo, error) { return nil, errSeam })
		})
		if err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("backup lstat", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		if err := os.Remove(filepath.Join(root.display)); err != nil {
			t.Fatal(err)
		}
		if err := root.attached(); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("backup open", func(t *testing.T) {
		err := check(t, func(t *testing.T) {
			realOpen := rootOpen
			calls := 0
			override(t, &rootOpen, func(rootHandle *os.Root, name string) (*os.File, error) {
				calls++
				if calls == 2 {
					return nil, errSeam
				}
				return realOpen(rootHandle, name)
			})
		})
		if err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("backup stat file", func(t *testing.T) {
		err := check(t, func(t *testing.T) {
			realStat := statFile
			calls := 0
			override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
				calls++
				if calls == 2 {
					return nil, errSeam
				}
				return realStat(file)
			})
		})
		if err == nil {
			t.Fatal("expected failure")
		}
	})
}

func TestRenameAndMoveFailures(t *testing.T) {
	t.Run("rename root open", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, name string) (*os.File, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realOpen(handle, name)
		})
		if err := root.rename("a", "b"); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move in source parent", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		missing := filepath.Join(t.TempDir(), "missing", "file")
		if err := root.moveIn(missing, "target"); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move in parent open", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		src := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, name string) (*os.File, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realOpen(handle, name)
		})
		if err := root.moveIn(src, "target"); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move in backup open", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		src := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, name string) (*os.File, error) {
			calls++
			if calls == 4 {
				return nil, errSeam
			}
			return realOpen(handle, name)
		})
		if err := root.moveIn(src, "target"); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move out dest parent", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		missing := filepath.Join(t.TempDir(), "missing", "file")
		if err := root.moveOut("staged", missing); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move out backup open", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, name string) (*os.File, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realOpen(handle, name)
		})
		if err := root.moveOut("staged", filepath.Join(t.TempDir(), "dest")); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move out parent open", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, name string) (*os.File, error) {
			calls++
			if calls == 4 {
				return nil, errSeam
			}
			return realOpen(handle, name)
		})
		if err := root.moveOut("staged", filepath.Join(t.TempDir(), "dest")); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("copy attached", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		override(t, &statPath, func(string) (os.FileInfo, error) { return nil, errSeam })
		if err := root.copyRaw("src", "name", nil); err == nil {
			t.Fatal("expected copyRaw failure")
		}
		if err := root.copyNodeModes("src", "name", nil); err == nil {
			t.Fatal("expected copyNodeModes failure")
		}
		if err := root.copyNode("src", "name", nil, nil); err == nil {
			t.Fatal("expected copyNode failure")
		}
	})
	t.Run("staging", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		if _, err := root.staging("ref"); err == nil {
			t.Fatal("expected staging failure")
		}
		if err := root.removeOwned("name", fakeInfoFor(t, 1)); err == nil {
			t.Fatal("expected removeOwned failure")
		}
	})
}

func TestRenameOwnedFailures(t *testing.T) {
	stage := func(t *testing.T) (*backupRoot, string) {
		t.Helper()
		root, _ := newBackupFixture(t)
		name := "staged"
		if err := os.WriteFile(filepath.Join(root.display, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, name
	}
	t.Run("nil identity", func(t *testing.T) {
		root, name := stage(t)
		if err := root.renameOwned(name, "dest", nil); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("staging", func(t *testing.T) {
		root, name := stage(t)
		info, err := os.Lstat(filepath.Join(root.display, name))
		if err != nil {
			t.Fatal(err)
		}
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		if err := root.renameOwned(name, "dest", info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("replaced restore failure", func(t *testing.T) {
		root, name := stage(t)
		info, err := os.Lstat(filepath.Join(root.display, name))
		if err != nil {
			t.Fatal(err)
		}
		override(t, &rootLstat, func(handle *os.Root, entry string) (os.FileInfo, error) {
			if strings.Contains(entry, "-restore") {
				return fakeInfoFor(t, 777), nil
			}
			return handle.Lstat(entry)
		})
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if strings.Contains(from, "-restore") {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		err = root.renameOwned(name, "dest", info)
		mustContain(t, err, "foreign object retained")
	})
	t.Run("replaced", func(t *testing.T) {
		root, name := stage(t)
		info, err := os.Lstat(filepath.Join(root.display, name))
		if err != nil {
			t.Fatal(err)
		}
		override(t, &rootLstat, func(handle *os.Root, entry string) (os.FileInfo, error) {
			if strings.Contains(entry, "-restore") {
				return fakeInfoFor(t, 778), nil
			}
			return handle.Lstat(entry)
		})
		err = root.renameOwned(name, "dest", info)
		mustContain(t, err, "staged entry replaced")
	})
	t.Run("dest rename restore failure", func(t *testing.T) {
		root, name := stage(t)
		info, err := os.Lstat(filepath.Join(root.display, name))
		if err != nil {
			t.Fatal(err)
		}
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if to == "dest" || to == name {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		err = root.renameOwned(name, "dest", info)
		mustContain(t, err, "restore failed")
	})
}

func TestMoveOwnedFailures(t *testing.T) {
	setup := func(t *testing.T) (root *backupRoot, source, name string, info os.FileInfo) {
		t.Helper()
		root, _ = newBackupFixture(t)
		source = filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		name = "staged"
		if err := os.WriteFile(filepath.Join(root.display, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		var err error
		if info, err = os.Lstat(source); err != nil {
			t.Fatal(err)
		}
		return root, source, name, info
	}
	bakIdentity := func(t *testing.T, root *backupRoot, name string) os.FileInfo {
		t.Helper()
		info, err := os.Lstat(filepath.Join(root.display, name))
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	t.Run("move in nil", func(t *testing.T) {
		root, source, name, _ := setup(t)
		if err := root.moveOwnedIn(source, name, nil); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move in staging", func(t *testing.T) {
		root, source, name, info := setup(t)
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		if err := root.moveOwnedIn(source, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move in source", func(t *testing.T) {
		root, _, name, info := setup(t)
		missing := filepath.Join(t.TempDir(), "missing", "file")
		if err := root.moveOwnedIn(missing, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move in replaced", func(t *testing.T) {
		root, source, name, info := setup(t)
		override(t, &rootLstat, func(handle *os.Root, entry string) (os.FileInfo, error) {
			if strings.Contains(entry, "-undo") {
				return fakeInfoFor(t, 901), nil
			}
			return handle.Lstat(entry)
		})
		err := root.moveOwnedIn(source, name, info)
		mustContain(t, err, "entry was replaced")
	})
	t.Run("move in replaced restore failure", func(t *testing.T) {
		root, source, name, info := setup(t)
		override(t, &rootLstat, func(handle *os.Root, entry string) (os.FileInfo, error) {
			if strings.Contains(entry, "-undo") {
				return fakeInfoFor(t, 902), nil
			}
			return handle.Lstat(entry)
		})
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if strings.Contains(from, "-undo") {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		err := root.moveOwnedIn(source, name, info)
		mustContain(t, err, "foreign object retained")
	})
	t.Run("move in rename failure", func(t *testing.T) {
		root, source, name, info := setup(t)
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if to == name || to == source {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		err := root.moveOwnedIn(source, name, info)
		mustContain(t, err, "seam failure")
	})
	t.Run("move out nil", func(t *testing.T) {
		root, source, name, _ := setup(t)
		if err := root.moveOwnedOut(name, source, nil); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move out staging", func(t *testing.T) {
		root, source, name, _ := setup(t)
		info := bakIdentity(t, root, name)
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		if err := root.moveOwnedOut(name, source, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("move out replaced restore failure", func(t *testing.T) {
		root, source, name, _ := setup(t)
		info := bakIdentity(t, root, name)
		override(t, &rootLstat, func(handle *os.Root, entry string) (os.FileInfo, error) {
			if strings.Contains(entry, "-restore") {
				return fakeInfoFor(t, 903), nil
			}
			return handle.Lstat(entry)
		})
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if strings.Contains(from, "-restore") {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		err := root.moveOwnedOut(name, source, info)
		mustContain(t, err, "foreign object retained")
	})
	t.Run("move out replaced", func(t *testing.T) {
		root, source, name, _ := setup(t)
		info := bakIdentity(t, root, name)
		override(t, &rootLstat, func(handle *os.Root, entry string) (os.FileInfo, error) {
			if strings.Contains(entry, "-restore") {
				return fakeInfoFor(t, 904), nil
			}
			return handle.Lstat(entry)
		})
		err := root.moveOwnedOut(name, source, info)
		mustContain(t, err, "staged entry replaced")
	})
	t.Run("move out restore failure", func(t *testing.T) {
		root, source, name, _ := setup(t)
		info := bakIdentity(t, root, name)
		dest := filepath.Base(source)
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if to == dest || strings.Contains(from, "-restore") {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		err := root.moveOwnedOut(name, source, info)
		mustContain(t, err, "restore failed")
	})
}

func TestMakeCopiedTreeWritableFailures(t *testing.T) {
	setup := func(t *testing.T) (*backupRoot, string, os.FileInfo) {
		t.Helper()
		root, _ := newBackupFixture(t)
		dir := filepath.Join(root.display, "copied")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		return root, "copied", info
	}
	t.Run("open", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &rootOpen, func(*os.Root, string) (*os.File, error) { return nil, errSeam })
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("stat", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &statFile, func(*os.File) (os.FileInfo, error) { return nil, errSeam })
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("replaced", func(t *testing.T) {
		root, name, _ := setup(t)
		if err := makeCopiedTreeWritable(root.root, name, fakeInfoFor(t, 31)); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("chmod", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &chmodFile, func(*os.File, os.FileMode) error { return errSeam })
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("open root", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &rootOpenRoot, func(*os.Root, string) (*os.Root, error) { return nil, errSeam })
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("child open", func(t *testing.T) {
		root, name, info := setup(t)
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, entry string) (*os.File, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realOpen(handle, entry)
		})
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("child stat", func(t *testing.T) {
		root, name, info := setup(t)
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realStat(file)
		})
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("child replaced", func(t *testing.T) {
		root, name, info := setup(t)
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return fakeInfoFor(t, 32), nil
			}
			return realStat(file)
		})
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("read dir", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &readDirFile, func(*os.File, int) ([]os.DirEntry, error) { return nil, errSeam })
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("child lstat", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &rootLstat, func(*os.Root, string) (os.FileInfo, error) { return nil, errSeam })
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("nested hook", func(t *testing.T) {
		root, name, info := setup(t)
		setHook(t, func(op, _ string) error {
			if op == "cleanup-chmod" {
				return errSeam
			}
			return nil
		})
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("nested recursion", func(t *testing.T) {
		root, name, info := setup(t)
		realOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(handle *os.Root, entry string) (*os.File, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realOpen(handle, entry)
		})
		if err := makeCopiedTreeWritable(root.root, name, info); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("disposable empty dir", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		dir := filepath.Join(root.display, "empty")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := root.removeOwnedCopy("empty", info); err != nil {
			t.Fatalf("removeOwnedCopy: %v", err)
		}
	})
	t.Run("disposable removal failure", func(t *testing.T) {
		root, name, info := setup(t)
		override(t, &rootRemove, func(*os.Root, string) error { return syscall.ENOTEMPTY })
		override(t, &makeCopiedTreeWritableHook, func(*os.Root, string, os.FileInfo) error { return errSeam })
		err := root.removeOwnedCopy(name, info)
		mustContain(t, err, "backup retained")
	})
	t.Run("disposable non-empty", func(t *testing.T) {
		root, name, info := setup(t)
		if err := root.removeOwnedCopy(name, info); err != nil {
			t.Fatalf("removeOwnedCopy: %v", err)
		}
	})
}
func TestMoveOutAndOwnedStageRestoreFailures(t *testing.T) {
	t.Run("move out refused", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		setHook(t, func(op, path string) error {
			if op == "rename" {
				return errSeam
			}
			return nil
		})
		if err := root.moveOut("stage", filepath.Join(t.TempDir(), "dest")); err == nil {
			t.Fatal("expected rename failure")
		}
	})
	t.Run("move out detached", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		override(t, &statPath, func(string) (os.FileInfo, error) { return nil, errSeam })
		if err := root.moveOut("stage", filepath.Join(t.TempDir(), "dest")); err == nil {
			t.Fatal("expected attached failure")
		}
	})
	t.Run("move owned in retains entry", func(t *testing.T) {
		root, dir := newBackupFixture(t)
		source := filepath.Join(dir, "live")
		writeFile(t, source, "live")
		info, err := os.Lstat(source)
		if err != nil {
			t.Fatal(err)
		}
		override(t, &renameAt, func(fromFD int, from string, toFD int, to string) error {
			if to == "tracked" {
				return errSeam
			}
			return renameAtReal(fromFD, from, toFD, to)
		})
		setHook(t, func(op, path string) error {
			if op == "rename" && strings.Contains(path, "-undo") {
				return errSeam
			}
			return nil
		})
		err = root.moveOwnedIn(source, "tracked", info)
		mustContain(t, err, "move failed; entry retained")
	})
	t.Run("owned stage restore refused", func(t *testing.T) {
		root, dir := newBackupFixture(t)
		name := "tracked"
		writeFile(t, filepath.Join(dir, ".bak", name), "live")
		info, err := os.Lstat(filepath.Join(dir, ".bak", name))
		if err != nil {
			t.Fatal(err)
		}
		realLstat := realBackupRootLstat()
		override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
			if strings.Contains(name, "-undo") {
				return fakeInfoFor(t, 4242), nil
			}
			return realLstat(root, name)
		})
		override(t, &renameAt, func(_ int, from string, _ int, _ string) error {
			if strings.Contains(from, "-undo") {
				return errSeam
			}
			return nil
		})
		err = root.removeOwned(name, info)
		mustContain(t, err, "backup replacement could not be restored")
	})
}
