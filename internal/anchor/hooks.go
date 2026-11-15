package anchor

import (
	"io"
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// Filesystem seams. Production values are the real calls, so shipping
// behaviour is unchanged; tests replace a single entry to reach compensation
// and verification branches that need a filesystem that fails on demand.
var (
	evalSymlinksPath = filepath.EvalSymlinks
	lstatPath        = os.Lstat
	statPath         = os.Stat
	readlinkPath     = os.Readlink
	openPath         = os.Open
	readDirPath      = os.ReadDir
	mkdirPath        = os.Mkdir
	stagingPath      = fsx.StagingPath

	openRootPath = os.OpenRoot
	renameAt     = renameAtExclusive

	statFile    = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	chmodFile   = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	readDirFile = func(file *os.File, n int) ([]os.DirEntry, error) { return file.ReadDir(n) }

	makeCopiedTreeWritableHook = makeCopiedTreeWritable

	copyHash  = io.Copy
	closeHash = func(file *os.File) error { return file.Close() }
)

// Root seams keep every observation relative to a pinned directory handle.
var (
	rootLstat    = func(root *os.Root, name string) (os.FileInfo, error) { return root.Lstat(name) }
	rootOpen     = func(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
	rootOpenRoot = func(root *os.Root, name string) (*os.Root, error) { return root.OpenRoot(name) }
	rootMkdir    = func(root *os.Root, name string, perm os.FileMode) error { return root.Mkdir(name, perm) }
	rootRemove   = func(root *os.Root, name string) error { return root.Remove(name) }
	rootReadlink = func(root *os.Root, name string) (string, error) { return root.Readlink(name) }
	rootOpenFile = func(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
		return root.OpenFile(name, flag, perm)
	}
)

// Permission-manifest helpers are seams so recovery failures can be injected.
var (
	manifestRestore = func(m *fsx.PermissionManifest, root *os.Root) error { return m.Restore(root) }
	manifestVerify  = func(m *fsx.PermissionManifest, root *os.Root) error { return m.VerifyAccessible(root) }
)

// renameDirect stages entries on their own volume.
var renameDirect = func(from, to string) error { return rename(from, to) }

// absPath resolves the caller's source path so a failure can be injected.
var absPath = filepath.Abs

// dbAllEntries exposes the tracked-entry listing so a failure can be injected.
var dbAllEntries = func(db *store.DB) ([]store.Entry, error) { return db.AllEntries() }
