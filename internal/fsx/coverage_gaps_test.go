package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClosedFileCannotBeCopiedIntoRoot(t *testing.T) {
	base := t.TempDir()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	in, err := os.Open(base)
	if err != nil {
		t.Fatal(err)
	}
	in.Close()
	if err := copyOpenedFileIntoRoot(in, base, root, "out", 0o644, nil, nil, "out"); err == nil {
		t.Fatal("copied from a closed file")
	}
}

func TestRootSymlinkReportsCreatedInfo(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "link")
	if err := os.Symlink("target", src); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var info os.FileInfo
	if err := CopyIntoRoot(src, root, "copy", func(fi os.FileInfo) { info = fi }); err != nil {
		t.Fatal(err)
	}
	if info == nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("created info=%v", info)
	}
	if got, err := root.Readlink("copy"); err != nil || got != "target" {
		t.Fatalf("link=%q err=%v", got, err)
	}
}

func TestClosedSourceParentCannotCopy(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	parent, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	parent.Close()
	dest, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	err = copyFromRoot(parent, "file", filepath.Join(base, "file"), dest, "file", true, false, false, nil, nil, nil, "file")
	if err == nil {
		t.Fatal("closed source parent copied a file")
	}
}

func TestManifestOrdersSiblingsAndRejectsImmutableChmod(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"b", "a"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var manifest PermissionManifest
	for _, name := range []string{"b", "a"} {
		f, err := root.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := manifest.Record(name, f, 0o644); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	got := manifest.paths(true)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("order=%v", got)
	}
	setImmutablePath(t, filepath.Join(base, "a"))
	if err := manifest.EnableOwnerAccess(root); err == nil {
		t.Fatal("changed the mode of an immutable file")
	}
}

func TestPrepareCopyAccessRejectsImmutableFile(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	setImmutablePath(t, path)
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := PrepareCopyAccess(root, "file", f, 0o600); err == nil {
		t.Fatal("prepared access on an immutable file")
	}
}

func TestDuplicateManifestPathRejectsSecondRecord(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.Open("src")
	if err != nil {
		t.Fatal(err)
	}
	var manifest PermissionManifest
	if err := manifest.Record("copy", f, 0o644); err != nil {
		t.Fatal(err)
	}
	f.Close()
	err = CopyNodeIntoRootPrepared(src, root, "copy", nil, &manifest, nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate record err=%v", err)
	}
}
