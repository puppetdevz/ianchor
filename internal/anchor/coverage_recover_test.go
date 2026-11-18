package anchor

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// recoverFixture tracks one entry whose local path is the symlink created by
// Add, so Recover starts from the normal linked state.
type recoverFixture struct {
	world  *world
	name   string
	local  string
	cloud  string
	parent string
}

func newRecoverFixture(t *testing.T) *recoverFixture {
	t.Helper()
	w := newWorld(t)
	local := filepath.Join(w.localDir, ".zshrc")
	writeFile(t, local, "cloud\n")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: local}); err != nil {
		t.Fatalf("add: %v", err)
	}
	return &recoverFixture{
		world:  w,
		name:   "zshrc",
		local:  local,
		cloud:  filepath.Join(w.configDir, "zshrc"),
		parent: w.localDir,
	}
}

func (f *recoverFixture) recover(force bool) error {
	_, err := Recover(f.world.db, f.world.configDir, f.name, force)
	return err
}

func recoverErr(t *testing.T, f *recoverFixture, force bool) error {
	t.Helper()
	err := f.recover(force)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

// recoverFailSafely requires a reported error plus a safe outcome: the iCloud
// copy still exists and no local content was silently dropped outside the
// hidden backup directory. A seam that also breaks compensation must say
// "rollback failed" for the assertion to accept the residue.
func recoverFailSafely(t *testing.T, f *recoverFixture, force bool) error {
	t.Helper()
	before := f.world.snapshot()
	err := recoverErr(t, f, force)
	checkSafeFailure(t, before, f.world.snapshot(), err)
	return err
}

func TestRecoverEarlyFailures(t *testing.T) {
	requireUnprivileged(t)
	t.Run("entry lookup", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := f.world.db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Recover(f.world.db, f.world.configDir, f.name, false); err == nil {
			t.Fatal("expected database failure")
		}
	})
	t.Run("invalid stored name", func(t *testing.T) {
		f := newRecoverFixture(t)
		original := f.local
		if err := f.world.db.AddEntry("../escape", &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		if _, err := Recover(f.world.db, f.world.configDir, "../escape", false); err == nil {
			t.Fatal("expected name validation failure")
		}
	})
	t.Run("cloud lstat", func(t *testing.T) {
		f := newRecoverFixture(t)
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.cloud {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("cloud stat", func(t *testing.T) {
		f := newRecoverFixture(t)
		realStat := statPath
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.cloud {
				return nil, errSeam
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("original parent stat", func(t *testing.T) {
		f := newRecoverFixture(t)
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.parent {
				calls++
				if calls == 2 {
					return nil, syscall.EACCES
				}
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), "permission denied")
	})
	t.Run("dangling local symlink stat", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.parent, "missing"), f.local); err != nil {
			t.Fatal(err)
		}
		realStat := statPath
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				return nil, syscall.EACCES
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), "permission denied")
	})
	t.Run("local lstat", func(t *testing.T) {
		f := newRecoverFixture(t)
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				return nil, syscall.EACCES
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), "permission denied")
	})
	t.Run("backup root", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.RemoveAll(filepath.Join(f.world.configDir, bakDirName)); err != nil {
			t.Fatal(err)
		}
		override(t, &rootMkdir, func(*os.Root, string, os.FileMode) error { return errSeam })
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
}

