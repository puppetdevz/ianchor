package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type failReader struct{}

func (failReader) Read(buf []byte) (int, error) {
	return 0, errors.New("rand unavailable")
}

func TestRandSuffixAndStagingFailures(t *testing.T) {
	original := randomReader
	randomReader = failReader{}
	t.Cleanup(func() { randomReader = original })
	if _, err := RandSuffix(); err == nil || !strings.Contains(err.Error(), "rand unavailable") {
		t.Fatalf("RandSuffix err=%v", err)
	}
	if _, err := StagingPath(t.TempDir(), "name"); err == nil {
		t.Fatal("staging accepted a failed suffix")
	}
}

func TestStagingPathTruncatesLongAndInvalidNames(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("a", 90)
	got, err := StagingPath(dir, filepath.Join(dir, long))
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(got)
	if !strings.HasPrefix(base, ".ianchor-tmp-") || strings.Contains(base, long) {
		t.Fatalf("long staging name = %q", base)
	}
	if len(strings.TrimPrefix(base, ".ianchor-tmp-")) > 12+1+80 {
		t.Fatalf("staging name too long: %q", base)
	}

	invalid := strings.Repeat("b", 79) + "\xff" + "tail"
	got, err = StagingPath(dir, invalid)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(filepath.Base(got), '\xff') {
		t.Fatalf("invalid UTF-8 survived truncation: %q", filepath.Base(got))
	}
	if !strings.HasPrefix(filepath.Base(got), ".ianchor-tmp-") {
		t.Fatalf("staging path = %q", got)
	}
}

