package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelativeSymlinkResolvesFromWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "target"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "src", "link")); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if err := CopyNode("src", "dst"); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join("dst", "link"))
	if err != nil || !filepath.IsAbs(got) || !strings.HasSuffix(got, "/src/target") {
		t.Fatalf("link=%q err=%v", got, err)
	}
}

func TestTrackedCopyReportsEveryCreatedNode(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "file"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	fileCreated := false
	if err := CopyNodeTracked(filepath.Join(src, "file"), filepath.Join(root, "file-copy"), func() { fileCreated = true }); err != nil {
		t.Fatal(err)
	}
	linkCreated := false
	if err := CopyTracked(filepath.Join(src, "link"), filepath.Join(root, "link-copy"), func() { linkCreated = true }); err != nil {
		t.Fatal(err)
	}
	if !fileCreated || !linkCreated {
		t.Fatalf("fileCreated=%v linkCreated=%v", fileCreated, linkCreated)
	}
	if data, err := os.ReadFile(filepath.Join(root, "file-copy")); err != nil || string(data) != "a" {
		t.Fatalf("file copy=%q err=%v", data, err)
	}
	if got, err := os.Readlink(filepath.Join(root, "link-copy")); err != nil || got != "file" {
		t.Fatalf("link=%q err=%v", got, err)
	}
}

func TestCopyDirectoryOntoExistingFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyNode(src, dst); err == nil {
		t.Fatal("copied a directory onto an existing file")
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "x" {
		t.Fatalf("destination changed: %q err=%v", data, err)
	}
}

func TestCopyFileRejectsDirectoryContents(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "dir")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	called := false
	err := copyFile(src, filepath.Join(root, "out"), 0o644, func() { called = true })
	if err == nil || !called {
		t.Fatalf("err=%v called=%v", err, called)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "out")); statErr != nil {
		t.Fatalf("partial copy missing: %v", statErr)
	}
}

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
