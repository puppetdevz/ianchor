package anchor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

func TestVerifyPreparedSnapshotUsesFinalModes(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var a, b fsx.PermissionManifest
	defer a.Close()
	defer b.Close()
	for _, entry := range []struct {
		name string
		mode os.FileMode
		m    *fsx.PermissionManifest
	}{{"a", 0044, &a}, {"b", 0044, &b}} {
		if err := os.WriteFile(filepath.Join(dir, entry.name), []byte("same"), 0444); err != nil {
			t.Fatal(err)
		}
		f, err := root.Open(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := entry.m.Record(entry.name, f, entry.mode); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if err := verifyPreparedSnapshots(root, "a", &a, root, "b", &b); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPreparedSnapshotRejectsDifferentFinalModes(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var a, b fsx.PermissionManifest
	defer a.Close()
	defer b.Close()
	for _, entry := range []struct {
		name string
		mode os.FileMode
		m    *fsx.PermissionManifest
	}{{"a", 0044, &a}, {"b", 0444, &b}} {
		if err := os.WriteFile(filepath.Join(dir, entry.name), []byte("same"), 0444); err != nil {
			t.Fatal(err)
		}
		f, err := root.Open(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if err := entry.m.Record(entry.name, f, entry.mode); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	if err := verifyPreparedSnapshots(root, "a", &a, root, "b", &b); err == nil {
		t.Fatal("different final modes must fail even if temporary modes are identical")
	}
}

func TestVerifyRootSnapshotOwnerCannotReadCopies(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"backup", "copy"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("same"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, name), 0044); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// A bare copy without a manifest has already lost owner read access. It
	// cannot be safely widened via an unverified pathname; recover must use
	// prepared copies whose modes are restored only after comparison.
	if err := verifyRootSnapshot(root, "backup", root, "copy"); err == nil {
		t.Fatal("expected unprepared owner-unreadable copies to be rejected")
	}
}
