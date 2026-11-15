package anchor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// RecoverResult describes a completed recover.
type RecoverResult struct {
	Name         string
	OriginalPath string
}

// Recover writes a local copy back while keeping the iCloud file, the symlink
// contract of other devices and the tracking entry untouched.
func Recover(db *store.DB, configDir, name string, force bool) (RecoverResult, error) {
	entry, err := db.GetEntry(name)
	if err != nil {
		return RecoverResult{}, err
	}
	if entry == nil {
		return RecoverResult{}, errorf("Entry \"%s\" not found.", name)
	}
	if err := ValidateEntryName(entry.Name); err != nil {
		return RecoverResult{}, err
	}
	if err := validateOriginal(configDir, entry.OriginalPath); err != nil {
		return RecoverResult{}, err
	}
	icloudPath := filepath.Join(configDir, name)
	if !fsx.Exists(icloudPath) {
		return RecoverResult{}, errorf("\"%s\" not found in iCloud directory.", name)
	}
	cloudIdentity, err := lstatPath(icloudPath)
	if err != nil {
		return RecoverResult{}, err
	}
	cloudTarget, err := statPath(icloudPath)
	if err != nil {
		return RecoverResult{}, err
	}
	checkCloudIdentity := func() error {
		current, err := lstatPath(icloudPath)
		if err != nil || !os.SameFile(cloudIdentity, current) {
			return fmt.Errorf("iCloud source was replaced: %s", icloudPath)
		}
		target, err := statPath(icloudPath)
		if err != nil || !os.SameFile(cloudTarget, target) {
			return fmt.Errorf("iCloud source target was replaced: %s", icloudPath)
		}
		return nil
	}
	if entry.OriginalPath == nil {
		return RecoverResult{}, errorf("Entry \"%s\" has no original path. Cannot recover locally.", name)
	}
	original := *entry.OriginalPath
	originalParent, parentErr := statPath(filepath.Dir(original))
	if parentErr != nil && !os.IsNotExist(parentErr) {
		return RecoverResult{}, parentErr
	}
	checkOriginalParent := func() error {
		if originalParent == nil {
			return nil
		}
		current, err := statPath(filepath.Dir(original))
		if err != nil || !os.SameFile(originalParent, current) {
			return fmt.Errorf("recovery destination parent was detached: %s", filepath.Dir(original))
		}
		return nil
	}
	if info, err := lstatPath(original); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if _, err := statPath(original); err != nil {
			if os.IsNotExist(err) {
				return RecoverResult{}, errorf("Refusing to recover over dangling symlink at \"%s\".", original)
			}
			return RecoverResult{}, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return RecoverResult{}, err
	}

	root, err := openBackupRoot(configDir)
	if err != nil {
		return RecoverResult{}, err
	}
	defer root.Close()
	j := &journal{}
	root.rollbackCreation(j)
	var backupModes, localModes fsx.PermissionManifest
	defer backupModes.Close()
	defer localModes.Close()
	_, previousBackup, err := stageBackupPrepared(root, name, icloudPath, cloudTarget, j, &backupModes)
	if err != nil {
		return RecoverResult{}, j.fail(err)
	}
	if err := checkCloudIdentity(); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	var displaced string
	var displacedIdentity os.FileInfo
	if fsx.Exists(original) {
		var target string
		var originalIdentity os.FileInfo
		if fsx.IsSymlink(original) {
			originalIdentity, err = lstatPath(original)
			if err != nil {
				return RecoverResult{}, j.fail(err)
			}
			target, err = readlinkPath(original)
			if err != nil {
				return RecoverResult{}, j.fail(err)
			}
		}
		pointsToCloud := target == icloudPath
		if originalIdentity != nil && !pointsToCloud {
			linked, linkErr := statPath(original)
			cloud, cloudErr := statPath(icloudPath)
			if linkErr != nil {
				return RecoverResult{}, j.fail(linkErr)
			}
			if cloudErr != nil {
				return RecoverResult{}, j.fail(cloudErr)
			}
			pointsToCloud = os.SameFile(linked, cloud)
		}
		if pointsToCloud {
			linkStage, stageErr := stagingPath(filepath.Dir(original), filepath.Base(original)+"-recover")
			if stageErr != nil {
				return RecoverResult{}, j.fail(stageErr)
			}
			if err := renameDirect(original, linkStage); err != nil {
				return RecoverResult{}, j.fail(err)
			}
			linkRemoved := false
			j.add("restore original symlink", func() error {
				if err := checkOriginalParent(); err != nil {
					return err
				}
				if linkRemoved {
					return symlink(target, original)
				}
				return renameDirect(linkStage, original)
			})
			actual, statErr := lstatPath(linkStage)
			if statErr != nil || originalIdentity == nil || !os.SameFile(originalIdentity, actual) || actual.Mode()&os.ModeSymlink == 0 {
				return RecoverResult{}, j.fail(fmt.Errorf("original symlink was replaced: %s", original))
			}
			if err := remove(linkStage); err != nil {
				return RecoverResult{}, j.fail(err)
			}
			linkRemoved = true
		} else if !force {
			// Preserve the old Node behavior: refresh the backup before
			// reporting an unforced local-file conflict.
			if err := root.attached(); err != nil {
				return RecoverResult{}, j.fail(err)
			}
			j.add("reopen backup copy permissions", func() error { return manifestEnable(&backupModes, root.root) })
			if err := manifestRestore(&backupModes, root.root); err != nil {
				return RecoverResult{}, j.fail(err)
			}
			if err := manifestVerify(&backupModes, root.root); err != nil {
				return RecoverResult{}, j.fail(err)
			}
			j.commit()
			if err := cleanupPreviousBackup(root, previousBackup); err != nil {
				return RecoverResult{}, err
			}
			return RecoverResult{}, errorf("File already exists at original path \"%s\". Use --force to overwrite.", original)
		} else {
			displacedIdentity, err = lstatPath(original)
			if err != nil {
				return RecoverResult{}, j.fail(err)
			}
			displaced, err = stagingPath(filepath.Dir(original), original)
			if err != nil {
				return RecoverResult{}, j.fail(err)
			}
			if err := renameDirect(original, displaced); err != nil {
				return RecoverResult{}, j.fail(err)
			}
			staged, statErr := lstatPath(displaced)
			if statErr != nil || !os.SameFile(displacedIdentity, staged) {
				if restoreErr := renameDirect(displaced, original); restoreErr != nil {
					return RecoverResult{}, j.fail(fmt.Errorf("local entry replaced; replacement retained at %s: %w", displaced, restoreErr))
				}
				return RecoverResult{}, j.fail(fmt.Errorf("local entry was replaced: %s", original))
			}
			j.add("restore replaced local file from "+displaced+" to "+original, func() error { return moveOwnedEntry(displaced, original, displacedIdentity) })
		}
	}

	created, err := ensureDir(filepath.Dir(original))
	if len(created) > 0 {
		j.add("remove created directories", func() error { return removeCreatedDirs(created) })
	}
	if err != nil {
		return RecoverResult{}, j.fail(err)
	}

	if err := checkCloudIdentity(); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	if err := checkOriginalParent(); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	if err := validateOriginal(configDir, entry.OriginalPath); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	parentIdentity, err := statPath(filepath.Dir(original))
	if err != nil {
		return RecoverResult{}, j.fail(err)
	}
	var copiedIdentity os.FileInfo
	if err := copyNodePath(icloudPath, original, cloudTarget, &localModes, func(identity os.FileInfo) {
		copiedIdentity = identity
		j.add("remove incomplete recovered copy "+original, func() error { return removeOwnedCopy(original, identity) })
	}); err != nil {
		return RecoverResult{}, j.fail(err)
	}

	if err := checkCloudIdentity(); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	if err := validateOriginal(configDir, entry.OriginalPath); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	currentParent, err := statPath(filepath.Dir(original))
	if err != nil || !os.SameFile(parentIdentity, currentParent) {
		return RecoverResult{}, j.fail(fmt.Errorf("recovery destination parent was detached; copied data location may be unknown: %s", filepath.Dir(original)))
	}
	currentCopy, err := lstatPath(original)
	if err != nil || copiedIdentity == nil || !os.SameFile(copiedIdentity, currentCopy) {
		return RecoverResult{}, j.fail(fmt.Errorf("recovery destination was replaced: %s", original))
	}
	if err := root.attached(); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	if err := checkOp("compare-backup", root.path(name)); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	localRoot, err := openRootPath(filepath.Dir(original))
	if err != nil {
		return RecoverResult{}, j.fail(err)
	}
	localDir, err := rootOpen(localRoot, ".")
	if err != nil {
		localRoot.Close()
		return RecoverResult{}, j.fail(err)
	}
	localIdentity, statErr := statFile(localDir)
	localDir.Close()
	if statErr != nil || !os.SameFile(parentIdentity, localIdentity) {
		localRoot.Close()
		return RecoverResult{}, j.fail(fmt.Errorf("recovery destination parent was detached: %s", filepath.Dir(original)))
	}
	backupBefore, backupErr := rootLstat(root.root, name)
	copyBefore, copyErr := rootLstat(localRoot, filepath.Base(original))
	if backupErr != nil || copyErr != nil || root.publishedIdentity == nil || !os.SameFile(root.publishedIdentity, backupBefore) || copiedIdentity == nil || !os.SameFile(copiedIdentity, copyBefore) {
		localRoot.Close()
		return RecoverResult{}, j.fail(fmt.Errorf("backup or recovered copy was replaced before comparison"))
	}
	compareErr := verifyPreparedSnapshots(root.root, name, &backupModes, localRoot, filepath.Base(original), &localModes)
	if compareErr == nil {
		j.add("reopen backup copy permissions", func() error { return manifestEnable(&backupModes, root.root) })
		j.add("reopen local copy permissions", func() error {
			parent, err := openRootPath(filepath.Dir(original))
			if err != nil {
				return err
			}
			defer parent.Close()
			return manifestEnable(&localModes, parent)
		})
		compareErr = manifestRestore(&localModes, localRoot)
		if compareErr == nil {
			compareErr = manifestRestore(&backupModes, root.root)
		}
		if compareErr == nil {
			compareErr = manifestVerify(&localModes, localRoot)
		}
		if compareErr == nil {
			compareErr = manifestVerify(&backupModes, root.root)
		}
	}
	localRoot.Close()
	if compareErr != nil {
		return RecoverResult{}, j.fail(fmt.Errorf("recovered copy differs from backup; operation aborted: %w", compareErr))
	}
	if err := checkOp("compare-backup-finished", root.path(name)); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	if err := root.attached(); err != nil {
		return RecoverResult{}, j.fail(err)
	}
	currentParent, err = statPath(filepath.Dir(original))
	if err != nil || !os.SameFile(parentIdentity, currentParent) {
		return RecoverResult{}, j.fail(fmt.Errorf("recovery destination parent was detached"))
	}
	currentCopy, err = lstatPath(original)
	if err != nil || !os.SameFile(copiedIdentity, currentCopy) {
		return RecoverResult{}, j.fail(fmt.Errorf("recovery destination was replaced: %s", original))
	}
	backupAfter, err := rootLstat(root.root, name)
	if err != nil || !os.SameFile(root.publishedIdentity, backupAfter) {
		return RecoverResult{}, j.fail(fmt.Errorf("backup was replaced: %s", root.path(name)))
	}
	j.commit()
	var cleanupErrors []error
	var retained []string
	if err := cleanupPreviousBackup(root, previousBackup); err != nil {
		cleanupErrors = append(cleanupErrors, err)
		retained = append(retained, cleanupLocation(err, root.path(previousBackup)))
	}
	if displaced != "" {
		if err := removeOwnedCopy(displaced, displacedIdentity); err != nil {
			cleanupErrors = append(cleanupErrors, err)
			retained = append(retained, cleanupLocation(err, displaced))
		}
	}
	if len(cleanupErrors) > 0 {
		return RecoverResult{}, &committedCleanupError{operation: "recover", path: strings.Join(retained, ", "), err: errors.Join(cleanupErrors...)}
	}
	return RecoverResult{Name: name, OriginalPath: original}, nil
}
