package anchor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

// backupRoot pins both the configuration directory and its backup directory.
// All operations through root remain confined even if the visible .bak path is
// replaced concurrently. The root remains open through rollback and cleanup.
type backupRoot struct {
	config             *os.Root
	root               *os.Root
	display            string
	created            bool
	previousIdentities map[string]os.FileInfo
	publishedIdentity  os.FileInfo
}

func openBackupRoot(configDir string) (*backupRoot, error) {
	config, err := openRootPath(configDir)
	if err != nil {
		return nil, err
	}
	created := false
	var identity os.FileInfo
	fail := func(cause error) (*backupRoot, error) {
		if created {
			current, statErr := rootLstat(config, bakDirName)
			switch {
			case os.IsNotExist(statErr):
			case statErr != nil || identity == nil || !os.SameFile(identity, current):
				cause = fmt.Errorf("%w; cannot safely remove possibly retained backup directory %s (identity could not be confirmed)", cause, filepath.Join(configDir, bakDirName))
			default:
				if hookErr := checkOp("remove-failed-backup-root", filepath.Join(configDir, bakDirName)); hookErr != nil {
					cause = fmt.Errorf("%w; backup cleanup: %v", cause, hookErr)
					break
				}
				if removeErr := removeFailedCreatedRoot(config, configDir, identity); removeErr != nil {
					cause = fmt.Errorf("%w; backup directory may remain at %s: %v", cause, filepath.Join(configDir, bakDirName), removeErr)
				}
			}
		}
		config.Close()
		return nil, cause
	}
	mkdirErr := rootMkdir(config, bakDirName, 0755)
	if mkdirErr != nil && !os.IsExist(mkdirErr) {
		return fail(mkdirErr)
	}
	created = mkdirErr == nil
	info, err := rootLstat(config, bakDirName)
	if err != nil {
		return fail(err)
	}
	if !info.IsDir() {
		return fail(fmt.Errorf("unsafe backup directory %q: not a real directory", filepath.Join(configDir, bakDirName)))
	}
	identity = info
	root, err := rootOpenRoot(config, bakDirName)
	if err != nil {
		return fail(err)
	}
	b := &backupRoot{config: config, root: root, display: filepath.Join(configDir, bakDirName), created: created}
	if err := b.attached(); err != nil {
		root.Close()
		return fail(err)
	}
	return b, nil
}

// removeFailedCreatedRoot quarantines a name before checking its identity, so
// a replacement is not removed following an earlier path-based check.
func removeFailedCreatedRoot(config *os.Root, configDir string, identity os.FileInfo) error {
	parent, err := rootOpen(config, ".")
	if err != nil {
		return err
	}
	defer parent.Close()
	stagePath, err := stagingPath(configDir, "backup-root-undo")
	if err != nil {
		return err
	}
	stage := filepath.Base(stagePath)
	if err := renameAt(int(parent.Fd()), bakDirName, int(parent.Fd()), stage); err != nil {
		return err
	}
	actual, statErr := rootLstat(config, stage)
	if statErr != nil || !os.SameFile(identity, actual) {
		if err := renameAt(int(parent.Fd()), stage, int(parent.Fd()), bakDirName); err != nil {
			return fmt.Errorf("replacement retained at %s: %w", stagePath, err)
		}
		return fmt.Errorf("backup directory was replaced")
	}
	if err := rootRemove(config, stage); err != nil {
		if restoreErr := renameAt(int(parent.Fd()), stage, int(parent.Fd()), bakDirName); restoreErr != nil {
			return fmt.Errorf("directory retained at %s: %v; restore: %v", stagePath, err, restoreErr)
		}
		return err
	}
	return nil
}

