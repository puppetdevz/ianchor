// Package fsx holds the filesystem primitives ianchor needs: link-aware
// existence checks, recursive copies that preserve symlinks, and unique
// staging names.
package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// Exists reports whether path can be stat'ed, following symlinks. A dangling
// symlink therefore reports false, exactly like fs.existsSync in Node.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Lexists reports whether an entry exists at path without following it, which
// is what ianchor needs to notice dangling symlinks.
func Lexists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// IsDir reports whether path is a directory.
func IsDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}

// IsSymlink reports whether path itself is a symbolic link.
func IsSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// randomReader is crypto/rand.Reader in production. crypto/rand.Read never
// returns an error in this toolchain: it crashes instead. Tests replace this
// reader to exercise the staging-token failure without changing the bytes
// production reads.
var randomReader io.Reader = rand.Reader

// workingDirectory is os.Getwd. Relative symlink text is resolved the same way
// filepath.Abs does. Darwin still returns a path after the working directory is
// removed, so tests replace this to exercise that failure.
var workingDirectory = os.Getwd

func resolvePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	dir, err := workingDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, path), nil
}

// RandSuffix returns a short random token used for staging names.
func RandSuffix() (string, error) {
	buf := make([]byte, 6)
	if _, err := io.ReadFull(randomReader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// Copy duplicates src at dst, recursing into directories. Symbolic links are
// recreated verbatim rather than followed, matching fs.cpSync defaults.
func Copy(src, dst string) error { return CopyTracked(src, dst, nil) }

// CopyTracked calls created immediately after this operation atomically
// creates the destination root, including before any later copy failure.
func CopyTracked(src, dst string, created func()) error {
	return copy(src, dst, false, true, true, created)
}

// CopyNode mirrors the old Node recover path: copyFile follows a top-level
// symlink, while cpSync resolves nested relative symlink texts to absolute.
func CopyNode(src, dst string) error { return CopyNodeTracked(src, dst, nil) }
func CopyNodeTracked(src, dst string, created func()) error {
	return copy(src, dst, true, false, true, created)
}

// CopyNodePreserveModesTracked uses Node's symlink behavior but retains
// special permission bits for cross-volume migration.
func CopyNodePreserveModesTracked(src, dst string, created func()) error {
	return copy(src, dst, true, true, true, created)
}

func copy(src, dst string, nodeSemantics, preserveSpecial, top bool, created func()) error {
	fi, err := lstatPath(src)
	if err != nil {
		return err
	}
	mode := fi.Mode()
	copyMode := mode.Perm()
	if preserveSpecial {
		copyMode |= mode & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	}
	switch {
	case mode&os.ModeSymlink != 0:
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
			return copyFile(resolved, dst, fi.Mode().Perm(), created)
		}
		target, err := readlinkPath(src)
		if err != nil {
			return err
		}
		if nodeSemantics && !filepath.IsAbs(target) {
			target, err = resolvePath(filepath.Join(filepath.Dir(src), target))
			if err != nil {
				return err
			}
		}
		if err := os.Symlink(target, dst); err != nil {
			return err
		}
		if created != nil {
			created()
		}
		return nil
	case mode.IsDir():
		// Populate a newly created directory before applying its final mode;
		// a read-only source must not make its destination unwritable midway.
		if err := os.Mkdir(dst, mode.Perm()|0o700); err != nil {
			return err
		}
		if created != nil {
			created()
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copy(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()), nodeSemantics, preserveSpecial, false, nil); err != nil {
				return err
			}
		}
		return chmodCopyDir(dst, copyMode)
	case mode.IsRegular():
		return copyFile(src, dst, copyMode, created)
	default:
		return fmt.Errorf("unsupported special file %q (%s)", src, mode)
	}
}

func copyFile(src, dst string, perm os.FileMode, created func()) error {
	in, err := os.Open(src) // #nosec G304 - caller supplied path
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) // #nosec G304
	if err != nil {
		return err
	}
	if created != nil {
		created()
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := closeCreatedFile(out); err != nil {
		return err
	}
	return chmodCreatedFile(dst, perm)
}

// RemoveAll deletes path and everything below it, ignoring a missing path.
func RemoveAll(path string) error {
	err := os.RemoveAll(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Move renames src to dst, falling back to a copy plus delete when the two
// live on different volumes (EXDEV), which is what Node's add/rename does.
func Move(src, dst string) (crossed bool, err error) {
	if err := renamePath(src, dst); err == nil {
		return false, nil
	}
	if err := Copy(src, dst); err != nil {
		return true, err
	}
	if err := os.RemoveAll(src); err != nil {
		return true, err
	}
	return true, nil
}

// StagingPath builds a hidden sibling of ref that lives inside dir.
func StagingPath(dir, ref string) (string, error) {
	suffix, err := RandSuffix()
	if err != nil {
		return "", err
	}
	base := filepath.Base(ref)
	// Repeated staging must stay well below NAME_MAX even when ref is a
	// previous staging name or a 255-byte user filename. Preserve UTF-8
	// boundaries when truncating a valid filename.
	if len(base) > 80 {
		base = base[:80]
		for !utf8.ValidString(base) && len(base) > 0 {
			base = base[:len(base)-1]
		}
	}
	return filepath.Join(dir, fmt.Sprintf(".ianchor-tmp-%s-%s", suffix, base)), nil
}
