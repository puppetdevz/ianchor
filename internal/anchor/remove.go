package anchor

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// RemoveResult describes a completed remove.
type RemoveResult struct {
	Name         string
	OriginalPath *string
}

// Remove deletes the iCloud copy, the symlink that points at it and the
// tracking entry.
func Remove(db *store.DB, configDir, name string) (RemoveResult, error) {
	entry, err := db.GetEntry(name)
	if err != nil {
		return RemoveResult{}, err
	}
	if entry == nil {
		return RemoveResult{}, errorf("Entry \"%s\" not found.", name)
	}
	if err := ValidateEntryName(entry.Name); err != nil {
		return RemoveResult{}, err
	}
	if err := validateOriginal(configDir, entry.OriginalPath); err != nil {
		return RemoveResult{}, err
	}
	icloudPath := filepath.Join(configDir, entry.Name)

	j := &journal{}
	var staged string
	var stageIdentity os.FileInfo
	var root *backupRoot
	var inBackup bool
	if fsx.Exists(icloudPath) {
		stageIdentity, err = lstatPath(icloudPath)
		if err != nil {
			return RemoveResult{}, j.fail(err)
		}
		// macOS resolves several spellings to one file. Moving the copy is
		// only safe when no other row tracks it: a case-only rename on disk is
		// just a different spelling of this entry's file, which stays
		// removable (DELTA-019).
		entries, err := dbAllEntries(db)
		if err != nil {
			return RemoveResult{}, j.fail(err)
		}
		for _, other := range entries {
			if other.Name == entry.Name {
				continue
			}
			// A legacy path-shaped name is compared where it actually
			// resolves, so it neither blocks unrelated removals nor slips past
			// when it names this file.
			otherInfo, err := lstatPath(cloudPath(configDir, other.Name))
			if err != nil {
				if !os.IsNotExist(err) {
					return RemoveResult{}, j.fail(err)
				}
				continue
			}
			if os.SameFile(stageIdentity, otherInfo) {
				return RemoveResult{}, j.fail(errorf("Refusing to remove \"%s\": its iCloud copy is also tracked as entry \"%s\". Run \"ianchor remove %s\" first.", entry.Name, other.Name, other.Name))
			}
		}
		root, err = openBackupRoot(configDir)
		if err != nil {
			return RemoveResult{}, j.fail(err)
		}
		defer root.Close()
		root.rollbackCreation(j)
		stageName, err := root.staging(entry.Name)
		if err != nil {
			return RemoveResult{}, j.fail(err)
		}
		staged = root.path(stageName)
		if err := root.moveIn(icloudPath, stageName); err != nil {
			if !errors.Is(err, syscall.EXDEV) {
				return RemoveResult{}, j.fail(err)
			}
			// A copy cannot reliably restore macOS metadata. Instead stage on
			// the source volume, preserving the original object for rollback.
			staged, err = stagingPath(filepath.Dir(icloudPath), entry.Name+"-removed")
			if err != nil {
				return RemoveResult{}, j.fail(err)
			}
			if err := rename(icloudPath, staged); err != nil {
				return RemoveResult{}, j.fail(err)
			}
			j.add("restore iCloud entry from "+staged+" to "+icloudPath, func() error { return moveOwnedEntry(staged, icloudPath, stageIdentity) })
		} else {
			inBackup = true
			j.add("restore iCloud entry from "+staged+" to "+icloudPath, func() error { return root.moveOwnedOut(stageName, icloudPath, stageIdentity) })
		}
		if err := checkOp("lstat-stage", staged); err != nil {
			return RemoveResult{}, j.fail(err)
		}
		var actual os.FileInfo
		if inBackup {
			actual, err = rootLstat(root.root, filepath.Base(staged))
		} else {
			actual, err = lstatPath(staged)
		}
		if err != nil {
			return RemoveResult{}, j.fail(err)
		}
		if !os.SameFile(stageIdentity, actual) {
			return RemoveResult{}, j.fail(errorf("staged entry replaced: %s", staged))
		}
	}

	if entry.OriginalPath != nil {
		original := *entry.OriginalPath
		if fsx.IsSymlink(original) {
			if target, err := readlinkPath(original); err == nil && target == icloudPath {
				if err := remove(original); err != nil {
					return RemoveResult{}, j.fail(err)
				}
				j.add("recreate symlink", func() error { return symlink(target, original) })
			}
		}
	}

	// The commit follows immediately, so an undo registered here could never
	// run; a row restore would only be reachable if a later step failed.
	if err := dbDelete(db, name); err != nil {
		return RemoveResult{}, j.fail(err)
	}

	j.commit()
	if staged != "" {
		var err error
		if inBackup {
			err = root.removeOwned(filepath.Base(staged), stageIdentity)
		} else {
			err = removeOwnedEntry(staged, stageIdentity, true)
		}
		if err != nil {
			return RemoveResult{}, &committedCleanupError{operation: "remove", path: cleanupLocation(err, staged), err: err}
		}
	}
	if root != nil {
		if err := root.cleanupCreation(); err != nil {
			return RemoveResult{}, &committedCleanupError{operation: "remove", path: root.display, err: err}
		}
	}
	return RemoveResult{Name: name, OriginalPath: entry.OriginalPath}, nil
}

// cloudPath resolves a stored name the way every command always has, so a
// legacy row whose name is path-shaped is compared at the location it actually
// names rather than at a path this build would reject.
func cloudPath(configDir, name string) string {
	return filepath.Clean(filepath.Join(configDir, name))
}
