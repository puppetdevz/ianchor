package fsx

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCopyNodeTopLevelSymlinkChain(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	link1 := filepath.Join(base, "link1")
	link2 := filepath.Join(base, "link2")
	if err := os.WriteFile(data, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", link1); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("link1", link2); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := CopyNodeIntoRoot(link2, root, "copy", nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(base, "copy"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("copy: %q %v", got, err)
	}
}

func TestCopyCheckedDirectoryDoesNotReadReplacement(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	other := filepath.Join(base, "other")
	dest := filepath.Join(base, "dest")
	for _, dir := range []string{source, other, dest} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "value"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "value"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = CopyNodeIntoRootChecked(source, root, "result", identity, func(os.FileInfo) {
		if err := os.Rename(source, source+"-saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, source); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(dest, "result", "value"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("read replacement %q", data)
	}
}

func TestCopyRootRegistersCreatedDirectoryBeforeOpenFailure(t *testing.T) {
	if os.Getenv("IANCHOR_TEST_RESTRICT_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCopyRootRegistersCreatedDirectoryBeforeOpenFailure$")
		cmd.Env = append(os.Environ(), "IANCHOR_TEST_RESTRICT_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess: %v\n%s", err, out)
		}
		return
	}
	base := t.TempDir()
	source, dest := filepath.Join(base, "source"), filepath.Join(base, "dest")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	old := syscall.Umask(0777)
	created := false
	err = CopyNodeIntoRoot(source, root, "tree", func(os.FileInfo) { created = true })
	syscall.Umask(old)
	if err == nil {
		t.Fatal("expected inaccessible directory to fail")
	}
	if !created {
		t.Fatal("created root was not registered for rollback")
	}
	if err := root.Remove("tree"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

func TestCopyIntoRootReportsCreatedFileIdentity(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.WriteFile(source, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var reported os.FileInfo
	err = CopyIntoRoot(source, root, "copy", func(info os.FileInfo) {
		reported = info
		if err := os.Rename(filepath.Join(base, "copy"), filepath.Join(base, "saved")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "copy"), []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("copy accepted a replaced destination")
	}
	saved, err := os.Lstat(filepath.Join(base, "saved"))
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := os.Lstat(filepath.Join(base, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	if reported == nil || !os.SameFile(reported, saved) || os.SameFile(reported, foreign) {
		t.Fatal("callback failed to preserve created file identity")
	}
}

func TestCopyNodeIntoRootRejectsEscapingParent(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "root")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := CopyNodeIntoRoot(source, root, "escape/item", nil); err == nil {
		t.Fatal("escaped root")
	}
	if _, err := os.Lstat(filepath.Join(outside, "item")); !os.IsNotExist(err) {
		t.Fatalf("outside modified: %v", err)
	}
}

func TestCopyNodeIntoRootDoesNotFollowReplacedDestination(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	dest := filepath.Join(base, "dest")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "new"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dest, "other"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	err = CopyNodeIntoRoot(source, root, "tree", func(os.FileInfo) {
		if err := os.Rename(filepath.Join(dest, "tree"), filepath.Join(dest, "saved")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("other", filepath.Join(dest, "tree")); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("replaced destination accepted")
	}
	if _, err := os.Lstat(filepath.Join(dest, "other", "new")); !os.IsNotExist(err) {
		t.Fatalf("foreign directory modified: %v", err)
	}
}

func TestCopyNodeIntoRootKeepsLinkSemantics(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	dest := filepath.Join(base, "dest")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	created := false
	if err := CopyNodeIntoRoot(source, root, "tree", func(os.FileInfo) { created = true }); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("missing create notification")
	}
	got, err := os.Readlink(filepath.Join(dest, "tree", "link"))
	if err != nil || got != filepath.Join(source, "file") {
		t.Fatalf("target %q: %v", got, err)
	}
	if err := CopyNodeIntoRoot(source, root, "tree", nil); !os.IsExist(err) {
		t.Fatalf("must refuse existing destination: %v", err)
	}
}