func TestRecoverPreCopyFailures(t *testing.T) {
	t.Run("cloud replaced after backup", func(t *testing.T) {
		f := newRecoverFixture(t)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.cloud {
				calls++
				if calls == 4 {
					return fakeInfoFor(t, 999), nil
				}
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), "iCloud source was replaced")
	})
	t.Run("linked original lstat", func(t *testing.T) {
		f := newRecoverFixture(t)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				calls++
				if calls == 3 {
					return nil, errSeam
				}
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("linked original readlink", func(t *testing.T) {
		f := newRecoverFixture(t)
		override(t, &readlinkPath, func(string) (string, error) { return "", errSeam })
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("unrelated link stat", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.parent, "elsewhere"), f.local); err != nil {
			t.Fatal(err)
		}
		realStat := statPath
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				return nil, errSeam
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, true), errSeam.Error())
	})
	t.Run("original lstat failure", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		realLstat := lstatPath
		override(t, &lstatPath, failingAt(t, "recover.go", 77, realLstat))
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("linked original stat", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		elsewhere := filepath.Join(f.parent, "elsewhere")
		writeFile(t, elsewhere, "other\n")
		if err := os.Symlink(elsewhere, f.local); err != nil {
			t.Fatal(err)
		}
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				calls++
				if calls == 2 {
					return nil, errSeam
				}
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, true), errSeam.Error())
	})
	t.Run("cloud target stat", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		elsewhere := filepath.Join(f.parent, "elsewhere")
		writeFile(t, elsewhere, "other\n")
		if err := os.Symlink(elsewhere, f.local); err != nil {
			t.Fatal(err)
		}
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.cloud {
				calls++
				if calls == 3 {
					return nil, errSeam
				}
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, true), errSeam.Error())
	})
	t.Run("link staging", func(t *testing.T) {
		f := newRecoverFixture(t)
		realStaging := stagingPath
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			if strings.HasSuffix(ref, "-recover") {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("conflict detached backup", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.world.configDir {
				calls++
				if calls == 5 {
					return nil, errSeam
				}
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("conflict restore backup", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		override(t, &manifestRestore, func(*fsxManifest, *os.Root) error { return errSeam })
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
		// Dying while refreshing the conflict backup must leave the local
		// edit, the cloud link and the database row untouched.
		assertRegularFile(t, f.local, "local edit\n")
		assertRegularFile(t, f.cloud, "cloud\n")
		assertNoStagingResidue(t, f.world.localDir)
		assertNoStagingResidue(t, f.world.configDir)
		assertEntryStatus(t, f.world, f.name, "linked")
	})
	t.Run("conflict verify backup", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		override(t, &manifestVerify, func(*fsxManifest, *os.Root) error { return errSeam })
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
		assertRegularFile(t, f.local, "local edit\n")
		assertRegularFile(t, f.cloud, "cloud\n")
		assertNoStagingResidue(t, f.world.localDir)
		assertNoStagingResidue(t, f.world.configDir)
		assertEntryStatus(t, f.world, f.name, "linked")
	})
	t.Run("force displaced lstat", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				calls++
				if calls == 3 {
					return nil, errSeam
				}
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, true), errSeam.Error())
	})
	t.Run("force displaced staging", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		realStaging := stagingPath
		calls := 0
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			calls++
			if calls == 3 {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, recoverFailSafely(t, f, true), errSeam.Error())
	})
	t.Run("force displaced restore refused", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		writeFile(t, f.local, "local edit\n")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if strings.Contains(path, "-recover") {
				return fakeInfoFor(t, 4242), nil
			}
			return realLstat(path)
		})
		override(t, &renameDirect, func(from, _ string) error {
			if strings.Contains(from, "-recover") {
				return errSeam
			}
			return rename(from, from)
		})
		_ = renameDirect
		mustContain(t, recoverFailSafely(t, f, true), "replacement retained at")
	})
}

