package anchor

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/puppetdevz/ianchor/internal/config"
	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

func mustContain(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), sub) {
		t.Fatalf("error %v, want substring %q", err, sub)
	}
}

func setHook(t *testing.T, hook func(op, path string) error) {
	t.Helper()
	prev := opHook
	opHook = hook
	t.Cleanup(func() { opHook = prev })
}

func restoreMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	t.Cleanup(func() { _ = os.Chmod(path, mode) })
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
}

func openRoot(t *testing.T, path string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatalf("open root %s: %v", path, err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func TestListAndLiveStatusErrors(t *testing.T) {
	f := newFixture(t)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := List(f.db, f.configDir); err == nil {
		t.Fatal("closed database should fail List")
	}
	if got := liveStatus(store.Entry{Status: "linked"}, ""); got != "linked" {
		t.Fatalf("empty config dir status = %q", got)
	}
}

func TestInitAndDiscoverErrors(t *testing.T) {
	requireUnprivileged(t)
	t.Run("missing icloud", func(t *testing.T) {
		_, err := Init(filepath.Join(t.TempDir(), "missing"), "ianchor")
		mustContain(t, err, config.ErrICloudMissing)
	})
	t.Run("database path is a directory", func(t *testing.T) {
		base := t.TempDir()
		if err := os.MkdirAll(filepath.Join(base, "ianchor", "ianchor.db"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Init(base, "ianchor")
		mustContain(t, err, "unable to open database file")
		if _, statErr := os.Stat(filepath.Join(base, "ianchor", "ianchor.db")); statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("entries query fails", func(t *testing.T) {
		base := t.TempDir()
		dir := filepath.Join(base, "ianchor")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", filepath.Join(dir, "ianchor.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE entries (id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = Init(base, "ianchor")
		mustContain(t, err, "no such column")
	})
	t.Run("discover read dir", func(t *testing.T) {
		f := newFixture(t)
		file := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := discover(f.db, file)
		mustContain(t, err, "not a directory")
	})
	t.Run("discover closed database", func(t *testing.T) {
		f := newFixture(t)
		if err := os.WriteFile(filepath.Join(f.configDir, "cfg"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := discover(f.db, f.configDir)
		if err == nil {
			t.Fatal("expected closed database error")
		}
		if _, statErr := os.Stat(filepath.Join(f.configDir, "cfg")); statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("second insert rolls back the first", func(t *testing.T) {
		f := newFixture(t)
		for _, name := range []string{"a", "b"} {
			if err := os.WriteFile(filepath.Join(f.configDir, name), []byte(name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		var adds int
		setHook(t, func(op, path string) error {
			if op == "db.add" {
				adds++
				if adds == 2 {
					return errors.New("second add blocked")
				}
			}
			return nil
		})
		_, err := discover(f.db, f.configDir)
		mustContain(t, err, "second add blocked")
		entries, err := f.db.AllEntries()
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("rollback left %#v", entries)
		}
		if readFile(t, filepath.Join(f.configDir, "a")) != "a" || readFile(t, filepath.Join(f.configDir, "b")) != "b" {
			t.Fatal("discovered files were modified")
		}
	})
}

func TestSafetyErrorPaths(t *testing.T) {
	requireUnprivileged(t)
	configDir := t.TempDir()
	t.Run("eval config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file")
		err := validateOriginal(filepath.Join(configDir, "missing"), &path)
		if err == nil {
			t.Fatal("expected eval error")
		}
	})
	t.Run("identity stat error", func(t *testing.T) {
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, locked, 0o755)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(locked, "child")
		err := validateOriginal(configDir, &path)
		mustContain(t, err, "permission denied")
	})
	t.Run("dangling parent symlink", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "dangling")
		if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), parent); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "file")
		err := validateOriginal(configDir, &path)
		if !os.IsNotExist(err) {
			t.Fatalf("got %v, want not exist", err)
		}
	})
	t.Run("parent walk permission", func(t *testing.T) {
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, locked, 0o755)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(locked, "child", "file")
		err := validateOriginal(configDir, &path)
		mustContain(t, err, "permission denied")
	})
	t.Run("identity helpers", func(t *testing.T) {
		if _, err := identityOverlap(filepath.Join(t.TempDir(), "missing"), "/tmp", false); err == nil {
			t.Fatal("expected missing config error")
		}
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, locked, 0o755)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := identityOverlap(configDir, filepath.Join(locked, "child"), false); err == nil {
			t.Fatal("expected lstat error")
		}
		link := filepath.Join(t.TempDir(), "dangling-link")
		if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), link); err != nil {
			t.Fatal(err)
		}
		if _, err := identityOverlap(configDir, link, true); !os.IsNotExist(err) {
			t.Fatalf("follow final: %v", err)
		}
		if _, err := identityOverlap(configDir, filepath.Join(locked, "a", "b"), false); err == nil {
			t.Fatal("expected ancestor stat error")
		}
		parent := t.TempDir()
		nested := filepath.Join(parent, "nested")
		if err := os.Mkdir(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		overlap, err := identityOverlap(nested, parent, false)
		if err != nil || !overlap {
			t.Fatalf("ancestor overlap = %v, %v", overlap, err)
		}
	})
	t.Run("within relative target", func(t *testing.T) {
		if within("/tmp/base", "relative") {
			t.Fatal("relative target should not be within an absolute base")
		}
		if !within("/tmp/base", "/tmp/base/child") {
			t.Fatal("child should be within base")
		}
	})
}

func TestEnsureDirAndCreatedCleanup(t *testing.T) {
	requireUnprivileged(t)
	t.Run("stat error", func(t *testing.T) {
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, locked, 0o755)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		_, err := ensureDir(filepath.Join(locked, "child"))
		mustContain(t, err, "permission denied")
	})
	t.Run("mkdir hook", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "new")
		setHook(t, func(op, path string) error {
			if op == "mkdir" && path == target {
				return errors.New("mkdir blocked")
			}
			return nil
		})
		_, err := ensureDir(target)
		mustContain(t, err, "mkdir blocked")
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			t.Fatalf("target created despite hook: %v", statErr)
		}
	})
	t.Run("ancestor replaced", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "new")
		setHook(t, func(op, path string) error {
			if op == "mkdir" && path == target {
				if err := os.Rename(parent, parent+"-old"); err != nil {
					return err
				}
				return os.Mkdir(parent, 0o755)
			}
			return nil
		})
		_, err := ensureDir(target)
		mustContain(t, err, "directory ancestor was replaced")
		if _, statErr := os.Stat(parent + "-old"); statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("concurrent mkdir", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "raced")
		setHook(t, func(op, path string) error {
			if op == "mkdir" && path == target {
				return os.Mkdir(path, 0o755)
			}
			return nil
		})
		created, err := ensureDir(target)
		if err != nil {
			t.Fatal(err)
		}
		if len(created) != 0 {
			t.Fatalf("claimed foreign directory %#v", created)
		}
		info, err := os.Stat(target)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
			t.Fatalf("dir = %v, %v", info, err)
		}
	})
	t.Run("remove created dirs", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := removeCreatedDirs([]createdDirectory{{path: dir}}); err == nil || !strings.Contains(err.Error(), "cannot verify created directory") {
			t.Fatalf("nil identity: %v", err)
		}
		setHook(t, func(op, path string) error {
			if op == "rename" && path == dir {
				return errors.New("rename blocked")
			}
			return nil
		})
		if err := removeCreatedDirs([]createdDirectory{{path: dir, identity: info}}); err == nil || !strings.Contains(err.Error(), "rename blocked") {
			t.Fatalf("rename: %v", err)
		}
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("replaced directory retained", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(t.TempDir(), "other")
		if err := os.Mkdir(other, 0o755); err != nil {
			t.Fatal(err)
		}
		stale, err := os.Lstat(other)
		if err != nil {
			t.Fatal(err)
		}
		var renames int
		setHook(t, func(op, path string) error {
			if op != "rename" {
				return nil
			}
			renames++
			if renames == 2 {
				return errors.New("restore blocked")
			}
			return nil
		})
		err = removeCreatedDirs([]createdDirectory{{path: dir, identity: stale}})
		mustContain(t, err, "replacement retained")
		mustContain(t, err, "restore blocked")
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Fatalf("original path still present: %v", statErr)
		}
	})
	t.Run("replaced directory restored", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		other, err := os.Lstat(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		err = removeCreatedDirs([]createdDirectory{{path: dir, identity: other}})
		mustContain(t, err, "created directory was replaced")
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("remove restore fails", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		var renames int
		setHook(t, func(op, path string) error {
			if op == "remove" {
				return errors.New("remove blocked")
			}
			if op == "rename" {
				renames++
				if renames > 1 {
					return errors.New("restore blocked")
				}
			}
			return nil
		})
		err = removeCreatedDirs([]createdDirectory{{path: dir, identity: info}})
		mustContain(t, err, "directory retained")
		mustContain(t, err, "restore blocked")
	})
}

