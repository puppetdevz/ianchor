package anchor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// Relink recreates the symlink for an entry whose local file was replaced.
func Relink(db *store.DB, configDir, name string) error {
	entry, err := db.GetEntry(name)
	if err != nil {
		return err
	}
	if entry == nil {
		return errorf("Entry \"%s\" not found.", name)
	}
	if err := ValidateEntryName(entry.Name); err != nil {
		return err
	}
	if err := validateOriginal(configDir, entry.OriginalPath); err != nil {
		return err
	}
	if entry.OriginalPath == nil {
		return errorf("Entry \"%s\" has no original path. Use \"ianchor add\" with a source path instead.", name)
	}
	original := *entry.OriginalPath
	icloudPath := filepath.Join(configDir, name)
	if !fsx.Exists(icloudPath) {
		return errorf("\"%s\" not found in iCloud directory.", name)
	}

	j := &journal{}
	var previousBackup string
	var previousBackupIdentity os.FileInfo
	if fsx.Lexists(original) {
		if fsx.IsSymlink(original) {
			target, err := readlinkPath(original)
			if err != nil {
				return j.fail(err)
			}
			if target == icloudPath {
				return errorf("\"%s\" is already linked.", name)
			}
			if err := remove(original); err != nil {
				return j.fail(err)
			}
			j.add("recreate previous symlink", func() error { return symlink(target, original) })
		} else {
			backupName := original + ".bak"
			if err := validateOriginal(configDir, &backupName); err != nil {
				return j.fail(err)
			}
			if fsx.Lexists(backupName) {
				// Staging must not make a rename succeed when the original
				// rename would reject incompatible types or a nonempty directory.
				srcInfo, err := lstatPath(original)
				if err != nil {
					return j.fail(err)
				}
				dstInfo, err := lstatPath(backupName)
				if err != nil {
					return j.fail(err)
				}
				if srcInfo.IsDir() != dstInfo.IsDir() {
					return j.fail(fmt.Errorf("cannot replace incompatible backup %q", backupName))
				}
				if dstInfo.IsDir() {
					entries, err := readDirPath(backupName)
					if err != nil {
						return j.fail(err)
					}
					if len(entries) != 0 {
						return j.fail(fmt.Errorf("cannot replace nonempty backup directory %q", backupName))
					}
				}
				previousBackup, err = stagingPath(filepath.Dir(backupName), backupName)
				if err != nil {
					return j.fail(err)
				}
				if err := rename(backupName, previousBackup); err != nil {
					return j.fail(err)
				}
				previousBackupIdentity = dstInfo
				j.add("restore previous backup "+previousBackup, func() error { return moveOwnedEntry(previousBackup, backupName, previousBackupIdentity) })
			}
			localIdentity, err := lstatPath(original)
			if err != nil {
				return j.fail(err)
			}
			if err := rename(original, backupName); err != nil {
				return j.fail(err)
			}
			j.add("restore local file from "+backupName+" to "+original, func() error { return moveOwnedEntry(backupName, original, localIdentity) })
		}
	}

	created, err := ensureDir(filepath.Dir(original))
	if len(created) > 0 {
		j.add("remove created directories", func() error { return removeCreatedDirs(created) })
	}
	if err != nil {
		return j.fail(err)
	}

	if err := symlink(icloudPath, original); err != nil {
		return j.fail(err)
	}
	var createdLink os.FileInfo
	j.add("remove new symlink at "+original, func() error { return removeOwnedSymlink(original, createdLink) })
	createdLink, err = lstatPath(original)
	if err != nil || createdLink.Mode()&os.ModeSymlink == 0 {
		return j.fail(fmt.Errorf("cannot verify created symlink at %s: %v", original, err))
	}

	// No step between this write and the commit can fail, so a status undo
	// registered here would be unreachable.
	if err := dbSetStatus(db, name, "linked"); err != nil {
		return j.fail(err)
	}

	j.commit()
	if previousBackup != "" {
		if err := removeOwnedEntry(previousBackup, previousBackupIdentity, true); err != nil {
			return &committedCleanupError{operation: "relink", path: cleanupLocation(err, previousBackup), err: err}
		}
	}
	return nil
}