// rollbackCreation removes only a newly created, still-attached and empty root.
func (b *backupRoot) rollbackCreation(j *journal) {
	if !b.created {
		return
	}
	j.add("remove backup directory "+b.display, func() error {
		if err := b.attached(); err != nil {
			return err
		}
		pinned, err := rootOpen(b.root, ".")
		if err != nil {
			return err
		}
		identity, err := statFile(pinned)
		pinned.Close()
		if err != nil {
			return err
		}
		if err := checkOp("remove-backup-root", b.display); err != nil {
			return err
		}
		parent, err := rootOpen(b.config, ".")
		if err != nil {
			return err
		}
		defer parent.Close()
		stagePath, err := stagingPath(filepath.Dir(b.display), "backup-root-undo")
		if err != nil {
			return err
		}
		stage := filepath.Base(stagePath)
		if err := renameAt(int(parent.Fd()), bakDirName, int(parent.Fd()), stage); err != nil {
			return err
		}
		actual, statErr := rootLstat(b.config, stage)
		if statErr != nil || !os.SameFile(identity, actual) {
			if restoreErr := renameAt(int(parent.Fd()), stage, int(parent.Fd()), bakDirName); restoreErr != nil {
				return fmt.Errorf("backup replacement retained at %s: %w", stagePath, restoreErr)
			}
			return fmt.Errorf("backup directory was replaced: %s", b.display)
		}
		if err := rootRemove(b.config, stage); err != nil {
			if restoreErr := renameAt(int(parent.Fd()), stage, int(parent.Fd()), bakDirName); restoreErr != nil {
				return fmt.Errorf("backup directory retained at %s (remove: %v; restore: %v)", stagePath, err, restoreErr)
			}
			return err
		}
		return nil
	})
}

// cleanupCreation applies the same identity-checked empty-directory removal
// after commit. Existing backup directories and user data are retained.
func (b *backupRoot) cleanupCreation() error {
	j := &journal{}
	b.rollbackCreation(j)
	for _, step := range j.steps {
		if err := step.undo(); err != nil {
			return fmt.Errorf("backup directory cleanup failed (%s): %w", step.description, err)
		}
	}
	return nil
}