func TestOpsErrorPaths(t *testing.T) {
	requireUnprivileged(t)
	t.Run("copy node identity", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "src")
		if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
		missing := filepath.Join(t.TempDir(), "missing", "dst")
		if err := copyNodeWithIdentity(src, missing, nil, nil, nil); err == nil {
			t.Fatal("expected missing parent")
		}
		dst := filepath.Join(t.TempDir(), "dst")
		if err := copyNodeWithIdentity(src, dst, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
		if readFile(t, dst) != "payload" {
			t.Fatalf("copied %q", readFile(t, dst))
		}
	})
	t.Run("remove all", func(t *testing.T) {
		if err := removeAllWithinRoot(nil, filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected missing path")
		}
		locked := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(locked, 0o755); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, locked, 0o755)
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		if err := removeAllWithinRoot(nil, locked); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("open: %v", err)
		}
		dir := filepath.Join(t.TempDir(), "tree")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "keep")
		if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		setImmutable(t, file)
		if err := removeAllWithinRoot(nil, dir); err == nil || !strings.Contains(err.Error(), "operation not permitted") {
			t.Fatalf("immutable child: %v", err)
		}
		if readFile(t, file) != "keep" {
			t.Fatal("immutable file was removed")
		}
	})
	t.Run("remove pinned", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := removePinned(nil, filepath.Join(t.TempDir(), "missing", "file"), info); err == nil {
			t.Fatal("expected missing parent")
		}
		if err := removePinned(nil, filepath.Join(filepath.Dir(file), "absent"), info); err == nil {
			t.Fatal("expected missing name")
		}
		other := filepath.Join(t.TempDir(), "other")
		if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := removePinned(nil, other, info); err == nil || !strings.Contains(err.Error(), "cleanup target replaced") {
			t.Fatalf("inode: %v", err)
		}
		if readFile(t, other) != "other" || readFile(t, file) != "keep" {
			t.Fatal("files changed")
		}
		parent := filepath.Join(t.TempDir(), "ro")
		if err := os.Mkdir(parent, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(parent, "child"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, parent, 0o755)
		if err := os.Chmod(parent, 0o555); err != nil {
			t.Fatal(err)
		}
		setImmutable(t, parent)
		opened, err := os.Open(parent)
		if err != nil {
			t.Fatal(err)
		}
		defer opened.Close()
		parentInfo, err := opened.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ensureParentWritable(opened, parentInfo); err == nil || !strings.Contains(err.Error(), "operation not permitted") {
			t.Fatalf("chmod: %v", err)
		}
	})
	t.Run("owned symlink and entry", func(t *testing.T) {
		if err := removeOwnedSymlink(filepath.Join(t.TempDir(), "link"), nil); err == nil || !strings.Contains(err.Error(), "cannot verify created symlink") {
			t.Fatalf("nil: %v", err)
		}
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := removeOwnedSymlink(file, info); err == nil || !strings.Contains(err.Error(), "cannot verify created symlink") {
			t.Fatalf("non-symlink: %v", err)
		}
		if readFile(t, file) != "keep" {
			t.Fatal("file changed")
		}
		if err := removeOwnedEntryInternal(file, nil, false, false); err == nil || !strings.Contains(err.Error(), "cannot verify created entry") {
			t.Fatalf("nil entry: %v", err)
		}
		setHook(t, func(op, path string) error {
			if op == "rename" && path == file {
				return errors.New("rename blocked")
			}
			return nil
		})
		if err := removeOwnedEntryInternal(file, info, false, false); err == nil || !strings.Contains(err.Error(), "rename blocked") {
			t.Fatalf("rename: %v", err)
		}
		if readFile(t, file) != "keep" {
			t.Fatal("file changed")
		}
	})
	t.Run("replacement retained", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		var renames int
		setHook(t, func(op, path string) error {
			if op != "rename" {
				return nil
			}
			renames++
			if renames == 1 && path == file {
				if err := os.Remove(path); err != nil {
					return err
				}
				return os.WriteFile(path, []byte("new"), 0o644)
			}
			if renames == 2 {
				return errors.New("restore blocked")
			}
			return nil
		})
		err = removeOwnedEntryInternal(file, info, false, false)
		mustContain(t, err, "replacement retained")
		mustContain(t, err, "restore blocked")
	})
	t.Run("disposable copy retained", func(t *testing.T) {
		parent := t.TempDir()
		dir := filepath.Join(parent, "copy")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "child"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = filepath.WalkDir(parent, func(path string, _ os.DirEntry, err error) error {
				if err == nil {
					_ = os.Chmod(path, 0o755)
				}
				return nil
			})
		})
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		err = removeOwnedEntryInternal(dir, info, true, true)
		mustContain(t, err, "created copy retained")
		var located *cleanupLocationError
		if !errors.As(err, &located) || located.path == "" {
			t.Fatalf("retained location: %v", err)
		}
		if err := os.Chmod(located.path, 0o755); err != nil {
			t.Fatal(err)
		}
		if readFile(t, filepath.Join(located.path, "child")) != "x" {
			t.Fatal("copy contents lost")
		}
		if _, statErr := os.Lstat(dir); !os.IsNotExist(statErr) {
			t.Fatalf("original path: %v", statErr)
		}
	})
	t.Run("disposable parent disappears", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "copy")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		setHook(t, func(op, path string) error {
			if op == "remove" {
				if err := os.RemoveAll(filepath.Dir(path)); err != nil {
					return err
				}
				return syscall.ENOTEMPTY
			}
			return nil
		})
		err = removeOwnedEntryInternal(dir, info, true, true)
		mustContain(t, err, "created copy retained")
		if _, statErr := os.Stat(filepath.Dir(dir)); !os.IsNotExist(statErr) && statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("move owned", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "dest")
		if err := moveOwnedEntry(file, dest, nil); err == nil || !strings.Contains(err.Error(), "cannot verify staged entry") {
			t.Fatalf("nil: %v", err)
		}
		setHook(t, func(op, path string) error {
			if op == "rename" && path == file {
				return errors.New("rename blocked")
			}
			return nil
		})
		if err := moveOwnedEntry(file, dest, info); err == nil || !strings.Contains(err.Error(), "rename blocked") {
			t.Fatalf("rename: %v", err)
		}
		if readFile(t, file) != "keep" {
			t.Fatal("source changed")
		}
	})
	t.Run("move restore failures", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "dest")
		if err := os.WriteFile(dest, []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
		var renames int
		setHook(t, func(op, path string) error {
			if op != "rename" {
				return nil
			}
			renames++
			if renames == 1 {
				if err := os.Remove(file); err != nil {
					return err
				}
				return os.WriteFile(file, []byte("foreign"), 0o644)
			}
			if renames == 2 {
				return errors.New("restore blocked")
			}
			return nil
		})
		err = moveOwnedEntry(file, dest, info)
		mustContain(t, err, "foreign object retained")
		mustContain(t, err, "restore blocked")
		if readFile(t, dest) != "occupied" {
			t.Fatal("destination changed")
		}
	})
	t.Run("move destination blocked", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "dest")
		if err := os.WriteFile(dest, []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
		var renames int
		setHook(t, func(op, path string) error {
			if op == "rename" {
				renames++
				if renames >= 3 {
					return errors.New("restore blocked")
				}
			}
			return nil
		})
		err = moveOwnedEntry(file, dest, info)
		mustContain(t, err, "restore failed")
		mustContain(t, err, "restore blocked")
		if readFile(t, dest) != "occupied" {
			t.Fatal("destination changed")
		}
	})
}

