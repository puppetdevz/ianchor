package fsx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CopyNodeIntoRoot copies using Node's existing symlink behavior. Destination
// operations stay rooted at the open directory even if its old path is moved.
// The caller retains and closes root. created is called once, only after this
// operation creates the destination root.
func CopyNodeIntoRoot(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
	return copyIntoRoot(src, root, dst, true, false, true, nil, created)
}

// CopyIntoRoot preserves link text and special modes for an exact snapshot.
func CopyIntoRoot(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
	return copyIntoRoot(src, root, dst, false, true, true, nil, created)
}

// CopyNodePreserveModesIntoRoot resolves links as Node does and retains special modes.
func CopyNodePreserveModesIntoRoot(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
	return copyIntoRoot(src, root, dst, true, true, true, nil, created)
}

// CopyNodeIntoRootChecked verifies the opened top-level regular source against
// an earlier observed identity before copying from the same file descriptor.
func CopyNodeIntoRootChecked(src string, root *os.Root, dst string, expected os.FileInfo, created func(os.FileInfo)) error {
	return copyIntoRoot(src, root, dst, true, false, true, expected, created)
}

// CopyNodeIntoRootPrepared keeps owner access until recorded modes are restored.
func CopyNodeIntoRootPrepared(src string, root *os.Root, dst string, expected os.FileInfo, manifest *PermissionManifest, created func(os.FileInfo)) error {
	if manifest == nil {
		return fmt.Errorf("permission manifest is required")
	}
	return copyFromRoot(nil, "", src, root, dst, true, false, true, expected, created, manifest, dst)
}

func copyIntoRoot(src string, root *os.Root, dst string, nodeSemantics, preserveSpecial, top bool, expected os.FileInfo, created func(os.FileInfo)) error {
	return copyFromRoot(nil, "", src, root, dst, nodeSemantics, preserveSpecial, top, expected, created, nil, dst)
}

