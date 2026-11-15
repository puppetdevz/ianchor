package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// override replaces a seam for one test and restores it afterwards.
func override[T any](t *testing.T, seam *T, replacement T) {
	t.Helper()
	original := *seam
	*seam = replacement
	t.Cleanup(func() { *seam = original })
}

// fakeFileInfo reports a chosen identity and mode for branches that compare an
// observed inode with an expected one.
type fakeFileInfo struct {
	name string
	mode os.FileMode
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

// newCopyRoot returns a temporary directory and an opened root for it.
func newCopyRoot(t *testing.T) (string, *os.Root) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return dir, root
}

// writeTree creates the named files and directories below dir.
func writeTree(t *testing.T, dir string, files []string, dirs []string) {
	t.Helper()
	for _, name := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCopyResolvedSymlinkTargetStatFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"real"}, nil)
	link := filepath.Join(srcDir, "link")
	if err := os.Symlink(filepath.Join(srcDir, "real"), link); err != nil {
		t.Fatal(err)
	}
	override(t, &statResolvedTarget, func(string) (os.FileInfo, error) { return nil, os.ErrPermission })
	_, root := newCopyRoot(t)
	if err := CopyNodeIntoRoot(link, root, "copy", nil); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("rooted copy err=%v", err)
	}
}

func TestCopyReadlinkFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"real"}, nil)
	link := filepath.Join(srcDir, "link")
	if err := os.Symlink("real", link); err != nil {
		t.Fatal(err)
	}
	failing := errors.New("readlink rejected")
	override(t, &readlinkPath, func(string) (string, error) { return "", failing })
	_, root := newCopyRoot(t)
	if err := CopyIntoRoot(link, root, "copy", nil); !errors.Is(err, failing) {
		t.Fatalf("rooted copy err=%v", err)
	}
}

func TestRootedCopyDirectorySourceOpenFailure(t *testing.T) {
	requireUnprivileged(t)
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	if err := os.Chmod(filepath.Join(srcDir, "d"), 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(srcDir, "d"), 0o755) })
	_, root := newCopyRoot(t)
	if err := CopyIntoRoot(filepath.Join(srcDir, "d"), root, "copy", nil); err == nil {
		t.Fatal("read-only directory copy succeeded")
	}
}

func TestRootedCopyDirectoryIdentityFailures(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("stat rejected")
	override(t, &statSourceDirIdentity, func(*os.File) (os.FileInfo, error) { return nil, rejected })
	dir, root := newCopyRoot(t)
	if err := CopyIntoRoot(source, root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("identity error=%v", err)
	}
	assertCopyRootEmpty(t, dir)
	override(t, &statSourceDirIdentity, func(*os.File) (os.FileInfo, error) {
		return fakeFileInfo{name: "d", mode: os.ModeDir | 0o755}, nil
	})
	if err := CopyIntoRoot(source, root, "other", nil); err == nil {
		t.Fatal("replaced source directory accepted")
	}
}

func TestRootedCopyCreatedDirectoryVerification(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("lstat rejected")
	override(t, &rootLstatCreatedDir, func(*os.Root, string) (os.FileInfo, error) { return nil, rejected })
	_, root := newCopyRoot(t)
	if err := CopyIntoRoot(source, root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("verification error=%v", err)
	}
	override(t, &rootLstatCreatedDir, func(*os.Root, string) (os.FileInfo, error) {
		return fakeFileInfo{name: "copy", mode: 0o644}, nil
	})
	if err := CopyIntoRoot(source, root, "other", nil); err == nil {
		t.Fatal("replaced destination accepted")
	}
}

func TestRootedCopyCreatedSymlinkVerification(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, nil)
	link := filepath.Join(srcDir, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("lstat rejected")
	override(t, &rootLstatCreatedSymlink, func(*os.Root, string) (os.FileInfo, error) { return nil, rejected })
	dir, root := newCopyRoot(t)
	err := CopyIntoRoot(link, root, "copy", func(os.FileInfo) {})
	if !errors.Is(err, rejected) {
		t.Fatalf("symlink verification error=%v", err)
	}
	assertReportedResidue(t, dir, err, "copy")
}

