package fsx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// PermissionManifest records created identities and final modes. Prepared copies
// stay owner-accessible until comparison. No descriptor is retained per node.
// Parent paths are not pinned here and these observations are not a snapshot.
type PermissionManifest struct{ nodes map[string]permissionNode }
type permissionNode struct {
	identity os.FileInfo
	mode     os.FileMode
}

func temporaryMode(info os.FileInfo, mode os.FileMode) os.FileMode {
	if info.IsDir() {
		return mode.Perm() | 0700
	}
	perm := mode.Perm()
	if perm&0400 == 0 {
		return perm | 0400
	}
	return perm
}

// Record observes an already verified creation handle; production copies first
// verify final access with PrepareCopyAccess while that handle is still open.
func (m *PermissionManifest) Record(path string, file *os.File, mode os.FileMode) error {
	path = filepath.Clean(path)
	if !validManifestPath(path) {
		return fmt.Errorf("invalid permission manifest path: %s", path)
	}
	if m.nodes == nil {
		m.nodes = make(map[string]permissionNode)
	}
	if _, ok := m.nodes[path]; ok {
		return fmt.Errorf("duplicate permission manifest path: %s", path)
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("unsupported permission object: %s", path)
	}
	m.nodes[path] = permissionNode{identity: info, mode: mode.Perm()}
	return nil
}
func validManifestPath(path string) bool {
	return path != "." && !filepath.IsAbs(path) && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}
func (m *PermissionManifest) Mode(path string) (os.FileMode, bool) {
	n, ok := m.nodes[filepath.Clean(path)]
	return n.mode, ok
}
func (m *PermissionManifest) Check(path string, observed os.FileInfo) (os.FileMode, error) {
	n, ok := m.nodes[filepath.Clean(path)]
	if !ok {
		return 0, fmt.Errorf("copy missing from permission manifest: %s", path)
	}
	if !os.SameFile(n.identity, observed) {
		return 0, fmt.Errorf("copy was replaced: %s", path)
	}
	if observed.Mode().Perm() != temporaryMode(n.identity, n.mode) {
		return 0, fmt.Errorf("temporary copy mode changed: %s", path)
	}
	return n.mode, nil
}
func (m *PermissionManifest) Rebase(old, new string) error {
	old, new = filepath.Clean(old), filepath.Clean(new)
	if !validManifestPath(old) || !validManifestPath(new) {
		return fmt.Errorf("invalid manifest rebase")
	}
	changed := make(map[string]permissionNode, len(m.nodes))
	matched := false
	for path, node := range m.nodes {
		updated := path
		if path == old || strings.HasPrefix(path, old+string(filepath.Separator)) {
			updated = new + strings.TrimPrefix(path, old)
			matched = true
		}
		if _, exists := changed[updated]; exists {
			return fmt.Errorf("manifest rebase collides at %s", updated)
		}
		changed[updated] = node
	}
	if !matched {
		return fmt.Errorf("manifest subtree not found: %s", old)
	}
	m.nodes = changed
	return nil
}

