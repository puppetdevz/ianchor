package anchor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
	"golang.org/x/sys/unix"
)

// opHook lets tests inject a failure at every destructive filesystem or
// database boundary. Production builds leave it nil, so the wrappers below are
// plain pass-throughs.
var opHook func(op, path string) error
var copyNodeImpl = copyNodeWithIdentity

func copyNodeWithIdentity(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
	root, err := os.OpenRoot(filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer root.Close()
	if manifest != nil {
		return fsx.CopyNodeIntoRootPrepared(src, root, filepath.Base(dst), expected, manifest, created)
	}
	return fsx.CopyNodeIntoRootChecked(src, root, filepath.Base(dst), expected, created)
}

func checkOp(op, path string) error {
	if opHook == nil {
		return nil
	}
	return opHook(op, path)
}

func rename(oldPath, newPath string) error {
	if err := checkOp("rename", oldPath); err != nil {
		return err
	}
	return renameExclusive(oldPath, newPath)
}

func symlink(target, linkPath string) error {
	if err := checkOp("symlink", linkPath); err != nil {
		return err
	}
	return os.Symlink(target, linkPath)
}

func remove(path string) error {
	if err := checkOp("remove", path); err != nil {
		return err
	}
	return os.Remove(path)
}

func removeAll(path string) error {
	if err := checkOp("removeall", path); err != nil {
		return err
	}
	return recursiveRemoveAll(path)
}

func recursiveRemoveAll(path string) error {
	return removeAllWithinRoot(nil, path)
}

func removeAllWithinRoot(root *os.Root, path string) error {
	var info os.FileInfo
	var err error
	if root == nil {
		info, err = lstatPath(path)
	} else {
		info, err = rootLstat(root, path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return removePinned(root, path, info)
	}
	var dir *os.File
	if root == nil {
		dir, err = openPath(path)
	} else {
		dir, err = rootOpen(root, path)
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	opened, err := statFile(dir)
	if err != nil || !os.SameFile(info, opened) {
		return fmt.Errorf("cleanup directory replaced: %s", path)
	}
	entries, err := readDirFile(dir, -1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := entry.Name()
		if root == nil {
			child = filepath.Join(path, child)
		} else if path != "." {
			child = path + "/" + child
		}
		if err := removeAllWithinRoot(root, child); err != nil {
			return err
		}
	}
	if err := checkOp("removeall-dir", path); err != nil {
		return err
	}
	current, err := statFile(dir)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("cleanup directory replaced: %s", path)
	}
	return removePinned(root, path, current)
}

func removePinned(root *os.Root, path string, info os.FileInfo) error {
	parentPath := filepath.Dir(path)
	var parent *os.File
	var err error
	if root == nil {
		parent, err = openPath(parentPath)
	} else {
		parent, err = rootOpen(root, parentPath)
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	parentInfo, err := statFile(parent)
	if err != nil {
		return err
	}
	restore, err := ensureParentWritable(parent, parentInfo)
	if err != nil {
		return err
	}
	defer restore()
	current, err := statFile(parent)
	if err != nil || !os.SameFile(parentInfo, current) {
		return fmt.Errorf("cleanup parent replaced: %s", parentPath)
	}
	base := filepath.Base(path)
	var actual unix.Stat_t
	if err := fstatatPath(int(parent.Fd()), base, &actual, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	expected, ok := info.Sys().(*syscall.Stat_t)
	if !ok || actual.Ino != expected.Ino || actual.Dev != expected.Dev {
		return fmt.Errorf("cleanup target replaced: %s", path)
	}
	flags := unlinkFlags(info)
	if info.Mode()&os.ModeSymlink != 0 {
		flags = 0
	}
	return unlinkatPath(int(parent.Fd()), base, flags)
}

func ensureParentWritable(parent *os.File, info os.FileInfo) (func(), error) {
	original := info.Mode().Perm()
	if original&0200 != 0 {
		return func() {}, nil
	}
	if err := chmodFile(parent, original|0700); err != nil {
		return nil, err
	}
	return func() { _ = chmodFile(parent, original) }, nil
}

func unlinkFlags(info os.FileInfo) int {
	if info.IsDir() {
		return unix.AT_REMOVEDIR
	}
	return 0
}

func copyNodePath(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
	if err := checkOp("copy", src); err != nil {
		return err
	}
	return copyNodeImpl(src, dst, expected, manifest, created)
}

// removeOwnedSymlink moves the entry out of the live path before checking its
// identity, so a concurrently replaced regular file is never unlinked in its
// place. A conflict stops rollback and preserves the original source stage.
func removeOwnedSymlink(path string, identity os.FileInfo) error {
	if identity == nil || identity.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("cannot verify created symlink at %s", path)
	}
	return removeOwnedEntry(path, identity, false)
}

// removeOwnedEntry quarantines before identity verification. If the path now
// names another inode, restore it and stop rather than deleting another
// operation's replacement. Concurrent changes inside the same inode and
// malicious replacement of parent directories remain outside this guarantee.
func removeOwnedEntry(path string, identity os.FileInfo, recursive bool) error {
	return removeOwnedEntryInternal(path, identity, recursive, false)
}

// Only disposable copies or entries superseded after commit may have their
// directory modes changed for cleanup; never use this for rollback originals.
func removeOwnedCopy(path string, identity os.FileInfo) error {
	return removeOwnedEntryInternal(path, identity, true, true)
}

func removeOwnedEntryInternal(path string, identity os.FileInfo, recursive, disposable bool) error {
	if identity == nil {
		return fmt.Errorf("cannot verify created entry at %s", path)
	}
	stage, err := stagingPath(filepath.Dir(path), filepath.Base(path)+"-undo")
	if err != nil {
		return err
	}
	if err := rename(path, stage); err != nil {
		return err
	}
	current, statErr := lstatPath(stage)
	if statErr != nil || !os.SameFile(identity, current) {
		if restoreErr := rename(stage, path); restoreErr != nil {
			return &cleanupLocationError{path: stage, err: fmt.Errorf("entry changed; replacement retained at %s (restore failed: %v; stat: %v)", stage, restoreErr, statErr)}
		}
		return fmt.Errorf("entry at %s was replaced; rollback stopped", path)
	}
	if disposable && current.IsDir() {
		// An empty 000 directory can be unlinked via its parent without being
		// opened; otherwise fall back to making the copied tree writable.
		if err := remove(stage); err == nil {
			return nil
		} else if !errors.Is(err, syscall.ENOTEMPTY) && !errors.Is(err, syscall.EEXIST) {
			return &cleanupLocationError{path: stage, err: fmt.Errorf("created copy retained at %s: %w", stage, err)}
		}
		parent, openErr := openRootPath(filepath.Dir(stage))
		if openErr != nil {
			return &cleanupLocationError{path: stage, err: fmt.Errorf("created copy retained at %s: %w", stage, openErr)}
		}
		chmodErr := makeCopiedTreeWritable(parent, filepath.Base(stage), current)
		parent.Close()
		if chmodErr != nil {
			return &cleanupLocationError{path: stage, err: fmt.Errorf("created copy retained at %s: %w", stage, chmodErr)}
		}
	}
	if recursive {
		err = removeAll(stage)
	} else {
		err = remove(stage)
	}
	if err != nil {
		return &cleanupLocationError{path: stage, err: fmt.Errorf("created entry retained at %s: %w", stage, err)}
	}
	return nil
}

// moveOwnedEntry verifies a staged inode before restoring it to a live path.
func moveOwnedEntry(source, dest string, identity os.FileInfo) error {
	if identity == nil {
		return fmt.Errorf("cannot verify staged entry at %s", source)
	}
	stage, err := stagingPath(filepath.Dir(source), filepath.Base(source)+"-restore")
	if err != nil {
		return err
	}
	if err := rename(source, stage); err != nil {
		return err
	}
	current, statErr := lstatPath(stage)
	if statErr != nil || !os.SameFile(identity, current) {
		if restoreErr := rename(stage, source); restoreErr != nil {
			return fmt.Errorf("entry replaced; foreign object retained at %s: %w", stage, restoreErr)
		}
		return fmt.Errorf("entry was replaced at %s", source)
	}
	if err := rename(stage, dest); err != nil {
		if restoreErr := rename(stage, source); restoreErr != nil {
			return fmt.Errorf("restore failed; entry retained at %s: %v; %w", stage, restoreErr, err)
		}
		return err
	}
	return nil
}

func dbAdd(db *store.DB, name string, originalPath *string, kind, status string) error {
	if err := checkOp("db.add", name); err != nil {
		return err
	}
	return db.AddEntry(name, originalPath, kind, status)
}

func dbUpsert(db *store.DB, name string, originalPath *string, kind, status string) error {
	if err := checkOp("db.upsert", name); err != nil {
		return err
	}
	return db.UpsertEntry(name, originalPath, kind, status)
}

func dbDelete(db *store.DB, name string) error {
	if err := checkOp("db.delete", name); err != nil {
		return err
	}
	return db.DeleteEntry(name)
}

func dbSetStatus(db *store.DB, name, status string) error {
	if err := checkOp("db.status", name); err != nil {
		return err
	}
	return db.SetStatus(name, status)
}
