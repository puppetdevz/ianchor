package anchor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bakDirName is the hidden subtree inside the iCloud config directory that
// holds backups, staging areas and displaced files.
const bakDirName = ".bak"

// ValidateEntryName rejects names that would escape the config directory or
// collide with ianchor's own files.
func ValidateEntryName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("Name \"%s\" must be a single path component inside the iCloud config directory", name)
	}
	if strings.ContainsRune(name, 0) || strings.ContainsRune(name, '/') || filepath.IsAbs(name) {
		return fmt.Errorf("Name \"%s\" must be a single path component inside the iCloud config directory", name)
	}
	lowerName := strings.ToLower(name)
	if lowerName == bakDirName || strings.HasPrefix(lowerName, "ianchor.db") {
		return fmt.Errorf("Name \"%s\" is reserved for ianchor internal use", name)
	}
	return nil
}

// validateOriginal rejects legacy paths that could mutate the shared tree.
// Resolve parent links but not the final component: a legitimate managed
// symlink at the original location intentionally points into the shared tree.
func validateOriginal(configDir string, original *string) error {
	if original == nil {
		return nil
	}
	path := *original
	if !filepath.IsAbs(path) || overlaps(filepath.Clean(path), filepath.Clean(configDir)) {
		return fmt.Errorf("Unsafe original path %q overlaps the iCloud config directory or is not absolute", path)
	}
	config, err := evalSymlinksPath(configDir)
	if err != nil {
		return err
	}
	unsafe, err := identityOverlap(config, path, false)
	if err != nil {
		return err
	}
	if unsafe {
		return fmt.Errorf("Unsafe original path %q overlaps the iCloud config directory", path)
	}
	parent := filepath.Dir(filepath.Clean(path))
	var missing []string
	for {
		resolved, err := evalSymlinksPath(parent)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			if overlaps(filepath.Join(resolved, filepath.Base(path)), config) {
				return fmt.Errorf("Unsafe original path %q overlaps the iCloud config directory", path)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		if _, statErr := lstatPath(parent); statErr == nil {
			return err // dangling parent symlink: do not follow it blindly
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		next := filepath.Dir(parent)
		if next == parent {
			return fmt.Errorf("cannot resolve existing ancestor of %q: %w", path, err)
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
}

// identityOverlap compares existing filesystem objects rather than spelling,
// so case and normalization aliases cannot cross the protected boundary.
// A final managed symlink must not be followed for legacy original paths.
func identityOverlap(config, path string, followFinal bool) (bool, error) {
	protected, err := statPath(config)
	if err != nil {
		return false, err
	}
	candidate, err := lstatPath(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil && (followFinal || candidate.Mode()&os.ModeSymlink == 0) {
		if followFinal {
			candidate, err = statPath(path)
			if err != nil {
				return false, err
			}
		}
		if os.SameFile(protected, candidate) {
			return true, nil
		}
		if candidate.IsDir() {
			for p := filepath.Dir(config); ; p = filepath.Dir(p) {
				info, err := statPath(p)
				if err != nil {
					return false, err
				}
				if os.SameFile(candidate, info) {
					return true, nil
				}
				if filepath.Dir(p) == p {
					break
				}
			}
		}
	}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		info, err := statPath(p)
		if err == nil && os.SameFile(protected, info) {
			return true, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return false, nil
}

// overlaps reports whether one of the two paths contains the other.
func overlaps(a, b string) bool {
	return within(a, b) || within(b, a)
}

func within(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// ensureDir creates dir and reports the directories it had to create so a
// rollback can remove them again.
type createdDirectory struct {
	path     string
	identity os.FileInfo
}

func ensureDir(dir string) ([]createdDirectory, error) {
	var created []createdDirectory
	missing := []string{}
	var parentIdentity os.FileInfo
	for probe := dir; ; probe = filepath.Dir(probe) {
		if info, err := statPath(probe); err == nil {
			parentIdentity = info
			break
		} else if !os.IsNotExist(err) {
			return created, err
		}
		missing = append(missing, probe)
		if parent := filepath.Dir(probe); parent == probe {
			return created, fmt.Errorf("cannot find existing ancestor for %q", dir)
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		path := missing[i]
		if err := checkOp("mkdir", path); err != nil {
			return created, err
		}
		currentParent, err := statPath(filepath.Dir(path))
		if err != nil || !os.SameFile(parentIdentity, currentParent) {
			return created, fmt.Errorf("directory ancestor was replaced: %s", filepath.Dir(path))
		}
		if err := mkdirPath(path, 0o755); err != nil {
			// Another process may have created the directory; never claim
			// ownership of it for rollback.
			if info, statErr := statPath(path); statErr == nil && info.IsDir() {
				parentIdentity = info
				continue
			}
			return created, err
		}
		info, err := lstatPath(path)
		created = append(created, createdDirectory{path: path, identity: info})
		if err != nil {
			return created, fmt.Errorf("created directory may remain at %s: %w", path, err)
		}
		parentIdentity = info
	}
	return created, nil
}

// removeCreatedDirs removes directories created by ensureDir, deepest first.
// Stop on failure so a parent is never removed while its child remains.
func removeCreatedDirs(created []createdDirectory) error {
	for i := len(created) - 1; i >= 0; i-- {
		entry := created[i]
		if entry.identity == nil {
			return fmt.Errorf("cannot verify created directory %s", entry.path)
		}
		stage, err := stagingPath(filepath.Dir(entry.path), filepath.Base(entry.path)+"-cleanup")
		if err != nil {
			return err
		}
		if err := rename(entry.path, stage); err != nil {
			return err
		}
		actual, statErr := lstatPath(stage)
		if statErr != nil || !os.SameFile(entry.identity, actual) {
			if err := rename(stage, entry.path); err != nil {
				return fmt.Errorf("replacement retained at %s: %w", stage, err)
			}
			return fmt.Errorf("created directory was replaced: %s", entry.path)
		}
		if err := remove(stage); err != nil {
			if restoreErr := rename(stage, entry.path); restoreErr != nil {
				return fmt.Errorf("directory retained at %s: %v; restore failed: %v", stage, err, restoreErr)
			}
			return fmt.Errorf("created directory cleanup %s: %w", entry.path, err)
		}
	}
	return nil
}