func TestRootedCopyDestinationOpenFailures(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("open rejected")
	_, root := newCopyRoot(t)

	override(t, &openChildRoot, func(*os.Root, string) (*os.Root, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("child root error=%v", err)
	}
	override(t, &openChildRoot, func(r *os.Root, name string) (*os.Root, error) { return r.OpenRoot(name) })
	override(t, &openChildDot, func(*os.Root, string) (*os.File, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "other", nil); !errors.Is(err, rejected) {
		t.Fatalf("child dot error=%v", err)
	}
	override(t, &openChildDot, func(r *os.Root, name string) (*os.File, error) { return r.Open(name) })
	override(t, &statPinnedDestination, func(*os.File) (os.FileInfo, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "third", nil); !errors.Is(err, rejected) {
		t.Fatalf("pinned stat error=%v", err)
	}
}

func TestRootedCopyNestedSourceOpenFailures(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d", "d/sub", "d/sub/inner"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("open rejected")
	_, root := newCopyRoot(t)

	override(t, &openSourceParentDot, func(*os.Root, string) (*os.File, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("source parent error=%v", err)
	}
	override(t, &openSourceParentDot, func(r *os.Root, name string) (*os.File, error) { return r.Open(name) })
	override(t, &openSourceRootNested, func(*os.Root, string) (*os.Root, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "other", nil); !errors.Is(err, rejected) {
		t.Fatalf("nested source root error=%v", err)
	}
	override(t, &openSourceRootNested, func(r *os.Root, name string) (*os.Root, error) { return r.OpenRoot(name) })
	override(t, &openSourceDot, func(*os.Root, string) (*os.File, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "third", nil); !errors.Is(err, rejected) {
		t.Fatalf("source dot error=%v", err)
	}
	override(t, &openSourceDot, func(r *os.Root, name string) (*os.File, error) { return r.Open(name) })
	override(t, &statPinnedSource, func(*os.File) (os.FileInfo, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "fourth", nil); !errors.Is(err, rejected) {
		t.Fatalf("pinned source error=%v", err)
	}
}

func TestRootedCopyTopSourceRootFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d", "d/sub"})
	rejected := errors.New("root rejected")
	override(t, &openRootPath, func(string) (*os.Root, error) { return nil, rejected })
	_, root := newCopyRoot(t)
	if err := CopyIntoRoot(filepath.Join(srcDir, "d"), root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("source root error=%v", err)
	}
}

func TestRootedCopyNestedRegularOpenFailures(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"d/f"}, []string{"d"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("open rejected")
	_, root := newCopyRoot(t)

	override(t, &openRegularParentDot, func(*os.Root, string) (*os.File, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("regular parent error=%v", err)
	}
	override(t, &openRegularParentDot, func(r *os.Root, name string) (*os.File, error) { return r.Open(name) })
	override(t, &openatPath, func(int, string, int, uint32) (int, error) { return -1, rejected })
	if err := CopyIntoRoot(source, root, "other", nil); !errors.Is(err, rejected) {
		t.Fatalf("openat error=%v", err)
	}
	override(t, &openatPath, unix.Openat)
	override(t, &statRegularSource, func(*os.File) (os.FileInfo, error) { return nil, rejected })
	if err := CopyIntoRoot(source, root, "third", nil); !errors.Is(err, rejected) {
		t.Fatalf("regular stat error=%v", err)
	}
	override(t, &statRegularSource, func(*os.File) (os.FileInfo, error) {
		return fakeFileInfo{name: "f", mode: 0o644}, nil
	})
	if err := CopyIntoRoot(source, root, "fourth", nil); err == nil {
		t.Fatal("replaced regular source accepted")
	}
}

func TestRootedCopyRegularSourceOpenFailure(t *testing.T) {
	requireUnprivileged(t)
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"f"}, nil)
	file := filepath.Join(srcDir, "f")
	if err := os.Chmod(file, 0o000); err != nil {
		t.Fatal(err)
	}
	_, root := newCopyRoot(t)
	if err := CopyIntoRoot(file, root, "copy", nil); err == nil {
		t.Fatal("unreadable source copied")
	}
}

func TestRootedCopyCreatedFileVerification(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"f"}, nil)
	file := filepath.Join(srcDir, "f")
	rejected := errors.New("stat rejected")

	override(t, &statCreatedFile, func(*os.File) (os.FileInfo, error) { return nil, rejected })
	dir, root := newCopyRoot(t)
	createErr := CopyIntoRoot(file, root, "copy", nil)
	if !errors.Is(createErr, rejected) {
		t.Fatalf("created file error=%v", createErr)
	}
	assertReportedResidue(t, dir, createErr, "copy")
	override(t, &statCreatedFile, func(f *os.File) (os.FileInfo, error) { return f.Stat() })
	override(t, &chmodCreatedFileFinal, func(*os.File, os.FileMode) error { return rejected })
	if err := CopyIntoRoot(file, root, "other", nil); !errors.Is(err, rejected) {
		t.Fatalf("created file chmod error=%v", err)
	}
	override(t, &chmodCreatedFileFinal, func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) })
	override(t, &statFinalMode, func(*os.File) (os.FileInfo, error) { return fakeFileInfo{name: "copy", mode: 0o600}, nil })
	if err := CopyIntoRoot(file, root, "third", nil); err == nil {
		t.Fatal("temporary mode mismatch accepted")
	}
}

func TestRootedCopyManifestFailures(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("access rejected")
	_, root := newCopyRoot(t)

	override(t, &manifestOpen, func(*os.Root, string, int, os.FileMode) (*os.File, error) { return nil, rejected })
	if err := CopyNodeIntoRootPrepared(source, root, "copy", nil, &PermissionManifest{}, nil); !errors.Is(err, rejected) {
		t.Fatalf("prepared access error=%v", err)
	}
	override(t, &manifestOpen, func(r *os.Root, path string, flags int, perm os.FileMode) (*os.File, error) {
		return r.OpenFile(path, flags, perm)
	})
	if err := CopyIntoRoot(source, root, "duplicate", nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.Open(filepath.Join(root.Name(), "duplicate"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { snapshot.Close() })
	manifest := &PermissionManifest{}
	if err := manifest.Record("second", snapshot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopyNodeIntoRootPrepared(source, root, "second", nil, manifest, nil); err == nil {
		t.Fatal("duplicate manifest path accepted")
	}
}

func TestRootedCopyPreparedDirectoryIdentityMode(t *testing.T) {
	requireUnprivileged(t)
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	source := filepath.Join(srcDir, "d")
	rejected := errors.New("mode rejected")
	_, root := newCopyRoot(t)

	override(t, &chmodPinnedTemporary, func(*os.File, os.FileMode) error { return rejected })
	if err := CopyNodeIntoRootPrepared(source, root, "copy", nil, &PermissionManifest{}, nil); !errors.Is(err, rejected) {
		t.Fatalf("temporary chmod error=%v", err)
	}
	override(t, &chmodPinnedTemporary, func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) })
	override(t, &statPinnedMode, func(*os.File) (os.FileInfo, error) { return fakeFileInfo{name: "copy", mode: os.ModeDir | 0o500}, nil })
	if err := CopyNodeIntoRootPrepared(source, root, "other", nil, &PermissionManifest{}, nil); err == nil {
		t.Fatal("temporary mode mismatch accepted")
	}
}

func TestRootedCopySourceListingFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	rejected := errors.New("readdir rejected")
	override(t, &readSearchDir, func(*os.File, int) ([]os.DirEntry, error) { return nil, rejected })
	_, root := newCopyRoot(t)
	if err := CopyIntoRoot(filepath.Join(srcDir, "d"), root, "copy", nil); !errors.Is(err, rejected) {
		t.Fatalf("listing error=%v", err)
	}
}

func TestRootedCopyFinalDirectoryChmodFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, nil, []string{"d"})
	rejected := errors.New("chmod rejected")
	override(t, &chmodPinnedFinal, func(*os.File, os.FileMode) error { return rejected })
	dir, root := newCopyRoot(t)
	err := CopyIntoRoot(filepath.Join(srcDir, "d"), root, "copy", nil)
	if !errors.Is(err, rejected) {
		t.Fatalf("final directory chmod error=%v", err)
	}
	assertResiduePresent(t, dir, "copy")
}

