//go:build darwin

package anchor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A failed operation restores the original object, including its metadata.
func TestCrossVolumeRollbackRestoresOriginalObject(t *testing.T) {
	w := newWorld(t)
	source := w.write("file", "valuable")
	if err := unix.Setxattr(source, "com.ianchor.test", []byte("important"), 0); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	firstRename := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && firstRename {
			firstRename = false
			return syscall.EXDEV
		}
		if op == "symlink" && path == source {
			return syscall.EIO
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err == nil {
		t.Fatal("expected failure")
	}
	after, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("original inode not restored")
	}
	buf := make([]byte, 32)
	n, err := unix.Getxattr(source, "com.ianchor.test", buf)
	if err != nil || string(buf[:n]) != "important" {
		t.Fatalf("metadata lost: %q %v", buf[:n], err)
	}
}

func TestCrossVolumeCleanupFailureReportsOriginalStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("file", "valuable")
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		if op == "removeall" && strings.Contains(path, "-source-undo") {
			return syscall.EIO
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	var committed *committedCleanupError
	if !errors.As(err, &committed) {
		t.Fatalf("expected committed cleanup error: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(w.localDir, ".ianchor-tmp-*-source-undo"))
	if err != nil || len(paths) != 1 || !strings.Contains(committed.Error(), paths[0]) {
		t.Fatalf("missing retained path %v: %v", paths, committed)
	}
	if got := readFile(t, paths[0]); got != "valuable" {
		t.Fatalf("staged original: %q", got)
	}
	assertSymlink(t, source, filepath.Join(w.configDir, "item"))
}

func TestCrossVolumeBasicPolicyAllowsExtendedAttributes(t *testing.T) {
	w := newWorld(t)
	source := w.write("file", "valuable")
	const attr = "com.ianchor.test"
	if err := unix.Setxattr(source, attr, []byte("metadata"), 0); err != nil {
		t.Fatalf("set xattr: %v", err)
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err != nil {
		t.Fatalf("basic policy rejected attributed file: %v", err)
	}
	assertSymlink(t, source, filepath.Join(w.configDir, "item"))
	if got := readFile(t, source); got != "valuable" {
		t.Fatalf("content: %q", got)
	}
}

func TestRemoveCrossVolumeFallbackPreservesMetadataOnRollback(t *testing.T) {
	w := newWorld(t)
	source := w.write("local", "valuable")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	if err := unix.Setxattr(cloud, "com.ianchor.test", []byte("important"), 0); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(cloud)
	if err != nil {
		t.Fatal(err)
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == cloud && first {
			first = false
			return syscall.EXDEV
		}
		if op == "db.delete" {
			return syscall.EIO
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Remove(w.db, w.configDir, "item"); err == nil {
		t.Fatal("expected database error")
	}
	after, err := os.Stat(cloud)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("original object not restored")
	}
	buf := make([]byte, 32)
	n, err := unix.Getxattr(cloud, "com.ianchor.test", buf)
	if err != nil || string(buf[:n]) != "important" {
		t.Fatalf("metadata lost: %q %v", buf[:n], err)
	}
}

func TestCrossVolumeBasicPolicyAllowsHardLinks(t *testing.T) {
	w := newWorld(t)
	source := w.write("file", "valuable")
	other := filepath.Join(w.localDir, "other")
	if err := os.Link(source, other); err != nil {
		t.Fatal(err)
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err != nil {
		t.Fatalf("basic policy rejected hard linked file: %v", err)
	}
	assertSymlink(t, source, filepath.Join(w.configDir, "item"))
	if got := readFile(t, other); got != "valuable" {
		t.Fatalf("other hard link content %q", got)
	}
}
