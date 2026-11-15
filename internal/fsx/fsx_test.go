package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStagingPath(t *testing.T) {
	dir := t.TempDir()
	path, err := StagingPath(dir, filepath.Join("/home/x", ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("staging dir = %q", filepath.Dir(path))
	}
	if base := filepath.Base(path); base[:len(".ianchor-tmp-")] != ".ianchor-tmp-" {
		t.Fatalf("staging name = %q", base)
	}
	if got := filepath.Base(path)[len(".ianchor-tmp-")+12:]; got != "-.zshrc" {
		t.Fatalf("staging suffix = %q", got)
	}
}

func TestPredicatesDistinguishSymlinkTargets(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "dir")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "live")
	if err := os.Symlink(file, live); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "absent"), dangling); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	if !Exists(file) || !Exists(live) || Exists(dangling) || Exists(missing) {
		t.Fatal("Exists must follow symlinks and report dangling links as absent")
	}
	if !Lexists(dangling) || Lexists(live) == false || Lexists(missing) {
		t.Fatal("Lexists must report dangling links as present")
	}
	if !IsDir(subdir) || IsDir(file) || IsDir(missing) {
		t.Fatal("IsDir must only accept directories")
	}
	if !IsSymlink(dangling) || IsSymlink(file) || IsSymlink(missing) {
		t.Fatal("IsSymlink must only accept links, including dangling ones")
	}
}

func TestRelativeSymlinkResolvesFromWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o700); err != nil {
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
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err := CopyNodeIntoRoot("src", dir, "dst", nil); err != nil {
		t.Fatal(err)
	}
	got, err := dir.Readlink(filepath.Join("dst", "link"))
	if err != nil || !filepath.IsAbs(got) || !strings.HasSuffix(got, "/src/target") {
		t.Fatalf("link=%q err=%v", got, err)
	}
}