func TestOpenManifestNodeSearchOnlyFallback(t *testing.T) {
	dir, root := newCopyRoot(t)
	writeTree(t, dir, []string{"node/child"}, []string{"node"})
	identity, err := os.Lstat(filepath.Join(dir, "node"))
	if err != nil {
		t.Fatal(err)
	}
	permErr := &os.PathError{Op: "open", Path: "node", Err: syscall.EPERM}
	calls := 0
	override(t, &manifestOpen, func(r *os.Root, path string, flags int, perm os.FileMode) (*os.File, error) {
		calls++
		if calls == 1 {
			return nil, permErr
		}
		return r.OpenFile(path, flags, perm)
	})
	if _, err := openManifestNode(root, "node", identity); err != nil {
		t.Fatalf("fallback error=%v", err)
	}
	if calls != 2 {
		t.Fatalf("open calls=%d", calls)
	}
}

func TestVerifyCopyAccessDescriptorFailures(t *testing.T) {
	dir, root := newCopyRoot(t)
	writeTree(t, dir, []string{"d/child"}, []string{"d"})
	identity, err := os.Lstat(filepath.Join(dir, "d"))
	if err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("descriptor rejected")
	override(t, &openatPath, func(int, string, int, uint32) (int, error) { return -1, rejected })
	if err := verifyCopyAccess(root, "d", identity); !errors.Is(err, rejected) {
		t.Fatalf("openat error=%v", err)
	}
	override(t, &openatPath, unix.Openat)
	override(t, &statSearchDir, func(*os.File) (os.FileInfo, error) { return fakeFileInfo{name: "d", mode: os.ModeDir | 0o755}, nil })
	if err := verifyCopyAccess(root, "d", identity); err == nil {
		t.Fatal("replaced search directory accepted")
	}
	override(t, &statSearchDir, func(f *os.File) (os.FileInfo, error) { return f.Stat() })
	override(t, &readSearchDir, func(*os.File, int) ([]os.DirEntry, error) { return nil, rejected })
	if err := verifyCopyAccess(root, "d", identity); !errors.Is(err, rejected) {
		t.Fatalf("search listing error=%v", err)
	}
	override(t, &readSearchDir, func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) })
	override(t, &fstatatChild, func(int, string, *unix.Stat_t, int) error { return rejected })
	if err := verifyCopyAccess(root, "d", identity); !errors.Is(err, rejected) {
		t.Fatalf("child lookup error=%v", err)
	}
}

