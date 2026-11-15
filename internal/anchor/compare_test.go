package anchor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

func TestVerifySnapshotBasicDifferences(t *testing.T) {
	cases := []struct {
		name  string
		alter func(*testing.T, string)
	}{
		{"same length content", func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "file"), "other") }},
		{"ordinary permissions", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "file"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing entry", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "file")); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra entry", func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "extra"), "data") }},
		{"changed link", func(t *testing.T, root string) {
			path := filepath.Join(root, "link")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("other", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"changed type", func(t *testing.T, root string) {
			path := filepath.Join(root, "file")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "source")
			snapshot := filepath.Join(base, "snapshot")
			if err := os.Mkdir(source, 0750); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(source, "file"), "hello")
			if err := os.Chmod(filepath.Join(source, "file"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := fsx.CopyIntoRoot(source, root, "snapshot", nil); err != nil {
				t.Fatal(err)
			}
			if err := verifySnapshot(source, snapshot); err != nil {
				t.Fatalf("baseline: %v", err)
			}
			tc.alter(t, snapshot)
			if err := verifySnapshot(source, snapshot); err == nil {
				t.Fatal("difference was accepted")
			}
		})
	}
}
