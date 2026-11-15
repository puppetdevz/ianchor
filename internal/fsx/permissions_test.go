package fsx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedCopyTemporarilyOpensOwnerAccess(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.WriteFile(source, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// The copy records final modes before comparison.
	var manifest PermissionManifest
	defer manifest.Close()
	if err := CopyNodeIntoRootPrepared(source, root, "copy", nil, &manifest, nil); err != nil {
		t.Fatal(err)
	}
	mode, ok := manifest.Mode("copy")
	if !ok || mode != 0600 {
		t.Fatalf("mode %o, present %v", mode, ok)
	}
	if err := manifest.Restore(root); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedCopyManyReadOnlyDirectories(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		p := filepath.Join(source, fmt.Sprintf("%03d", i))
		if err := os.Mkdir(p, 0500); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var m PermissionManifest
	defer m.Close()
	if err := CopyNodeIntoRootPrepared(source, root, "copy", nil, &m, nil); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(root); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyAccessible(root); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(base, "copy", "299")); err != nil || info.Mode().Perm() != 0500 {
		t.Fatalf("last mode: %v %v", info, err)
	}
}

func TestPermissionManifestRebasesRenamedCopy(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "stage"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.Open("stage")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var m PermissionManifest
	defer m.Close()
	if err := m.Record("stage", f, 0044); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename("stage", "final"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rebase("stage", "final"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(root); err != nil {
		t.Fatal(err)
	}
	if info, err := f.Stat(); err != nil || info.Mode().Perm() != 0044 {
		t.Fatalf("mode %v %v", info, err)
	}
}

func TestPermissionManifestRestoresChildBeforeParent(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "tree", "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dir, err := root.Open("tree")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	file, err := root.Open("tree/file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var manifest PermissionManifest
	defer manifest.Close()
	if err := manifest.Record("tree", dir, 0001); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Record("tree/file", file, 0044); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Restore(root); err != nil {
		t.Fatal(err)
	}
	if info, err := file.Stat(); err != nil || info.Mode().Perm() != 0044 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	if info, err := dir.Stat(); err != nil || info.Mode().Perm() != 0001 {
		t.Fatalf("dir mode: %v %v", info, err)
	}
	if err := dir.Chmod(0700); err != nil {
		t.Fatal(err)
	}
}

func TestPermissionManifestDoesNotChmodReplacement(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "file")
	if err := os.WriteFile(path, []byte("ours"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.Open("file")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var manifest PermissionManifest
	defer manifest.Close()
	if err := manifest.Record("file", f, 0044); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-ours"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Restore(root); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("expected replacement error: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("replacement mode: %v %v", info, err)
	}
}

func TestVerifyAccessibleRequiresDirectorySearch(t *testing.T) {
	requireUnprivileged(t)
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "tree", "item"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dir, err := root.Open("tree")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	var manifest PermissionManifest
	defer manifest.Close()
	if err := manifest.Record("tree", dir, 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(base, "tree"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := manifest.VerifyAccessible(root); err == nil {
		t.Fatal("directory without search accepted")
	}
	if err := os.Chmod(filepath.Join(base, "tree"), 0700); err != nil {
		t.Fatal(err)
	}
}