func TestPrepareCopyAccessRestoreFailure(t *testing.T) {
	dir, root := newCopyRoot(t)
	writeTree(t, dir, []string{"node"}, nil)
	file, err := os.Open(filepath.Join(dir, "node"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	rejected := errors.New("restore rejected")
	override(t, &chmodPrepareRestore, func(*os.File, os.FileMode) error { return rejected })
	err = PrepareCopyAccess(root, "node", file, 0o644)
	if !errors.Is(err, rejected) {
		t.Fatalf("restore error=%v", err)
	}
	if err == nil || err.Error() != "restore temporary mode node: restore rejected" {
		t.Fatalf("restore message=%v", err)
	}
}

func TestManifestPathsParentFirstOrdering(t *testing.T) {
	manifest := &PermissionManifest{nodes: map[string]permissionNode{
		"b": {mode: 0o644}, "a": {mode: 0o644}, "a/c": {mode: 0o644},
	}}
	paths := manifest.paths(true)
	if len(paths) != 3 || paths[0] != "a" || paths[1] != "b" || paths[2] != "a/c" {
		t.Fatalf("paths=%v", paths)
	}
}
func TestRootedCopyCheckedRegularSourceStatFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"f"}, nil)
	rejected := errors.New("stat rejected")
	override(t, &statRegularSource, func(*os.File) (os.FileInfo, error) { return nil, rejected })
	_, root := newCopyRoot(t)
	if err := CopyNodeIntoRootChecked(filepath.Join(srcDir, "f"), root, "copy", nil, nil); !errors.Is(err, rejected) {
		t.Fatalf("checked source stat error=%v", err)
	}
}

func TestRootedCopyPreparedFileAccessFailure(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"f"}, nil)
	rejected := errors.New("access rejected")
	override(t, &manifestOpen, func(*os.Root, string, int, os.FileMode) (*os.File, error) { return nil, rejected })
	_, root := newCopyRoot(t)
	if err := CopyNodeIntoRootPrepared(filepath.Join(srcDir, "f"), root, "copy", nil, &PermissionManifest{}, nil); !errors.Is(err, rejected) {
		t.Fatalf("prepared file access error=%v", err)
	}
}

func TestRootedCopyNestedDirectorySucceeds(t *testing.T) {
	srcDir := t.TempDir()
	writeTree(t, srcDir, []string{"d/sub/inner/f"}, []string{"d", "d/sub", "d/sub/inner"})
	dir, root := newCopyRoot(t)
	if err := CopyIntoRoot(filepath.Join(srcDir, "d"), root, "copy", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "copy", "sub", "inner", "f")); err != nil {
		t.Fatal(err)
	}
}

// assertCopyRootEmpty pins that a copy rejected before creating its destination
// leaves nothing behind.
func assertCopyRootEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("copy root %s not empty: %v", dir, names)
	}
}

// assertResiduePresent checks that the partially created entry is still on disk
// for the caller's journal to remove; its error need not name the path.
func assertResiduePresent(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return
		}
	}
	t.Fatalf("created entry %q missing from %s", name, dir)
}

// assertReportedResidue pins the contract for an interrupted rooted copy: the
// error names the entry that may remain, that entry is what is left in the
// destination root, and the destructive-operation journal removes it later.
func assertReportedResidue(t *testing.T, dir string, err error, name string) {
	t.Helper()
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("error %v does not name the created entry %q", err, name)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return
		}
	}
	t.Fatalf("error reports residue %q but %s does not contain it", name, dir)
}
