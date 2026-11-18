package anchor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// AddOptions configures Add.
type AddOptions struct {
	ConfigDir  string
	Source     string
	Name       string
	Force      bool
	OnConflict func(destName string) (string, error)
}

// AddResult describes a completed add.
type AddResult struct {
	Name         string
	OriginalPath string
	Type         string
}

// Add moves a file or directory into the iCloud config directory and leaves a
// symlink behind, recording the entry in the database.
func Add(db *store.DB, opts AddOptions) (AddResult, error) {
	resolved, err := absPath(opts.Source)
	if err != nil {
		return AddResult{}, err
	}
	if err := validateCoordinatorPath(resolved, true); err != nil {
		return AddResult{}, err
	}
	if err := validateCoordinatorPath(opts.ConfigDir, true); err != nil {
		return AddResult{}, err
	}
	if !fsx.Exists(resolved) {
		return AddResult{}, fmt.Errorf("Source \"%s\" does not exist.", resolved)
	}
	if fsx.IsSymlink(resolved) {
		return AddResult{}, fmt.Errorf("Source \"%s\" is already a symlink.", resolved)
	}
	physicalSource, err := evalSymlinksPath(resolved)
	if err != nil {
		return AddResult{}, err
	}
	physicalConfig, err := evalSymlinksPath(opts.ConfigDir)
	if err != nil {
		return AddResult{}, err
	}
	if overlaps(resolved, opts.ConfigDir) || overlaps(physicalSource, physicalConfig) {
		return AddResult{}, fmt.Errorf("Source \"%s\" overlaps the iCloud config directory \"%s\".", resolved, opts.ConfigDir)
	}
	unsafe, err := identityOverlap(physicalConfig, physicalSource, true)
	if err != nil {
		return AddResult{}, err
	}
	if unsafe {
		return AddResult{}, fmt.Errorf("Source \"%s\" overlaps the iCloud config directory \"%s\".", resolved, opts.ConfigDir)
	}

	destName := opts.Name
	if destName == "" {
		destName = filepath.Base(resolved)
		if strings.HasPrefix(destName, ".") {
			destName = destName[1:]
		}
	}
	if err := ValidateEntryName(destName); err != nil {
		return AddResult{}, err
	}
	destPath := filepath.Join(opts.ConfigDir, destName)
	if fsx.Lexists(destPath) && !opts.Force && opts.OnConflict != nil {
		prefix, err := opts.OnConflict(destName)
		if err != nil {
			return AddResult{}, err
		}
		if prefix != "" {
			destName = prefix + "_" + destName
			if err := ValidateEntryName(destName); err != nil {
				return AddResult{}, err
			}
			destPath = filepath.Join(opts.ConfigDir, destName)
		}
	}
	if fsx.Lexists(destPath) && !opts.Force {
		return AddResult{}, fmt.Errorf("\"%s\" already exists in iCloud directory. Use --force to overwrite.", destName)
	}

	kind := "file"
	if fsx.IsDir(resolved) {
		kind = "directory"
	}

	previous, err := db.GetEntry(destName)
	if err != nil {
		return AddResult{}, err
	}
	entries, err := dbAllEntries(db)
	if err != nil {
		return AddResult{}, err
	}
	if previous != nil && !opts.Force && !fsx.Lexists(destPath) {
		// A row without a shared entity still belongs to its original source;
		// refuse an unforced takeover before moving any files (DELTA-018).
		return AddResult{}, fmt.Errorf("\"%s\" is still tracked but its iCloud copy is missing. Use --force to take over the entry, or run \"ianchor remove %s\" first.", destName, destName)
	}
	for _, entry := range entries {
		if entry.Name == destName {
			continue
		}
		// A case-insensitive volume stores one file under several spellings,
		// so a stale row that resolves to the same path would end up pointing
		// at the copy this call creates: remove would then move the file the
		// other row still tracks (DELTA-019). The comparison uses the path the
		// name actually resolves to, so a legacy path-shaped name is checked at
		// the place it names instead of being skipped.
		otherPath := cloudPath(opts.ConfigDir, entry.Name)
		if !strings.EqualFold(otherPath, destPath) {
			continue
		}
		if fsx.Lexists(otherPath) {
			continue
		}
		return AddResult{}, fmt.Errorf("\"%s\" differs only by case from tracked entry \"%s\", which is the same file name on macOS. %s", destName, entry.Name, removalHint(entry.Name))
	}
	if opts.Force && fsx.Lexists(destPath) {
		destination, err := lstatPath(destPath)
		if err != nil {
			return AddResult{}, err
		}
		for _, entry := range entries {
			if entry.Name == destName {
				continue
			}
			// Compare where the stored name actually resolves, so one legacy
			// path-shaped row neither blocks the takeover nor slips past as an
			// alias of the destination.
			other, err := lstatPath(cloudPath(opts.ConfigDir, entry.Name))
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return AddResult{}, err
			}
			if os.SameFile(destination, other) {
				return AddResult{}, fmt.Errorf("Name %q aliases existing tracked entry %q", destName, entry.Name)
			}
		}
	}
	if opts.Force && previous != nil {
		if err := validateOriginal(opts.ConfigDir, previous.OriginalPath); err != nil {
			return AddResult{}, err
		}
	}

	j := &journal{}
	root, err := openBackupRoot(opts.ConfigDir)
	if err != nil {
		return AddResult{}, err
	}
	defer root.Close()
	bakRoot := root.display
	root.rollbackCreation(j)
	stageName, err := root.staging(destName)
	if err != nil {
		return AddResult{}, j.fail(err)
	}
	stage := root.path(stageName)

	sourceIdentity, err := lstatPath(resolved)
	if err != nil {
		return AddResult{}, j.fail(err)
	}
	sameVolume := true
	var snapshotIdentity os.FileInfo
	if err := root.moveIn(resolved, stageName); err != nil {
		if !errors.Is(err, syscall.EXDEV) {
			return AddResult{}, j.fail(err)
		}
		sameVolume = false
		if err := root.copyRaw(resolved, stageName, func(identity os.FileInfo) {
			snapshotIdentity = identity
			j.add("remove incomplete staging copy "+stage, func() error {
				return root.removeOwnedCopy(stageName, identity)
			})
		}); err != nil {
			return AddResult{}, j.fail(err)
		}
	} else {
		j.add("restore source from "+stage+" to "+resolved, func() error { return root.moveOwnedOut(stageName, resolved, sourceIdentity) })
	}

	// For cross-volume directory copies, retain an exact snapshot for
	// comparison while separately reproducing Node's cpSync symlink behavior
	// in the published copy. Relative targets are resolved against the source;
	// rollback preserves the original object on its own volume below.
	installStage := stage
	installIdentity := sourceIdentity
	if !sameVolume {
		installIdentity = snapshotIdentity
	}
	if !sameVolume && kind == "directory" {
		installStage, err = stagingPath(bakRoot, destName+"-install")
		if err != nil {
			return AddResult{}, j.fail(err)
		}
		if err := root.copyNodeModes(resolved, filepath.Base(installStage), func(identity os.FileInfo) {
			installIdentity = identity
			j.add("remove incomplete install copy "+installStage, func() error {
				return root.removeOwnedCopy(filepath.Base(installStage), identity)
			})
		}); err != nil {
			return AddResult{}, j.fail(err)
		}
	}

	var displaced string
	var displacedIdentity os.FileInfo
	if fsx.Lexists(destPath) {
		// A competing add may have published after the initial conflict check.
		// Never displace that entry unless the caller explicitly chose force.
		if !opts.Force {
			return AddResult{}, j.fail(fmt.Errorf("\"%s\" already exists in iCloud directory. Use --force to overwrite.", destName))
		}
		displacedIdentity, err = lstatPath(destPath)
		if err != nil {
			return AddResult{}, j.fail(err)
		}
		displaced, err = stagingPath(bakRoot, destName+"-replaced")
		if err != nil {
			return AddResult{}, j.fail(err)
		}
		if err := root.moveIn(destPath, filepath.Base(displaced)); err != nil {
			return AddResult{}, j.fail(err)
		}
		j.add("restore replaced iCloud entry from "+displaced+" to "+destPath, func() error { return root.moveOwnedOut(filepath.Base(displaced), destPath, displacedIdentity) })
	}

	// installIdentity is always set: either the observed source identity or the
	// identity reported by the copy callback that created the install stage.
	if err := root.moveOwnedOut(filepath.Base(installStage), destPath, installIdentity); err != nil {
		return AddResult{}, j.fail(err)
	}
	j.add("undo iCloud move from "+destPath+" to "+installStage, func() error { return root.moveOwnedIn(destPath, filepath.Base(installStage), installIdentity) })

	var sourceStage string
	if !sameVolume {
		recoveryCopy := destPath
		if installStage != stage {
			recoveryCopy = stage
		}
		if err := verifySnapshot(resolved, recoveryCopy); err != nil {
			return AddResult{}, j.fail(err)
		}
		if installStage != stage {
			if err := verifyPublishedSnapshot(recoveryCopy, destPath, resolved); err != nil {
				return AddResult{}, j.fail(err)
			}
		}
		// Keep the actual source object on its own volume until commit. A
		// copied tree cannot restore ACLs, xattrs, inode links or timestamps.
		sourceStage, err = stagingPath(filepath.Dir(resolved), filepath.Base(resolved)+"-source")
		if err != nil {
			return AddResult{}, j.fail(err)
		}
		if err := rename(resolved, sourceStage); err != nil {
			return AddResult{}, j.fail(err)
		}
		j.add("restore original source from "+sourceStage+" to "+resolved, func() error {
			if fsx.Lexists(resolved) {
				return fmt.Errorf("refusing to overwrite new entry %s; original remains at %s", resolved, sourceStage)
			}
			return moveOwnedEntry(sourceStage, resolved, sourceIdentity)
		})
		stagedIdentity, err := lstatPath(sourceStage)
		if err != nil {
			return AddResult{}, j.fail(err)
		}
		if !os.SameFile(sourceIdentity, stagedIdentity) {
			return AddResult{}, j.fail(fmt.Errorf("source was replaced; staged object retained at %s", sourceStage))
		}
		if err := verifySnapshot(sourceStage, recoveryCopy); err != nil {
			return AddResult{}, j.fail(err)
		}
	}

	if err := symlink(destPath, resolved); err != nil {
		return AddResult{}, j.fail(err)
	}
	var createdLink os.FileInfo
	j.add("remove new symlink at "+resolved, func() error { return removeOwnedSymlink(resolved, createdLink) })
	createdLink, err = lstatPath(resolved)
	if err != nil || createdLink.Mode()&os.ModeSymlink == 0 {
		return AddResult{}, j.fail(fmt.Errorf("cannot verify created symlink at %s: %v", resolved, err))
	}

	var linkStage string
	var linkIdentity os.FileInfo
	var linkOnSourceVolume bool
	if opts.Force && previous != nil && previous.OriginalPath != nil {
		oldOriginal := *previous.OriginalPath
		if oldOriginal != "" && oldOriginal != resolved && fsx.IsSymlink(oldOriginal) {
			if target, err := readlinkPath(oldOriginal); err == nil && target == destPath {
				linkIdentity, err = lstatPath(oldOriginal)
				if err != nil {
					return AddResult{}, j.fail(err)
				}
				linkStage, err = stagingPath(bakRoot, destName+"-link")
				if err != nil {
					return AddResult{}, j.fail(err)
				}
				if err := root.moveIn(oldOriginal, filepath.Base(linkStage)); err != nil {
					if !errors.Is(err, syscall.EXDEV) {
						return AddResult{}, j.fail(err)
					}
					linkOnSourceVolume = true
					linkStage, err = stagingPath(filepath.Dir(oldOriginal), destName+"-link")
					if err != nil {
						return AddResult{}, j.fail(err)
					}
					if err := rename(oldOriginal, linkStage); err != nil {
						return AddResult{}, j.fail(err)
					}
					j.add("restore previous symlink from "+linkStage+" to "+oldOriginal, func() error { return moveOwnedEntry(linkStage, oldOriginal, linkIdentity) })
				} else {
					j.add("restore previous symlink from "+linkStage+" to "+oldOriginal, func() error { return root.moveOwnedOut(filepath.Base(linkStage), oldOriginal, linkIdentity) })
				}
			}
		}
	}

	if opts.Force {
		err = dbUpsert(db, destName, &resolved, kind, "linked")
	} else {
		err = dbAdd(db, destName, &resolved, kind, "linked")
	}
	if err != nil {
		return AddResult{}, j.fail(err)
	}
	// The commit follows immediately, so a database undo registered here could
	// never run.

	j.commit()
	var cleanupErrors []error
	var retained []string
	if sourceStage != "" {
		if err := removeOwnedEntry(sourceStage, sourceIdentity, true); err != nil {
			retained = append(retained, cleanupLocation(err, sourceStage))
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if installStage != stage {
		if err := root.removeOwnedCopy(filepath.Base(stage), snapshotIdentity); err != nil {
			retained = append(retained, cleanupLocation(err, stage))
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if displaced != "" {
		if err := root.removeOwned(filepath.Base(displaced), displacedIdentity); err != nil {
			retained = append(retained, cleanupLocation(err, displaced))
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if linkStage != "" {
		var err error
		if linkOnSourceVolume {
			err = removeOwnedEntry(linkStage, linkIdentity, false)
		} else {
			err = root.removeOwned(filepath.Base(linkStage), linkIdentity)
		}
		if err != nil {
			retained = append(retained, cleanupLocation(err, linkStage))
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if err := root.cleanupCreation(); err != nil {
		retained = append(retained, root.display)
		cleanupErrors = append(cleanupErrors, err)
	}
	if len(cleanupErrors) > 0 {
		return AddResult{}, &committedCleanupError{operation: "add", path: strings.Join(retained, ", "), err: errors.Join(cleanupErrors...)}
	}
	return AddResult{Name: destName, OriginalPath: resolved, Type: kind}, nil
}