// openManifestNode never follows a replaced final symlink or waits on a FIFO.
// The opened inode is checked before any chmod is allowed.
func openManifestNode(root *os.Root, path string, identity os.FileInfo) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if identity.IsDir() {
		flags |= unix.O_DIRECTORY
	}
	f, err := manifestOpen(root, path, flags, 0)
	if err != nil && identity.IsDir() && errors.Is(err, syscall.EPERM) {
		// macOS rejects O_RDONLY on a search-only directory. Reopen it for
		// search only; the inode is still checked before chmod.
		f, err = manifestOpen(root, path, unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("copy inaccessible at %s: %w", path, err)
	}
	actual, err := f.Stat()
	if err != nil || !os.SameFile(identity, actual) {
		f.Close()
		return nil, fmt.Errorf("copy was replaced: %s", path)
	}
	return f, nil
}
func verifyCopyAccess(root *os.Root, path string, identity os.FileInfo) error {
	f, err := openManifestNode(root, path, identity)
	if err != nil {
		return err
	}
	defer f.Close()
	if identity.IsDir() {
		_, err = f.ReadDir(1)
		if err != nil && err != io.EOF {
			return fmt.Errorf("copy inaccessible at %s: %w", path, err)
		}
		// Search through the verified directory descriptor. Reopening its
		// mutable pathname with OpenRoot can block on a replacement FIFO.
		fd, err := openatPath(int(f.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return fmt.Errorf("copy inaccessible at %s: %w", path, err)
		}
		d := os.NewFile(uintptr(fd), path)
		defer d.Close()
		actual, err := statSearchDir(d)
		if err != nil || !os.SameFile(identity, actual) {
			return fmt.Errorf("copy was replaced: %s", path)
		}
		// Read permission alone is not directory search. Looking up a known
		// child through the pinned descriptor proves traversal. An empty
		// directory has no child to traverse; read plus the successful open
		// remains the applicable check.
		entries, err := readSearchDir(d, 1)
		if err != nil && err != io.EOF {
			return fmt.Errorf("copy inaccessible at %s: %w", path, err)
		}
		if len(entries) > 0 {
			var child unix.Stat_t
			if err := fstatatChild(int(d.Fd()), entries[0].Name(), &child, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return fmt.Errorf("copy inaccessible at %s: %w", path, err)
			}
		}
	}
	return nil
}

// PrepareCopyAccess tries final access on a newly created node while its creation
// handle can restore temporary access even when the check fails. This rejects
// lost ACL access before closing that handle and avoids retaining one per node.
func PrepareCopyAccess(root *os.Root, path string, file *os.File, mode os.FileMode) error {
	identity, err := file.Stat()
	if err != nil {
		return err
	}
	if err := file.Chmod(mode.Perm()); err != nil {
		return err
	}
	accessErr := verifyCopyAccess(root, path, identity)
	restoreErr := chmodPrepareRestore(file, temporaryMode(identity, mode))
	if restoreErr != nil {
		restoreErr = fmt.Errorf("restore temporary mode %s: %w", path, restoreErr)
	}
	return errors.Join(accessErr, restoreErr)
}
func (m *PermissionManifest) VerifyAccessible(root *os.Root) error {
	for path, n := range m.nodes {
		if err := verifyCopyAccess(root, path, n.identity); err != nil {
			return err
		}
	}
	return nil
}
func (m *PermissionManifest) paths(parentsFirst bool) []string {
	paths := make([]string, 0, len(m.nodes))
	for p := range m.nodes {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		a, b := strings.Count(paths[i], string(filepath.Separator)), strings.Count(paths[j], string(filepath.Separator))
		if a == b {
			return paths[i] < paths[j]
		}
		if parentsFirst {
			return a < b
		}
		return a > b
	})
	return paths
}

// EnableOwnerAccess applies only to disposable copies, in parent-first order.
func (m *PermissionManifest) EnableOwnerAccess(root *os.Root) error {
	for _, path := range m.paths(true) {
		n := m.nodes[path]
		f, err := openManifestNode(root, path, n.identity)
		if err != nil {
			return err
		}
		err = f.Chmod(temporaryMode(n.identity, n.mode))
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Restore reopens and verifies each created inode before chmod, child-first.
// Access to every final node was checked before its creation handle closed.
func (m *PermissionManifest) Restore(root *os.Root) error {
	for _, path := range m.paths(false) {
		n := m.nodes[path]
		f, err := openManifestNode(root, path, n.identity)
		if err != nil {
			return err
		}
		if TestPermissionFault != nil {
			err = TestPermissionFault(path)
		}
		if err == nil {
			err = f.Chmod(n.mode)
		}
		f.Close()
		if err != nil {
			return fmt.Errorf("restore mode %s: %w", path, err)
		}
	}
	return nil
}
func (m *PermissionManifest) Close() error { m.nodes = nil; return nil }