func TestRecoverPostCopyFailures(t *testing.T) {
	t.Run("missing parent created", func(t *testing.T) {
		f := newRecoverFixture(t)
		nested := filepath.Join(f.parent, "missing", "deeper", ".zshrc")
		original := nested
		if err := f.world.db.UpsertEntry(f.name, &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		result, err := Recover(f.world.db, f.world.configDir, f.name, false)
		if err != nil {
			t.Fatalf("recover: %v", err)
		}
		if result.OriginalPath != nested {
			t.Fatalf("original path = %q", result.OriginalPath)
		}
		if readFile(t, nested) != "cloud\n" {
			t.Fatal("recovered content missing")
		}
	})
	t.Run("validate after dirs", func(t *testing.T) {
		f := newRecoverFixture(t)
		original := filepath.Join(f.parent, "missing", ".zshrc")
		if err := f.world.db.UpsertEntry(f.name, &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		realEval := evalSymlinksPath
		calls := 0
		override(t, &evalSymlinksPath, func(path string) (string, error) {
			calls++
			if calls >= 3 {
				return "", errSeam
			}
			return realEval(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("parent identity", func(t *testing.T) {
		f := newRecoverFixture(t)
		if err := os.Remove(f.local); err != nil {
			t.Fatal(err)
		}
		original := filepath.Join(f.parent, "missing", ".zshrc")
		if err := f.world.db.UpsertEntry(f.name, &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		realStat := statPath
		override(t, &statPath, failingAt(t, "recover.go", 216, realStat))
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("original parent changed", func(t *testing.T) {
		f := newRecoverFixture(t)
		// recover.go:214 is the validateOriginal failure after the parent
		// check: the pre-commit validation is the second call made from
		// validateOriginal, which is line-shift safe unlike a caller line.
		override(t, &evalSymlinksPath, failingInFunc(t, "validateOriginal", f.world.configDir, 2, evalSymlinksPath))
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("cloud replaced after copy", func(t *testing.T) {
		f := forceLocal(t)
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.cloud {
				if info, statErr := realLstat(path); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
					return fakeInfoFor(t, 777), nil
				}
			}
			return realLstat(path)
		})
		err := recoverFailSafely(t, f, true)
		mustContain(t, err, "iCloud source")
	})
	t.Run("validate after copy", func(t *testing.T) {
		f := forceLocal(t)
		realEval := evalSymlinksPath
		calls := 0
		override(t, &evalSymlinksPath, func(path string) (string, error) {
			calls++
			if calls == 6 {
				return "", errSeam
			}
			return realEval(path)
		})
		mustContain(t, recoverFailSafely(t, f, true), errSeam.Error())
	})
	t.Run("copy replaced", func(t *testing.T) {
		missingCoordinator := filepath.Join(t.TempDir(), "absent")
		override(t, &coordinatorDirectory, func() string { return missingCoordinator })
		f := forceLocal(t)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				if info, statErr := realLstat(path); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
					calls++
					if calls == 5 {
						return fakeInfoFor(t, 31337), nil
					}
				}
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, true), "recovery destination was replaced")
	})
	t.Run("local root", func(t *testing.T) {
		f := newRecoverFixture(t)
		realOpen := openRootPath
		override(t, &openRootPath, func(path string) (*os.Root, error) {
			if path == f.parent {
				return nil, errSeam
			}
			return realOpen(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("local dir open", func(t *testing.T) {
		f := newRecoverFixture(t)
		realRootOpen := rootOpen
		calls := 0
		override(t, &rootOpen, func(root *os.Root, name string) (*os.File, error) {
			if name == "." {
				calls++
				if calls == 13 {
					return nil, errSeam
				}
			}
			return realRootOpen(root, name)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("local dir replaced", func(t *testing.T) {
		f := newRecoverFixture(t)
		realStatFile := statFile
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			if file.Name() == f.parent+"/." {
				return fakeInfoFor(t, 555), nil
			}
			return realStatFile(file)
		})
		mustContain(t, recoverFailSafely(t, f, false), "recovery destination parent was detached")
	})
}

func forceLocal(t *testing.T) *recoverFixture {
	t.Helper()
	f := newRecoverFixture(t)
	if err := os.Remove(f.local); err != nil {
		t.Fatal(err)
	}
	writeFile(t, f.local, "local edit\n")
	return f
}

func TestRecoverLateFailures(t *testing.T) {
	t.Run("late attached", func(t *testing.T) {
		f := newRecoverFixture(t)
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.world.configDir {
				calls++
				if calls == 6 {
					return nil, errSeam
				}
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), errSeam.Error())
	})
	t.Run("late parent detached", func(t *testing.T) {
		missingCoordinator := filepath.Join(t.TempDir(), "absent")
		override(t, &coordinatorDirectory, func() string { return missingCoordinator })
		f := newRecoverFixture(t)
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == f.parent {
				calls++
				if calls == 9 {
					return fakeInfoFor(t, 321), nil
				}
			}
			return realStat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), "recovery destination parent was detached")
	})
	t.Run("late copy replaced", func(t *testing.T) {
		f := newRecoverFixture(t)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == f.local {
				if info, statErr := realLstat(path); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
					calls++
					if calls == 3 {
						return fakeInfoFor(t, 2024), nil
					}
				}
			}
			return realLstat(path)
		})
		mustContain(t, recoverFailSafely(t, f, false), "recovery destination was replaced")
	})
	t.Run("late backup replaced", func(t *testing.T) {
		f := newRecoverFixture(t)
		realLstat := realBackupRootLstat()
		calls := 0
		override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
			if name == f.name {
				calls++
				if calls == 5 {
					return fakeInfoFor(t, 6060), nil
				}
			}
			return realLstat(root, name)
		})
		err := recoverFailSafely(t, f, false)
		mustContain(t, err, "backup")
	})
	t.Run("local reopen parent", func(t *testing.T) {
		f := newRecoverFixture(t)
		realOpen := openRootPath
		calls := 0
		override(t, &openRootPath, func(path string) (*os.Root, error) {
			if path == f.parent {
				calls++
				if calls == 2 {
					return nil, errSeam
				}
			}
			return realOpen(path)
		})
		override(t, &manifestRestore, func(*fsxManifest, *os.Root) error { return errSeam })
		err := recoverFailSafely(t, f, false)
		mustContain(t, err, "reopen local copy permissions")
		// Compensation uses the same permission helper, so it cannot finish;
		// the contract is that the copy stays in place and every unexecuted
		// step is named instead of being reported as success.
		for _, step := range []string{
			"reopen backup copy permissions",
			"remove incomplete recovered copy",
			"restore original symlink",
			"remove refreshed backup",
		} {
			mustContain(t, err, "not attempted: "+step)
		}
		assertRegularFile(t, f.local, "cloud\n")
		assertRegularFile(t, f.cloud, "cloud\n")
	})
}

type fsxManifest = fsx.PermissionManifest

var _ = store.DB{}

// assertNoStagingResidue fails when a staging directory from an interrupted
// destructive operation is still present in dir.
func assertNoStagingResidue(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ianchor-tmp-") {
			t.Fatalf("staging residue in %s: %s", dir, entry.Name())
		}
	}
}

// assertRegularFile pins the bytes of a real file, not a link into the cloud.
func assertRegularFile(t *testing.T, path, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is still a symlink", path)
	}
	assertFileContent(t, path, want)
}

// assertFileContent pins the exact bytes of a surviving fixture file.
func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}

// assertEntryStatus pins the tracked-entry status after an interrupted run.
func assertEntryStatus(t *testing.T, w *world, name, want string) {
	t.Helper()
	entry, err := w.db.GetEntry(name)
	if err != nil {
		t.Fatalf("get entry %s: %v", name, err)
	}
	if entry == nil {
		t.Fatalf("entry %s missing", name)
	}
	if entry.Status != want {
		t.Fatalf("entry %s status=%s, want %s", name, entry.Status, want)
	}
}