func copyFromRoot(sourceParent *os.Root, sourceName, src string, root *os.Root, dst string, nodeSemantics, preserveSpecial, top bool, expected os.FileInfo, created func(os.FileInfo), manifest *PermissionManifest, manifestPath string) error {
	if TestCopyFault != nil {
		if err := TestCopyFault(src, dst); err != nil {
			return err
		}
	}
	var fi os.FileInfo
	var err error
	if sourceParent == nil {
		fi, err = lstatPath(src)
	} else {
		fi, err = sourceParent.Lstat(sourceName)
	}
	if err != nil {
		return err
	}
	mode := fi.Mode()
	if top && mode.IsDir() && expected != nil && !os.SameFile(expected, fi) {
		return fmt.Errorf("copy source directory was replaced: %s", src)
	}
	copyMode := mode.Perm()
	if preserveSpecial {
		copyMode |= mode & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	}
	if mode&os.ModeSymlink != 0 {
		if nodeSemantics && top {
			resolved, err := filepath.EvalSymlinks(src)
			if err != nil {
				return err
			}
			fi, err := statResolvedTarget(resolved)
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() {
				return fmt.Errorf("unsupported non-regular symlink target %q (%s)", src, fi.Mode())
			}
			if expected == nil {
				expected = fi
			}
			return copyFileIntoRoot(resolved, root, dst, fi.Mode().Perm(), expected, created, manifest, manifestPath)
		}
		var target string
		if sourceParent == nil {
			target, err = readlinkPath(src)
		} else {
			target, err = sourceParent.Readlink(sourceName)
		}
		if err != nil {
			return err
		}
		if nodeSemantics && !filepath.IsAbs(target) {
			target, err = resolvePath(filepath.Join(filepath.Dir(src), target))
			if err != nil {
				return err
			}
		}
		if err := root.Symlink(target, dst); err != nil {
			return err
		}
		if created != nil {
			info, err := rootLstatCreatedSymlink(root, dst)
			if err != nil {
				return fmt.Errorf("created symlink %q could not be verified; it may remain: %w", dst, err)
			}
			created(info)
		}
		return nil
	}
	if mode.IsDir() {
		flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		var fd int
		if sourceParent == nil {
			fd, err = unix.Open(src, flags, 0)
		} else {
			parent, openErr := openSourceParentDot(sourceParent, ".")
			if openErr != nil {
				return openErr
			}
			fd, err = unix.Openat(int(parent.Fd()), sourceName, flags, 0)
			parent.Close()
		}
		if err != nil {
			return err
		}
		sourceDir := os.NewFile(uintptr(fd), src)
		defer sourceDir.Close()
		sourceIdentity, err := statSourceDirIdentity(sourceDir)
		if err != nil {
			return err
		}
		if !os.SameFile(fi, sourceIdentity) {
			return fmt.Errorf("copy source directory was replaced: %s", src)
		}
		if err := root.Mkdir(dst, mode.Perm()|0700); err != nil {
			return err
		}
		info, err := rootLstatCreatedDir(root, dst)
		if err != nil {
			return fmt.Errorf("created copy directory %q could not be verified; it may remain: %w", dst, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("copy destination was replaced: %s", dst)
		}
		// Register as soon as the created directory can be identified. Opening
		// it may fail (for example under a restrictive umask), and that error
		// must not bypass the caller's rollback.
		if created != nil {
			created(info)
		}
		// Keep the terminal dot: dst is traversed as a directory component,
		// so a replacement FIFO is rejected before OpenRoot can block.
		child, err := openChildRoot(root, dst+"/.")
		if err != nil {
			return err
		}
		defer child.Close()
		pinned, err := openChildDot(child, ".")
		if err != nil {
			return err
		}
		pinnedInfo, err := statPinnedDestination(pinned)
		if err != nil {
			pinned.Close()
			return err
		}
		if !os.SameFile(info, pinnedInfo) {
			pinned.Close()
			return fmt.Errorf("copy destination was replaced: %s", dst)
		}
		defer pinned.Close()
		if manifest != nil {
			if err := PrepareCopyAccess(root, dst, pinned, copyMode); err != nil {
				return err
			}
			if err := manifest.Record(manifestPath, pinned, copyMode); err != nil {
				return err
			}
			if err := chmodPinnedTemporary(pinned, temporaryMode(pinnedInfo, copyMode)); err != nil {
				return err
			}
			if actual, err := statPinnedMode(pinned); err != nil || actual.Mode().Perm() != temporaryMode(pinnedInfo, copyMode) {
				return fmt.Errorf("temporary copy mode changed: %s", dst)
			}
		}
		entries, err := readSearchDir(sourceDir, -1)
		if err != nil {
			return err
		}
		var sourceRoot *os.Root
		if len(entries) > 0 {
			if sourceParent == nil {
				sourceRoot, err = openRootPath(src + "/.")
			} else {
				sourceRoot, err = openSourceRootNested(sourceParent, sourceName+"/.")
			}
			if err != nil {
				return err
			}
			defer sourceRoot.Close()
			pinnedSource, err := openSourceDot(sourceRoot, ".")
			if err != nil {
				return err
			}
			pinnedInfo, statErr := statPinnedSource(pinnedSource)
			pinnedSource.Close()
			if statErr != nil {
				return statErr
			}
			if !os.SameFile(sourceIdentity, pinnedInfo) {
				return fmt.Errorf("copy source directory was replaced: %s", src)
			}
		}
		for _, e := range entries {
			if err := copyFromRoot(sourceRoot, e.Name(), filepath.Join(src, e.Name()), child, e.Name(), nodeSemantics, preserveSpecial, false, nil, nil, manifest, filepath.Join(manifestPath, e.Name())); err != nil {
				return err
			}
		}
		if manifest == nil {
			if err := chmodPinnedFinal(pinned, copyMode); err != nil {
				return err
			}
		}
		return nil
	}
	if mode.IsRegular() {
		if sourceParent != nil {
			parent, err := openRegularParentDot(sourceParent, ".")
			if err != nil {
				return err
			}
			fd, err := openatPath(int(parent.Fd()), sourceName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			parent.Close()
			if err != nil {
				return err
			}
			in := os.NewFile(uintptr(fd), src)
			defer in.Close()
			actual, err := statRegularSource(in)
			if err != nil {
				return err
			}
			if !os.SameFile(fi, actual) || !actual.Mode().IsRegular() {
				return fmt.Errorf("copy source was replaced: %s", src)
			}
			return copyOpenedFileIntoRoot(in, src, root, dst, copyMode, created, manifest, manifestPath)
		}
		if expected == nil {
			expected = fi
		}
		return copyFileIntoRoot(src, root, dst, copyMode, expected, created, manifest, manifestPath)
	}
	return fmt.Errorf("unsupported special file %q (%s)", src, mode)
}

func copyFileIntoRoot(src string, root *os.Root, dst string, mode os.FileMode, expected os.FileInfo, created func(os.FileInfo), manifest *PermissionManifest, manifestPath string) error {
	fd, err := unix.Open(src, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	in := os.NewFile(uintptr(fd), src)
	defer in.Close()
	if expected != nil {
		actual, err := statRegularSource(in)
		if err != nil {
			return err
		}
		if !os.SameFile(expected, actual) {
			return fmt.Errorf("copy source was replaced: %s", src)
		}
	}
	return copyOpenedFileIntoRoot(in, src, root, dst, mode, created, manifest, manifestPath)
}

func copyOpenedFileIntoRoot(in *os.File, src string, root *os.Root, dst string, mode os.FileMode, created func(os.FileInfo), manifest *PermissionManifest, manifestPath string) error {
	out, err := root.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	info, err := statCreatedFile(out)
	if err != nil {
		out.Close()
		return fmt.Errorf("created file %q could not be verified; it may remain: %w", dst, err)
	}
	if created != nil {
		created(info)
	}
	if manifest != nil {
		if err := PrepareCopyAccess(root, dst, out, mode); err != nil {
			out.Close()
			return err
		}
		if err := manifest.Record(manifestPath, out, mode); err != nil {
			out.Close()
			return err
		}
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	finalMode := mode
	if manifest != nil {
		finalMode = temporaryMode(info, mode)
	}
	if err := chmodCreatedFileFinal(out, finalMode); err != nil {
		out.Close()
		return err
	}
	if actual, err := statFinalMode(out); err != nil || actual.Mode().Perm() != finalMode.Perm() {
		out.Close()
		return fmt.Errorf("temporary copy mode changed: %s", dst)
	}
	visible, err := root.Lstat(dst)
	if err != nil || !os.SameFile(info, visible) {
		out.Close()
		return fmt.Errorf("copy destination was detached or replaced: %s", dst)
	}
	return out.Close()
}
