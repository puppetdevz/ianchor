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