func TestCompareGaps(t *testing.T) {
	requireUnprivileged(t)
	write := func(t *testing.T, path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("snapshot mismatches", func(t *testing.T) {
		dir := t.TempDir()
		src, snap := filepath.Join(dir, "src"), filepath.Join(dir, "snap")
		err := verifySnapshot(src, snap)
		mustContain(t, err, "check source")
		write(t, src, "a", 0o644)
		err = verifySnapshot(src, snap)
		mustContain(t, err, "check rollback copy")
		write(t, snap, "ab", 0o644)
		err = verifySnapshot(src, snap)
		mustContain(t, err, "source changed")
		write(t, snap, "b", 0o600)
		err = verifySnapshot(src, snap)
		mustContain(t, err, "source changed")
		write(t, snap, "b", 0o644)
		err = verifySnapshot(src, snap)
		mustContain(t, err, "source changed")
		if err := os.Remove(snap); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("x", snap); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshot(src, snap)
		mustContain(t, err, "source changed")
	})
	t.Run("symlinks directories and fifos", func(t *testing.T) {
		dir := t.TempDir()
		src, snap := filepath.Join(dir, "src"), filepath.Join(dir, "snap")
		if err := os.Symlink("one", src); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("two", snap); err != nil {
			t.Fatal(err)
		}
		err := verifySnapshot(src, snap)
		mustContain(t, err, "source changed")
		if err := os.Remove(snap); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("one", snap); err != nil {
			t.Fatal(err)
		}
		if err := verifySnapshot(src, snap); err != nil {
			t.Fatal(err)
		}
		tree, copyTree := filepath.Join(dir, "tree"), filepath.Join(dir, "tree-copy")
		if err := os.Mkdir(tree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(copyTree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tree, "a"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(copyTree, "b"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshot(tree, copyTree)
		mustContain(t, err, "source changed")
		fifo, fifo2 := filepath.Join(dir, "fifo"), filepath.Join(dir, "fifo2")
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(fifo2, 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshot(fifo, fifo2)
		mustContain(t, err, "unsupported source type")
	})
	t.Run("unreadable directory and file", func(t *testing.T) {
		dir := t.TempDir()
		a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		if err := os.Mkdir(a, 0o755); err != nil || os.Mkdir(b, 0o755) != nil {
			t.Fatal(err)
		}
		restoreMode(t, a, 0o755)
		restoreMode(t, b, 0o755)
		if err := os.Chmod(a, 0); err != nil || os.Chmod(b, 0) != nil {
			t.Fatal(err)
		}
		err := verifySnapshot(a, b)
		mustContain(t, err, "permission denied")
		file, other := filepath.Join(dir, "file"), filepath.Join(dir, "other")
		if err := os.WriteFile(file, []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(other, []byte("same"), 0o644); err != nil {
			t.Fatal(err)
		}
		restoreMode(t, file, 0o644)
		if err := os.Chmod(file, 0); err != nil || os.Chmod(other, 0) != nil {
			t.Fatal(err)
		}
		restoreMode(t, other, 0o644)
		err = verifySnapshot(file, other)
		mustContain(t, err, "permission denied")
	})
	t.Run("published snapshot", func(t *testing.T) {
		dir := t.TempDir()
		raw, published := filepath.Join(dir, "raw"), filepath.Join(dir, "published")
		original := filepath.Join(dir, "original")
		err := verifyPublishedSnapshot(raw, published, original)
		if err == nil {
			t.Fatal("expected missing raw")
		}
		if err := os.WriteFile(raw, []byte("ab"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(published, []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifyPublishedSnapshot(raw, published, original)
		mustContain(t, err, "published copy differs")
		if err := os.WriteFile(published, []byte("cd"), 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifyPublishedSnapshot(raw, published, original)
		mustContain(t, err, "published copy differs")
		if err := os.Remove(raw); err != nil || os.Remove(published) != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("rel", raw); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "rel"), published); err != nil {
			t.Fatal(err)
		}
		if err := verifyPublishedSnapshot(raw, published, original); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(published); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "other"), published); err != nil {
			t.Fatal(err)
		}
		err = verifyPublishedSnapshot(raw, published, original)
		mustContain(t, err, "published copy differs")
		fifo, fifo2 := filepath.Join(dir, "fifo"), filepath.Join(dir, "fifo2")
		if err := syscall.Mkfifo(fifo, 0o644); err != nil || syscall.Mkfifo(fifo2, 0o644) != nil {
			t.Fatal(err)
		}
		err = verifyPublishedSnapshot(fifo, fifo2, original)
		mustContain(t, err, "unsupported snapshot type")
	})
	t.Run("file hash", func(t *testing.T) {
		if _, err := fileHash(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("expected open error")
		}
		dir := t.TempDir()
		if _, err := fileHash(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
			t.Fatalf("read: %v", err)
		}
	})
	t.Run("root snapshots", func(t *testing.T) {
		left, right := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(left, "f"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(right, "f"), []byte("bb"), 0o644); err != nil {
			t.Fatal(err)
		}
		a, b := openRoot(t, left), openRoot(t, right)
		err := verifySnapshots(a, "missing", nil, b, "f", nil)
		if err == nil {
			t.Fatal("expected missing name")
		}
		if err := os.Symlink("x", filepath.Join(left, "link")); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshots(a, "link", nil, b, "f", nil)
		mustContain(t, err, "snapshot mismatch")
		if err := os.Chmod(filepath.Join(right, "f"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(left, "g"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(right, "g"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshots(a, "g", nil, b, "f", nil)
		mustContain(t, err, "snapshot mismatch")
		if err := os.Chmod(filepath.Join(right, "f"), 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshots(a, "f", &fsx.PermissionManifest{}, b, "f", &fsx.PermissionManifest{})
		mustContain(t, err, "copy missing from permission manifest")
		setHook(t, func(op, path string) error {
			if op == "snapshot-open" {
				return errors.New("snapshot open blocked")
			}
			return nil
		})
		err = verifySnapshots(a, "g", nil, b, "g", nil)
		mustContain(t, err, "snapshot open blocked")
	})
	t.Run("snapshot replaced and rechecked", func(t *testing.T) {
		left, right := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(left, "f"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(right, "f"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		a, b := openRoot(t, left), openRoot(t, right)
		setHook(t, func(op, path string) error {
			if op == "snapshot-open" && path == "f" {
				if err := os.Remove(filepath.Join(left, "f")); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(left, "f"), []byte("zz"), 0o644)
			}
			return nil
		})
		err := verifySnapshots(a, "f", nil, b, "f", nil)
		mustContain(t, err, "snapshot mismatch")
		if readFile(t, filepath.Join(left, "f")) != "zz" {
			t.Fatal("replacement lost")
		}
	})
	t.Run("snapshot mtime recheck", func(t *testing.T) {
		left, right := t.TempDir(), t.TempDir()
		if err := os.WriteFile(filepath.Join(left, "f"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(right, "f"), []byte("aa"), 0o644); err != nil {
			t.Fatal(err)
		}
		a, b := openRoot(t, left), openRoot(t, right)
		setHook(t, func(op, path string) error {
			if op == "snapshot-open" {
				future := time.Now().Add(2 * time.Hour)
				return os.Chtimes(filepath.Join(left, path), future, future)
			}
			return nil
		})
		err := verifySnapshots(a, "f", nil, b, "f", nil)
		mustContain(t, err, "snapshot mismatch")
	})
	t.Run("directory and fifo through roots", func(t *testing.T) {
		left, right := t.TempDir(), t.TempDir()
		if err := os.Mkdir(filepath.Join(left, "d"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(right, "d"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(left, "d", "a"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(right, "d", "b"), []byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
		a, b := openRoot(t, left), openRoot(t, right)
		err := verifySnapshots(a, "d", nil, b, "d", nil)
		mustContain(t, err, "snapshot mismatch")
		restoreMode(t, filepath.Join(left, "d"), 0o755)
		restoreMode(t, filepath.Join(right, "d"), 0o755)
		if err := os.Chmod(filepath.Join(left, "d"), 0); err != nil || os.Chmod(filepath.Join(right, "d"), 0) != nil {
			t.Fatal(err)
		}
		err = verifySnapshots(a, "d", nil, b, "d", nil)
		mustContain(t, err, "permission denied")
		if err := syscall.Mkfifo(filepath.Join(left, "fifo"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(filepath.Join(right, "fifo"), 0o644); err != nil {
			t.Fatal(err)
		}
		err = verifySnapshots(a, "fifo", nil, b, "fifo", nil)
		mustContain(t, err, "unsupported snapshot object")
	})
}
