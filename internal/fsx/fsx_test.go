package fsx

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCopyRefusesExistingDestination(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []bool{false, true} {
		src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
		if directory {
			if err := os.Mkdir(src, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dst, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, "existing"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(src, []byte("source"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		created := false
		if err := CopyTracked(src, dst, func() { created = true }); err == nil {
			t.Fatal("copy accepted existing destination")
		}
		if created {
			t.Fatal("existing destination claimed by operation")
		}
		check := dst
		if directory {
			check = filepath.Join(dst, "existing")
		}
		data, err := os.ReadFile(check)
		if err != nil || string(data) != "keep" {
			t.Fatalf("destination changed: %q %v", data, err)
		}
		if err := os.RemoveAll(src); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dst); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopyDirectoryWithSymlink(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "copy")
	if err := Copy(source, target); err != nil {
		t.Fatalf("copy: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "file.txt"))
	if err != nil || string(data) != "data" {
		t.Fatalf("file = %q %v", data, err)
	}
	info, err := os.Stat(filepath.Join(target, "file.txt"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v %v", info.Mode(), err)
	}
	if link, err := os.Readlink(filepath.Join(target, "link")); err != nil || link != "/etc/hosts" {
		t.Fatalf("symlink = %q %v", link, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "nested", "deep.txt")); err != nil || string(data) != "deep" {
		t.Fatalf("nested = %q %v", data, err)
	}
}

func TestCopyNodeResolvesNestedRelativeSymlink(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../target", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "destination")
	if err := CopyNode(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil || got != filepath.Join(root, "target") {
		t.Fatalf("got %q: %v", got, err)
	}
}

func TestCopyReadOnlyDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "item"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source, 0700); _ = os.Chmod(filepath.Join(root, "dst"), 0700) })
	dest := filepath.Join(root, "dst")
	if err := Copy(source, dest); err != nil {
		t.Fatalf("copy read-only directory: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Mode().Perm() != 0500 {
		t.Fatalf("directory permissions: %v %v", info, err)
	}
}

func TestCopyRejectsFIFO(t *testing.T) {
	requireUnprivileged(t)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Copy(fifo, filepath.Join(dir, "copy")) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO copied without error")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO copy blocked")
	}
}

func TestCopyFile(t *testing.T) {
	requireUnprivileged(t)
	source := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(source, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "g")
	if err := Copy(source, target); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "payload" {
		t.Fatalf("target = %q %v", data, err)
	}
	if err := os.Chmod(source, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(source, 0o600) })
	if err := Copy(source, filepath.Join(t.TempDir(), "blocked")); err == nil {
		t.Fatal("unreadable source copied")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := CopyNode(link, filepath.Join(t.TempDir(), "from-link")); err == nil {
		t.Fatal("unreadable symlink target copied")
	}
	dangling := filepath.Join(t.TempDir(), "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := CopyNode(dangling, filepath.Join(t.TempDir(), "resolved")); err == nil {
		t.Fatal("dangling symlink resolved")
	}
}

func TestMoveAndPredicates(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Exists(source) || !Lexists(source) || IsDir(source) || IsSymlink(source) {
		t.Fatal("predicate mismatch")
	}
	target := filepath.Join(dir, "b")
	if crossed, err := Move(source, target); err != nil || crossed {
		t.Fatalf("move: %v %v", err, crossed)
	}
	if Exists(source) || !Exists(target) {
		t.Fatal("move did not relocate")
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	if Exists(filepath.Join(dir, "dangling")) || !Lexists(filepath.Join(dir, "dangling")) {
		t.Fatal("dangling symlink predicates are inverted")
	}
	if !IsSymlink(filepath.Join(dir, "dangling")) || IsDir(filepath.Join(dir, "dangling")) {
		t.Fatal("dangling symlink type mismatch")
	}
	if err := os.MkdirAll(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsDir(filepath.Join(dir, "d")) {
		t.Fatal("directory not detected")
	}
}

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