func TestPermissionManifestErrorBranches(t *testing.T) {
	requireUnprivileged(t)
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("data"), 0o200); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if temporaryMode(info, 0o200)&0o400 == 0 {
		t.Fatal("unreadable file kept no temporary read bit")
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if temporaryMode(info, 0o644) != 0o644 {
		t.Fatalf("readable mode changed to %o", temporaryMode(info, 0o644))
	}
	dirInfo, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	if temporaryMode(dirInfo, 0o500)&0o700 != 0o700 {
		t.Fatal("directory temporary mode dropped owner access")
	}

	var manifest PermissionManifest
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Record(".", f, 0o644); err == nil {
		t.Fatal("recorded an invalid path")
	}
	if err := manifest.Record("file", f, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Record("file", f, 0o644); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate err=%v", err)
	}
	f.Close()
	if err := manifest.Record("closed", f, 0o644); err == nil {
		t.Fatal("recorded a closed file")
	}

	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	fifoFile, err := os.OpenFile(fifo, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer fifoFile.Close()
	if err := manifest.Record("fifo", fifoFile, 0o600); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("fifo record err=%v", err)
	}

	if _, err := manifest.Check("missing", info); err == nil {
		t.Fatal("checked a missing manifest path")
	}
	replaced := filepath.Join(root, "other")
	if err := os.WriteFile(replaced, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	replacedInfo, err := os.Lstat(replaced)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Check("file", replacedInfo); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replaced check err=%v", err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.Check("file", changed); err == nil || !strings.Contains(err.Error(), "mode changed") {
		t.Fatalf("mode check err=%v", err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	restored, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	mode, err := manifest.Check("file", restored)
	if err != nil || mode != 0o644 {
		t.Fatalf("check mode=%o err=%v", mode, err)
	}

	if err := manifest.Rebase("/abs", "next"); err == nil {
		t.Fatal("rebased an absolute path")
	}
	if err := manifest.Rebase("missing", "next"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing rebase err=%v", err)
	}
	if err := manifest.Record("other", mustOpen(t, replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Rebase("file", "other"); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision err=%v", err)
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestPermissionAccessAndRestoreFailures(t *testing.T) {
	requireUnprivileged(t)
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(base, "parent", "child")
	if err := os.WriteFile(child, []byte("child"), 0o200); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	var manifest PermissionManifest
	parentFile, err := root.Open("parent")
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Record("parent", parentFile, 0o100); err != nil {
		t.Fatal(err)
	}
	parentFile.Close()
	if err := os.Chmod(filepath.Join(base, "parent"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(base, "parent"), 0o700) })
	if err := manifest.EnableOwnerAccess(root); err != nil {
		t.Fatalf("enable access: %v", err)
	}
	info, err := os.Lstat(filepath.Join(base, "parent"))
	if err != nil || info.Mode().Perm()&0o700 != 0o700 {
		t.Fatalf("parent mode=%v err=%v", info, err)
	}

	var blocked PermissionManifest
	if err := os.Chmod(child, 0o600); err != nil {
		t.Fatal(err)
	}
	blockedFile, err := os.OpenFile(child, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := blocked.Record("blocked", blockedFile, 0o000); err != nil {
		t.Fatal(err)
	}
	blockedFile.Close()
	if err := os.Chmod(child, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(child, 0o600) })
	childRoot, err := os.OpenRoot(filepath.Dir(child))
	if err != nil {
		t.Fatal(err)
	}
	defer childRoot.Close()
	if err := blocked.EnableOwnerAccess(childRoot); err == nil {
		t.Fatal("enabled access to an unreadable file")
	}

	if err := os.Chmod(child, 0o644); err != nil {
		t.Fatal(err)
	}
	closed, err := os.Open(child)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	if err := PrepareCopyAccess(root, "parent/child", closed, 0o644); err == nil {
		t.Fatal("prepared access from a closed file")
	}

	original := TestPermissionFault
	TestPermissionFault = func(path string) error { return errors.New("restore blocked: " + path) }
	t.Cleanup(func() { TestPermissionFault = original })
	if err := manifest.Restore(root); err == nil || !strings.Contains(err.Error(), "restore blocked") {
		t.Fatalf("restore err=%v", err)
	}
}

func TestVerifyCopyAccessDirectoryShapes(t *testing.T) {
	base := t.TempDir()
	empty := filepath.Join(base, "empty")
	filled := filepath.Join(base, "filled")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filled, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filled, "child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"empty", "filled"} {
		info, err := os.Lstat(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyCopyAccess(root, name, info); err != nil {
			t.Fatalf("%s access: %v", name, err)
		}
	}
	missing, err := os.Lstat(filled)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filled); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopyAccess(root, "filled", missing); err == nil {
		t.Fatal("verified a removed directory")
	}
}

func TestRootCopyErrorBranches(t *testing.T) {
	requireUnprivileged(t)
	base := t.TempDir()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	src := filepath.Join(base, "src")
	if err := os.WriteFile(src, []byte("root"), 0o640); err != nil {
		t.Fatal(err)
	}
	created := false
	if err := CopyNodePreserveModesIntoRoot(src, root, "preserved", func(os.FileInfo) { created = true }); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("root copy did not report creation")
	}
	got, err := os.ReadFile(filepath.Join(base, "preserved"))
	if err != nil || string(got) != "root" {
		t.Fatalf("preserved=%q %v", got, err)
	}

	if err := CopyNodeIntoRootPrepared(src, root, "prepared", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "permission manifest") {
		t.Fatalf("nil manifest err=%v", err)
	}

	original := TestCopyFault
	TestCopyFault = func(src, dst string) error { return errors.New("fault " + filepath.Base(dst)) }
	t.Cleanup(func() { TestCopyFault = original })
	if err := CopyNodeIntoRoot(src, root, "faulty", nil); err == nil || !strings.Contains(err.Error(), "fault faulty") {
		t.Fatalf("fault err=%v", err)
	}
	TestCopyFault = func(string, string) error { return nil }
	if err := CopyNodeIntoRoot(src, root, "fault-pass", nil); err != nil {
		t.Fatal(err)
	}
	TestCopyFault = nil

	if err := CopyNodeIntoRoot(filepath.Join(base, "missing"), root, "missing", nil); err == nil {
		t.Fatal("copied a missing root source")
	}

	other := filepath.Join(base, "other")
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(other)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "srcdir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRootChecked(dir, root, "replaced-dir", expected, nil); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("replaced directory err=%v", err)
	}

	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink("missing", dangling); err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRoot(dangling, root, "dangling-copy", nil); err == nil {
		t.Fatal("copied a dangling symlink into root")
	}
	if err := os.Symlink("srcdir", filepath.Join(base, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRoot(filepath.Join(base, "dirlink"), root, "dirlink-copy", nil); err == nil || !strings.Contains(err.Error(), "unsupported non-regular") {
		t.Fatalf("directory link err=%v", err)
	}

	linkCreated := false
	if err := CopyIntoRoot(dangling, root, "link-text", func(os.FileInfo) { linkCreated = true }); err != nil {
		t.Fatal(err)
	}
	if !linkCreated {
		t.Fatal("symlink creation was not reported")
	}
	target, err := os.Readlink(filepath.Join(base, "link-text"))
	if err != nil || target != "missing" {
		t.Fatalf("link target=%q err=%v", target, err)
	}
	if err := CopyIntoRoot(dangling, root, "link-text", nil); err == nil {
		t.Fatal("created a duplicate symlink")
	}

	unreadableDir := filepath.Join(base, "closed-dir")
	if err := os.Mkdir(unreadableDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadableDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadableDir, 0o700) })
	if err := CopyIntoRoot(unreadableDir, root, "closed-copy", nil); err == nil {
		t.Fatal("opened an unreadable directory")
	}

	fifo := filepath.Join(base, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CopyIntoRoot(fifo, root, "pipe-copy", nil); err == nil || !strings.Contains(err.Error(), "unsupported special file") {
		t.Fatalf("fifo err=%v", err)
	}

	locked := filepath.Join(base, "locked")
	if err := os.WriteFile(locked, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o600) })
	lockedInfo, err := os.Lstat(locked)
	if err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRootChecked(locked, root, "locked-copy", lockedInfo, nil); err == nil {
		t.Fatal("opened an unreadable file into root")
	}

	wrong, err := os.Lstat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRootChecked(other, root, "wrong-identity", wrong, nil); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("wrong identity err=%v", err)
	}
}

func TestRootRelativeSymlinkNeedsWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.Mkdir(src, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(src, "rel")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(base, "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		os.Chdir(cwd)
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	original := workingDirectory
	workingDirectory = func() (string, error) { return "", errors.New("no working directory") }
	t.Cleanup(func() { workingDirectory = original })
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRoot("src", root, "abs-fail", nil); err == nil || !strings.Contains(err.Error(), "no working directory") {
		t.Fatalf("relative symlink err=%v", err)
	}
}
