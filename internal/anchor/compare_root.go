package anchor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"golang.org/x/sys/unix"
)

// verifyRootSnapshot compares through pinned roots instead of reopening their
// visible paths. It detects observed changes but does not provide a snapshot.
func verifyRootSnapshot(aRoot *os.Root, aName string, bRoot *os.Root, bName string) error {
	return verifySnapshots(aRoot, aName, nil, bRoot, bName, nil)
}

func verifyPreparedSnapshots(aRoot *os.Root, aName string, am *fsx.PermissionManifest, bRoot *os.Root, bName string, bm *fsx.PermissionManifest) error {
	return verifySnapshots(aRoot, aName, am, bRoot, bName, bm)
}

func verifySnapshots(aRoot *os.Root, aName string, am *fsx.PermissionManifest, bRoot *os.Root, bName string, bm *fsx.PermissionManifest) error {
	a, err := rootLstat(aRoot, aName)
	if err != nil {
		return err
	}
	b, err := rootLstat(bRoot, bName)
	if err != nil {
		return err
	}
	changed := func() error { return fmt.Errorf("snapshot mismatch at %s", aName) }
	if a.Mode().Type() != b.Mode().Type() {
		return changed()
	}
	if am != nil && bm != nil && a.Mode()&os.ModeSymlink == 0 {
		ap, err := am.Check(aName, a)
		if err != nil {
			return err
		}
		bp, err := bm.Check(bName, b)
		if err != nil {
			return err
		}
		if ap != bp {
			return changed()
		}
	} else if a.Mode().Perm() != b.Mode().Perm() {
		return changed()
	}
	if err := checkOp("snapshot-open", aName); err != nil {
		return err
	}
	switch {
	case a.Mode()&os.ModeSymlink != 0:
		x, err := rootReadlink(aRoot, aName)
		if err != nil {
			return err
		}
		y, err := rootReadlink(bRoot, bName)
		if err != nil {
			return err
		}
		if x != y {
			return changed()
		}
	case a.IsDir():
		x, err := rootOpenFile(aRoot, aName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer x.Close()
		y, err := rootOpenFile(bRoot, bName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		defer y.Close()
		xInfo, err := statFile(x)
		if err != nil {
			return err
		}
		yInfo, err := statFile(y)
		if err != nil {
			return err
		}
		if !os.SameFile(a, xInfo) || !os.SameFile(b, yInfo) {
			return changed()
		}
		xEntries, err := readDirFile(x, -1)
		if err != nil {
			return err
		}
		yEntries, err := readDirFile(y, -1)
		if err != nil {
			return err
		}
		if len(xEntries) != len(yEntries) {
			return changed()
		}
		// ReadDir order is filesystem-dependent; compare by name.
		yNames := make(map[string]bool, len(yEntries))
		for _, e := range yEntries {
			yNames[e.Name()] = true
		}
		for _, e := range xEntries {
			if !yNames[e.Name()] {
				return changed()
			}
			if err := verifySnapshots(aRoot, filepath.Join(aName, e.Name()), am, bRoot, filepath.Join(bName, e.Name()), bm); err != nil {
				return err
			}
		}
	case a.Mode().IsRegular():
		if a.Size() != b.Size() {
			return changed()
		}
		x, err := rootOpenFile(aRoot, aName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer x.Close()
		y, err := rootOpenFile(bRoot, bName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer y.Close()
		xInfo, err := statFile(x)
		if err != nil {
			return err
		}
		yInfo, err := statFile(y)
		if err != nil {
			return err
		}
		if !os.SameFile(a, xInfo) || !os.SameFile(b, yInfo) {
			return changed()
		}
		xHash := sha256.New()
		if _, err := copyHash(xHash, x); err != nil {
			return err
		}
		yHash := sha256.New()
		if _, err := copyHash(yHash, y); err != nil {
			return err
		}
		if string(xHash.Sum(nil)) != string(yHash.Sum(nil)) {
			return changed()
		}
	default:
		return fmt.Errorf("unsupported snapshot object %s", aName)
	}
	last, err := rootLstat(aRoot, aName)
	if err != nil || !os.SameFile(a, last) || a.Mode() != last.Mode() || a.Size() != last.Size() || !a.ModTime().Equal(last.ModTime()) {
		return changed()
	}
	lastB, err := rootLstat(bRoot, bName)
	if err != nil || !os.SameFile(b, lastB) || b.Mode() != lastB.Mode() || b.Size() != lastB.Size() || !b.ModTime().Equal(lastB.ModTime()) {
		return changed()
	}
	return nil
}
