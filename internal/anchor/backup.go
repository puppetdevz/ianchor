package anchor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

func backupPath(configDir, name string) string { return filepath.Join(configDir, bakDirName, name) }

func backupToBak(configDir, name, icloudPath string) (string, error) {
	root, err := openBackupRoot(configDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	j := &journal{}
	root.rollbackCreation(j)
	path, previous, err := stageBackup(root, name, icloudPath, nil, j)
	if err != nil {
		return "", j.fail(err)
	}
	if err := root.attached(); err != nil {
		return "", j.fail(err)
	}
	j.commit()
	if err := cleanupPreviousBackup(root, previous); err != nil {
		return "", err
	}
	return path, nil
}

// stageBackup uses a pinned directory for all backup writes, compensation and
// cleanup. previous is a relative single-component name; root stays open until
// the surrounding transaction finishes.
func stageBackup(root *backupRoot, name, icloudPath string, expected os.FileInfo, j *journal) (string, string, error) {
	return stageBackupPrepared(root, name, icloudPath, expected, j, nil)
}

func stageBackupPrepared(root *backupRoot, name, icloudPath string, expected os.FileInfo, j *journal, manifest *fsx.PermissionManifest) (string, string, error) {
	path := root.path(name)
	stage, err := root.staging(name)
	if err != nil {
		return "", "", err
	}
	stageMoved := false
	var stageIdentity os.FileInfo
	if err := root.copyNodePrepared(icloudPath, stage, expected, manifest, func(identity os.FileInfo) {
		stageIdentity = identity
		j.add("remove incomplete backup "+root.path(stage), func() error {
			if stageMoved {
				return nil
			}
			return root.removeOwnedCopy(stage, identity)
		})
	}); err != nil {
		return "", "", err
	}
	var previous string
	if identity, err := rootLstat(root.root, name); err == nil {
		previous, err = root.staging(name)
		if err != nil {
			return "", "", err
		}
		if err := root.rename(name, previous); err != nil {
			return "", "", err
		}
		if root.previousIdentities == nil {
			root.previousIdentities = make(map[string]os.FileInfo)
		}
		root.previousIdentities[previous] = identity
		j.add("restore previous backup "+root.path(previous), func() error { return root.renameOwned(previous, name, identity) })
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	// renameOwned rejects a missing identity, so the callback invariant is
	// enforced there instead of duplicating an unreachable check here.
	identity := stageIdentity
	if err := root.renameOwned(stage, name, identity); err != nil {
		return "", "", err
	}
	stageMoved = true
	root.publishedIdentity = identity
	j.add("remove refreshed backup "+path, func() error { return root.removeOwned(name, identity) })
	if manifest != nil {
		if err := manifest.Rebase(stage, name); err != nil {
			return "", "", err
		}
	}
	return path, previous, nil
}
func cleanupPreviousBackup(root *backupRoot, previous string) error {
	if previous != "" {
		if err := root.attached(); err != nil {
			return &cleanupLocationError{err: fmt.Errorf("backup updated but previous backup location is unknown; not cleaned: %w", err)}
		}
		identity := root.previousIdentities[previous]
		if identity == nil {
			return fmt.Errorf("cannot verify previous backup identity: %s", previous)
		}
		if err := root.removeOwned(previous, identity); err != nil {
			return fmt.Errorf("backup updated but cleanup failed; previous backup named %s in pinned directory may contain only partial data (visible directory may have moved): %w", previous, err)
		}
	}
	return nil
}