func (b *backupRoot) Close()                  { b.root.Close(); b.config.Close() }
func (b *backupRoot) path(name string) string { return filepath.Join(b.display, name) }
func (b *backupRoot) attached() error {
	configVisible, err := statPath(filepath.Dir(b.display))
	if err != nil {
		return fmt.Errorf("config directory is detached: %w", err)
	}
	configHandle, err := rootOpen(b.config, ".")
	if err != nil {
		return err
	}
	configPinned, err := statFile(configHandle)
	configHandle.Close()
	if err != nil {
		return err
	}
	if !os.SameFile(configVisible, configPinned) {
		return fmt.Errorf("config directory is detached or replaced: %s", filepath.Dir(b.display))
	}
	visible, err := rootLstat(b.config, bakDirName)
	if err != nil {
		return fmt.Errorf("backup directory is detached: %w", err)
	}
	if !visible.IsDir() {
		return fmt.Errorf("backup directory is detached or replaced: %s", b.display)
	}
	f, err := rootOpen(b.root, ".")
	if err != nil {
		return err
	}
	defer f.Close()
	pinned, err := statFile(f)
	if err != nil {
		return err
	}
	if !os.SameFile(visible, pinned) {
		return fmt.Errorf("backup directory is detached or replaced: %s", b.display)
	}
	return nil
}
func (b *backupRoot) rename(from, to string) error {
	if err := checkOp("rename", b.path(from)); err != nil {
		return err
	}
	if err := b.attached(); err != nil {
		return err
	}
	dir, err := rootOpen(b.root, ".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return renameAt(int(dir.Fd()), from, int(dir.Fd()), to)
}

// moveIn and moveOut pin both parent directories for an exclusive rename.
func (b *backupRoot) moveIn(source, name string) error {
	if err := checkOp("rename", source); err != nil {
		return err
	}
	if err := b.attached(); err != nil {
		return err
	}
	parent, err := openRootPath(filepath.Dir(source))
	if err != nil {
		return err
	}
	defer parent.Close()
	from, err := rootOpen(parent, ".")
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := rootOpen(b.root, ".")
	if err != nil {
		return err
	}
	defer to.Close()
	return renameAt(int(from.Fd()), filepath.Base(source), int(to.Fd()), name)
}
func (b *backupRoot) moveOut(name, dest string) error {
	if err := checkOp("rename", b.path(name)); err != nil {
		return err
	}
	if err := b.attached(); err != nil {
		return err
	}
	parent, err := openRootPath(filepath.Dir(dest))
	if err != nil {
		return err
	}
	defer parent.Close()
	from, err := rootOpen(b.root, ".")
	if err != nil {
		return err
	}
	defer from.Close()
	to, err := rootOpen(parent, ".")
	if err != nil {
		return err
	}
	defer to.Close()
	return renameAt(int(from.Fd()), name, int(to.Fd()), filepath.Base(dest))
}

// renameOwned verifies the staged inode before restoring it within the pinned root.
func (b *backupRoot) renameOwned(from, to string, identity os.FileInfo) error {
	if identity == nil {
		return fmt.Errorf("cannot verify staged entry %s", b.path(from))
	}
	quarantine, err := b.staging(from + "-restore")
	if err != nil {
		return err
	}
	if err := b.rename(from, quarantine); err != nil {
		return err
	}
	current, statErr := rootLstat(b.root, quarantine)
	if statErr != nil || !os.SameFile(identity, current) {
		if restoreErr := b.rename(quarantine, from); restoreErr != nil {
			return fmt.Errorf("entry replaced; foreign object retained at %s: %w", b.path(quarantine), restoreErr)
		}
		return fmt.Errorf("staged entry replaced: %s", b.path(from))
	}
	if err := b.rename(quarantine, to); err != nil {
		if restoreErr := b.rename(quarantine, from); restoreErr != nil {
			return fmt.Errorf("restore failed; entry retained at %s: %v; %w", b.path(quarantine), restoreErr, err)
		}
		return err
	}
	return nil
}

// moveOwnedIn quarantines a live entry and verifies it before staging it.
func (b *backupRoot) moveOwnedIn(source, name string, identity os.FileInfo) error {
	if identity == nil {
		return fmt.Errorf("cannot verify entry %s", source)
	}
	quarantine, err := b.staging(name + "-undo")
	if err != nil {
		return err
	}
	if err := b.moveIn(source, quarantine); err != nil {
		return err
	}
	current, statErr := rootLstat(b.root, quarantine)
	if statErr != nil || !os.SameFile(identity, current) {
		if restoreErr := b.moveOut(quarantine, source); restoreErr != nil {
			return fmt.Errorf("entry replaced; foreign object retained at %s: %w", b.path(quarantine), restoreErr)
		}
		return fmt.Errorf("entry was replaced at %s", source)
	}
	if err := b.rename(quarantine, name); err != nil {
		if restoreErr := b.moveOut(quarantine, source); restoreErr != nil {
			return fmt.Errorf("move failed; entry retained at %s: %v; %w", b.path(quarantine), restoreErr, err)
		}
		return err
	}
	return nil
}

// moveOwnedOut refuses to restore a stage replaced by another operation.
func (b *backupRoot) moveOwnedOut(name, dest string, identity os.FileInfo) error {
	if identity == nil {
		return fmt.Errorf("cannot verify staged entry %s", b.path(name))
	}
	quarantine, err := b.staging(name + "-restore")
	if err != nil {
		return err
	}
	if err := b.rename(name, quarantine); err != nil {
		return err
	}
	current, statErr := rootLstat(b.root, quarantine)
	if statErr != nil || !os.SameFile(identity, current) {
		if restoreErr := b.rename(quarantine, name); restoreErr != nil {
			return fmt.Errorf("stage replaced; foreign object retained as %s in pinned backup: %w", quarantine, restoreErr)
		}
		return fmt.Errorf("staged entry replaced: %s", b.path(name))
	}
	if err := b.moveOut(quarantine, dest); err != nil {
		if restoreErr := b.rename(quarantine, name); restoreErr != nil {
			return fmt.Errorf("restore failed; staged entry retained as %s in pinned backup: %v; %w", quarantine, restoreErr, err)
		}
		return err
	}
	return nil
}

func (b *backupRoot) removeAll(name string) error {
	if err := checkOp("removeall", b.path(name)); err != nil {
		return b.cleanupError(name, err)
	}
	if err := removeAllWithinRoot(b.root, name); err != nil {
		return b.cleanupError(name, err)
	}
	return nil
}
func (b *backupRoot) cleanupError(name string, err error) error {
	if attachedErr := b.attached(); attachedErr != nil {
		return &cleanupLocationError{err: fmt.Errorf("backup directory detached; actual location of %q is unknown: %w", name, err)}
	}
	return &cleanupLocationError{path: b.path(name), err: fmt.Errorf("backup retained at %s: %w", b.path(name), err)}
}

var copyIntoBackup = fsx.CopyIntoRoot
var copyNodeModesIntoBackup = fsx.CopyNodePreserveModesIntoRoot

func (b *backupRoot) copyRaw(src, name string, created func(os.FileInfo)) error {
	if err := checkOp("copy", src); err != nil {
		return err
	}
	if err := b.attached(); err != nil {
		return err
	}
	if err := copyIntoBackup(src, b.root, name, created); err != nil {
		return fmt.Errorf("copy to backup stage %s: %w", b.path(name), err)
	}
	return nil
}
func (b *backupRoot) copyNodeModes(src, name string, created func(os.FileInfo)) error {
	if err := checkOp("copy", src); err != nil {
		return err
	}
	if err := b.attached(); err != nil {
		return err
	}
	if err := copyNodeModesIntoBackup(src, b.root, name, created); err != nil {
		return fmt.Errorf("copy to backup stage %s: %w", b.path(name), err)
	}
	return nil
}
func (b *backupRoot) copyNode(src, name string, expected os.FileInfo, created func(os.FileInfo)) error {
	return b.copyNodePrepared(src, name, expected, nil, created)
}

func (b *backupRoot) copyNodePrepared(src, name string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
	if err := checkOp("copy", src); err != nil {
		return err
	}
	if err := b.attached(); err != nil {
		return err
	}
	var err error
	if manifest != nil {
		err = fsx.CopyNodeIntoRootPrepared(src, b.root, name, expected, manifest, created)
	} else {
		err = fsx.CopyNodeIntoRootChecked(src, b.root, name, expected, created)
	}
	if err != nil {
		return fmt.Errorf("copy to backup stage %s: %w", b.path(name), err)
	}
	return nil
}
func (b *backupRoot) staging(ref string) (string, error) {
	p, err := stagingPath(b.display, ref)
	if err != nil {
		return "", err
	}
	return filepath.Base(p), nil
}
func (b *backupRoot) removeOwned(name string, identity os.FileInfo) error {
	return b.removeOwnedStage(name, identity, false)
}

// removeOwnedCopy may make a verified disposable copy writable before removal.
// Never use this for an original or previous backup that must retain metadata.
func (b *backupRoot) removeOwnedCopy(name string, identity os.FileInfo) error {
	return b.removeOwnedStage(name, identity, true)
}

func (b *backupRoot) removeOwnedStage(name string, identity os.FileInfo, disposable bool) error {
	stage, err := b.staging(name + "-undo")
	if err != nil {
		return b.cleanupError(name, err)
	}
	if err := b.rename(name, stage); err != nil {
		return b.cleanupError(name, err)
	}
	current, statErr := rootLstat(b.root, stage)
	if statErr != nil || !os.SameFile(identity, current) {
		if restoreErr := b.rename(stage, name); restoreErr != nil {
			return b.cleanupError(stage, fmt.Errorf("backup replacement could not be restored: %w", restoreErr))
		}
		return fmt.Errorf("backup was replaced at %s; rollback stopped", b.path(name))
	}
	if disposable && current.IsDir() {
		// An empty directory created under umask 0777 cannot be opened for
		// chmod, but its parent can still unlink it without entering it.
		if err := checkOp("removeall", b.path(stage)); err != nil {
			return b.cleanupError(stage, err)
		}
		if err := rootRemove(b.root, stage); err == nil {
			return nil
		}
		if err := makeCopiedTreeWritableHook(b.root, stage, current); err != nil {
			return b.cleanupError(stage, err)
		}
	}
	if err := b.removeAll(stage); err != nil {
		return fmt.Errorf("backup cleanup failed: %w", err)
	}
	return nil
}

// Operate on opened directory handles, rejecting replacements and symlinks.
func makeCopiedTreeWritable(parent *os.Root, name string, expected os.FileInfo) error {
	// Opening the directory itself can succeed with read permission even when
	// opening "." relative to it fails for lack of search permission. Check
	// the opened inode before changing permissions; never chmod by path.
	dir, err := rootOpen(parent, name)
	if err != nil {
		return err
	}
	defer dir.Close()
	actual, err := statFile(dir)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, actual) {
		return fmt.Errorf("copied directory replaced: %s", name)
	}
	if err := chmodFile(dir, actual.Mode().Perm()|0700); err != nil {
		return err
	}
	child, err := rootOpenRoot(parent, name)
	if err != nil {
		return err
	}
	defer child.Close()
	childDir, err := rootOpen(child, ".")
	if err != nil {
		return err
	}
	childInfo, statErr := statFile(childDir)
	childDir.Close()
	if statErr != nil {
		return statErr
	}
	if !os.SameFile(actual, childInfo) {
		return fmt.Errorf("copied directory replaced: %s", name)
	}
	entries, err := readDirFile(dir, -1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := rootLstat(child, entry.Name())
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := checkOp("cleanup-chmod", filepath.Join(name, entry.Name())); err != nil {
				return err
			}
			if err := makeCopiedTreeWritable(child, entry.Name(), info); err != nil {
				return err
			}
		}
	}
	return nil
}
