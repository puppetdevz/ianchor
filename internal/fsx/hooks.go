package fsx

import (
	"os"

	"golang.org/x/sys/unix"
)

// Production assigns the real call at every seam below. Tests replace exactly
// one seam to exercise a defensive branch that would otherwise need a replaced
// inode, an unsupported filesystem or an unreachable syscall error. Shipping
// behavior is unchanged because each seam holds the same call it replaces.
var (
	// Source observations.
	lstatPath             = os.Lstat
	statResolvedTarget    = os.Stat
	readlinkPath          = os.Readlink
	renamePath            = os.Rename
	statSourceDirIdentity = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statPinnedDestination = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statPinnedMode        = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statPinnedSource      = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statRegularSource     = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statCreatedFile       = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statFinalMode         = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	statSearchDir         = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	readSearchDir         = func(file *os.File, n int) ([]os.DirEntry, error) { return file.ReadDir(n) }
	fstatatChild          = unix.Fstatat

	// Destination observations and creation inside an opened root.
	rootLstatCreatedSymlink = func(root *os.Root, name string) (os.FileInfo, error) { return root.Lstat(name) }
	rootLstatCreatedDir     = func(root *os.Root, name string) (os.FileInfo, error) { return root.Lstat(name) }
	rootLstatVisibleCopy    = func(root *os.Root, name string) (os.FileInfo, error) { return root.Lstat(name) }
	openChildDot            = func(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
	openSourceDot           = func(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
	openSourceParentDot     = func(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
	openRegularParentDot    = func(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
	openChildRoot           = func(root *os.Root, name string) (*os.Root, error) { return root.OpenRoot(name) }
	openSourceRootNested    = func(root *os.Root, name string) (*os.Root, error) { return root.OpenRoot(name) }
	openRootPath            = os.OpenRoot
	openatPath              = unix.Openat
	manifestOpen            = func(root *os.Root, path string, flags int, perm os.FileMode) (*os.File, error) {
		return root.OpenFile(path, flags, perm)
	}

	// Modes on handles this operation created.
	chmodCopyDir          = os.Chmod
	chmodCreatedFile      = os.Chmod
	chmodPinnedTemporary  = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	chmodPinnedFinal      = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	chmodCreatedFileFinal = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	chmodPrepareFinal     = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	chmodPrepareRestore   = func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) }
	closeCreatedFile      = func(file *os.File) error { return file.Close() }
)

// Fault injection seams are unit-test only. Production never sets them, and
// while they stay nil the call sites cost one nil check and nothing else.
// They are exported only because the destructive-operation tests live in
// internal/anchor and cannot reach unexported variables of this package.
var (
	TestCopyFault       func(src, dst string) error
	TestPermissionFault func(path string) error
)
