package anchor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// verifySnapshot checks the exact rollback copy before deleting a cross-volume
// source. This detects observed changes, but cannot lock out another writer or
// close the race between the final check and deletion.
func verifySnapshot(source, snapshot string) error {
	changed := func() error {
		return fmt.Errorf("source changed during cross-volume add at %q; original was not removed", source)
	}
	src, err := lstatPath(source)
	if err != nil {
		return fmt.Errorf("check source %q: %w", source, err)
	}
	dst, err := lstatPath(snapshot)
	if err != nil {
		return fmt.Errorf("check rollback copy %q: %w", snapshot, err)
	}
	if src.Mode().Type() != dst.Mode().Type() || src.Mode().Perm() != dst.Mode().Perm() {
		return changed()
	}
	switch {
	case src.Mode()&os.ModeSymlink != 0:
		a, err := readlinkPath(source)
		if err != nil {
			return err
		}
		b, err := readlinkPath(snapshot)
		if err != nil {
			return err
		}
		if a != b {
			return changed()
		}
	case src.IsDir():
		a, err := readDirPath(source)
		if err != nil {
			return err
		}
		b, err := readDirPath(snapshot)
		if err != nil {
			return err
		}
		if len(a) != len(b) {
			return changed()
		}
		for i := range a {
			if a[i].Name() != b[i].Name() {
				return changed()
			}
			if err := verifySnapshot(filepath.Join(source, a[i].Name()), filepath.Join(snapshot, b[i].Name())); err != nil {
				return err
			}
		}
	case src.Mode().IsRegular():
		if src.Size() != dst.Size() {
			return changed()
		}
		a, err := fileHash(source)
		if err != nil {
			return err
		}
		b, err := fileHash(snapshot)
		if err != nil {
			return err
		}
		if a != b {
			return changed()
		}
	default:
		return fmt.Errorf("unsupported source type at %q", source)
	}
	after, err := lstatPath(source)
	if err != nil {
		return fmt.Errorf("recheck source %q: %w", source, err)
	}
	if !os.SameFile(src, after) || src.Mode() != after.Mode() || src.Size() != after.Size() || !src.ModTime().Equal(after.ModTime()) {
		return changed()
	}
	return nil
}

// verifyPublishedSnapshot validates the Node-style transformed directory
// against the single raw rollback snapshot, including resolved link targets.
func verifyPublishedSnapshot(raw, published, original string) error {
	a, err := lstatPath(raw)
	if err != nil {
		return err
	}
	b, err := lstatPath(published)
	if err != nil {
		return err
	}
	mismatch := func() error {
		return fmt.Errorf("published copy differs from rollback snapshot at %q; original was not removed", original)
	}
	if a.Mode().Type() != b.Mode().Type() || a.Mode().Perm() != b.Mode().Perm() {
		return mismatch()
	}
	switch {
	case a.Mode()&os.ModeSymlink != 0:
		target, err := readlinkPath(raw)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(original), target)
		}
		got, err := readlinkPath(published)
		if err != nil {
			return err
		}
		if target != got {
			return mismatch()
		}
	case a.IsDir():
		left, err := readDirPath(raw)
		if err != nil {
			return err
		}
		right, err := readDirPath(published)
		if err != nil {
			return err
		}
		if len(left) != len(right) {
			return mismatch()
		}
		for i := range left {
			if left[i].Name() != right[i].Name() {
				return mismatch()
			}
			if err := verifyPublishedSnapshot(filepath.Join(raw, left[i].Name()), filepath.Join(published, right[i].Name()), filepath.Join(original, left[i].Name())); err != nil {
				return err
			}
		}
	case a.Mode().IsRegular():
		if a.Size() != b.Size() {
			return mismatch()
		}
		x, err := fileHash(raw)
		if err != nil {
			return err
		}
		y, err := fileHash(published)
		if err != nil {
			return err
		}
		if x != y {
			return mismatch()
		}
	default:
		return fmt.Errorf("unsupported snapshot type at %q", raw)
	}
	return nil
}

func fileHash(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := openPath(path)
	if err != nil {
		return sum, err
	}
	h := sha256.New()
	_, readErr := copyHash(h, f)
	closeErr := closeHash(f)
	if readErr != nil {
		return sum, readErr
	}
	if closeErr != nil {
		return sum, closeErr
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
