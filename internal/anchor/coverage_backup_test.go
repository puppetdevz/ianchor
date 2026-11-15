package anchor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

func backupFixture(t *testing.T) (configDir string, icloudPath string) {
	t.Helper()
	configDir = filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	icloudPath = filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(icloudPath, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	return configDir, icloudPath
}

func TestBackupToBak(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		configDir, icloudPath := backupFixture(t)
		path, err := backupToBak(configDir, ".zshrc", icloudPath)
		if err != nil {
			t.Fatalf("backupToBak: %v", err)
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("backup missing: %v", err)
		}
	})
	t.Run("attached failure", func(t *testing.T) {
		configDir, icloudPath := backupFixture(t)
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 5 {
				return nil, errSeam
			}
			return realStat(path)
		})
		if _, err := backupToBak(configDir, ".zshrc", icloudPath); err == nil {
			t.Fatal("expected attached failure")
		}
	})
	t.Run("cleanup failure", func(t *testing.T) {
		configDir, icloudPath := backupFixture(t)
		if err := os.Mkdir(filepath.Join(configDir, bakDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, bakDirName, ".zshrc"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		setHook(t, func(op, _ string) error {
			if op == "removeall" {
				return errSeam
			}
			return nil
		})
		if _, err := backupToBak(configDir, ".zshrc", icloudPath); err == nil {
			t.Fatal("expected cleanup failure")
		}
	})
}

func TestCleanupPreviousBackup(t *testing.T) {
	t.Run("attached failure", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		override(t, &statPath, func(string) (os.FileInfo, error) { return nil, errSeam })
		var location *cleanupLocationError
		if err := cleanupPreviousBackup(root, "previous"); err == nil {
			t.Fatal("expected failure")
		} else if !asCleanupLocation(err, &location) {
			t.Fatalf("error %v is not cleanupLocationError", err)
		}
	})
	t.Run("missing identity", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		mustContain(t, cleanupPreviousBackup(root, "previous"), "cannot verify previous backup identity")
	})
	t.Run("removal failure", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		if err := os.WriteFile(filepath.Join(root.display, "previous"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		root.previousIdentities = map[string]os.FileInfo{"previous": fakeInfoFor(t, 1234)}
		mustContain(t, cleanupPreviousBackup(root, "previous"), "backup updated but cleanup failed")
	})
	t.Run("success", func(t *testing.T) {
		root, _ := newBackupFixture(t)
		name := "previous"
		if err := os.WriteFile(filepath.Join(root.display, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(filepath.Join(root.display, name))
		if err != nil {
			t.Fatal(err)
		}
		root.previousIdentities = map[string]os.FileInfo{name: info}
		if err := cleanupPreviousBackup(root, name); err != nil {
			t.Fatalf("cleanupPreviousBackup: %v", err)
		}
	})
}

func asCleanupLocation(err error, target **cleanupLocationError) bool {
	c, ok := err.(*cleanupLocationError)
	if ok {
		*target = c
	}
	return ok
}

func TestStageBackupPreparedFailures(t *testing.T) {
	setup := func(t *testing.T) (*backupRoot, string) {
		t.Helper()
		root, _ := newBackupFixture(t)
		src := filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, src
	}
	t.Run("staging", func(t *testing.T) {
		root, src := setup(t)
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		if _, _, err := stageBackup(root, ".zshrc", src, nil, &journal{}); err == nil {
			t.Fatal("expected staging failure")
		}
	})
	t.Run("previous staging", func(t *testing.T) {
		root, src := setup(t)
		if err := os.WriteFile(filepath.Join(root.display, ".zshrc"), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		realStaging := stagingPath
		calls := 0
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			calls++
			if calls == 2 {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		if _, _, err := stageBackup(root, ".zshrc", src, nil, &journal{}); err == nil {
			t.Fatal("expected staging failure")
		}
	})
	t.Run("previous lstat", func(t *testing.T) {
		root, src := setup(t)
		realLstat := rootLstat
		calls := 0
		override(t, &rootLstat, func(handle *os.Root, name string) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realLstat(handle, name)
		})
		if _, _, err := stageBackup(root, ".zshrc", src, nil, &journal{}); err == nil {
			t.Fatal("expected lstat failure")
		}
	})
	t.Run("manifest rebase", func(t *testing.T) {
		root, src := setup(t)
		manifest := &fsx.PermissionManifest{}
		file, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if err := manifest.Record(".zshrc", file, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := stageBackupPrepared(root, ".zshrc", src, nil, &journal{}, manifest); err == nil {
			t.Fatal("expected rebase failure")
		}
	})
}
