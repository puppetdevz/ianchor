package anchor

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateOriginalAncestorErrors(t *testing.T) {
	requireUnprivileged(t)
	configDir := t.TempDir()
	config, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("resolved ancestor overlaps", func(t *testing.T) {
		sub := filepath.Join(config, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(sub, link); err != nil {
			t.Fatal(err)
		}
		original := filepath.Join(link, "child")
		mustContain(t, validateOriginal(configDir, &original), "overlaps the iCloud config directory")
	})
	t.Run("unresolvable ancestor", func(t *testing.T) {
		directory := t.TempDir()
		original := filepath.Join(directory, "child")
		realEval := evalSymlinksPath
		override(t, &evalSymlinksPath, func(path string) (string, error) {
			if path == directory {
				return "", &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrPermission}
			}
			return realEval(path)
		})
		mustContain(t, validateOriginal(configDir, &original), "permission denied")
	})
	t.Run("dangling parent lstat", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "gone")
		original := filepath.Join(directory, "child")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == directory {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, validateOriginal(configDir, &original), errSeam.Error())
	})
	t.Run("root ancestor", func(t *testing.T) {
		realEval := evalSymlinksPath
		override(t, &evalSymlinksPath, func(path string) (string, error) {
			if path == "/" {
				return "", &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
			}
			return realEval(path)
		})
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == "/" {
				return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
			}
			return realLstat(path)
		})
		original := "/missing-root-child/child"
		mustContain(t, validateOriginal(configDir, &original), "cannot resolve existing ancestor")
	})
}

func TestIdentityOverlapBranches(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "cloud")
	if err := os.Mkdir(config, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("followed final", func(t *testing.T) {
		overlap, err := identityOverlap(config, config, true)
		if err != nil || !overlap {
			t.Fatalf("identityOverlap: %v %v", overlap, err)
		}
	})
	t.Run("ancestor stat", func(t *testing.T) {
		candidate := filepath.Join(root, "local")
		if err := os.Mkdir(candidate, 0o755); err != nil {
			t.Fatal(err)
		}
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realStat(path)
		})
		if _, err := identityOverlap(config, candidate, false); err == nil {
			t.Fatal("expected ancestor stat failure")
		}
	})
	t.Run("missing ancestor stat", func(t *testing.T) {
		candidate := filepath.Join(t.TempDir(), "missing", "child")
		realStat := statPath
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			if path == filepath.Dir(candidate) {
				return nil, errSeam
			}
			return realStat(path)
		})
		if _, err := identityOverlap(config, candidate, false); err == nil {
			t.Fatal("expected ancestor stat failure")
		}
	})
}

func TestEnsureDirErrors(t *testing.T) {
	t.Run("no existing ancestor", func(t *testing.T) {
		override(t, &statPath, func(string) (os.FileInfo, error) {
			return nil, &fs.PathError{Op: "stat", Err: fs.ErrNotExist}
		})
		if _, err := ensureDir(filepath.Join(string(filepath.Separator), "missing")); err == nil {
			t.Fatal("expected ancestor failure")
		}
	})
	t.Run("mkdir failure", func(t *testing.T) {
		base := t.TempDir()
		override(t, &mkdirPath, func(string, os.FileMode) error { return errSeam })
		_, err := ensureDir(filepath.Join(base, "a", "b"))
		mustContain(t, err, errSeam.Error())
	})
	t.Run("created lstat", func(t *testing.T) {
		base := t.TempDir()
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == filepath.Join(base, "a") {
				return nil, errSeam
			}
			return realLstat(path)
		})
		_, err := ensureDir(filepath.Join(base, "a"))
		mustContain(t, err, "created directory may remain")
	})
	t.Run("rollback staging", func(t *testing.T) {
		base := t.TempDir()
		created, err := ensureDir(filepath.Join(base, "a"))
		if err != nil {
			t.Fatal(err)
		}
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		mustContain(t, removeCreatedDirs(created), errSeam.Error())
	})
	t.Run("replaced before restore", func(t *testing.T) {
		base := t.TempDir()
		created, err := ensureDir(filepath.Join(base, "a"))
		if err != nil {
			t.Fatal(err)
		}
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if base := filepath.Base(path); base != "a" && len(base) > 2 && base[len(base)-2:] == "up" {
				return fakeInfoFor(t, 4321), nil
			}
			return realLstat(path)
		})
		mustContain(t, removeCreatedDirs(created), "created directory was replaced")
	})
}
