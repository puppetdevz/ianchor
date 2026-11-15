package anchor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

func TestRecoverUnrelatedSymlinkRequiresForce(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	other := w.write("other", "other data")
	if err := os.Symlink(other, original); err != nil {
		t.Fatal(err)
	}
	_, err := Recover(w.db, w.configDir, "item", false)
	if err == nil || !strings.Contains(err.Error(), "Use --force") {
		t.Fatalf("expected conflict: %v", err)
	}
	if target, err := os.Readlink(original); err != nil || target != other {
		t.Fatalf("unrelated symlink changed: %q %v", target, err)
	}
	if _, err := Recover(w.db, w.configDir, "item", true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, original); got != "cloud" {
		t.Fatalf("recovered %q", got)
	}
	if got := readFile(t, other); got != "other data" {
		t.Fatalf("other target modified %q", got)
	}
}

func TestRecoverForceUnrelatedSymlinkRollsBack(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	other := w.write("other", "other data")
	if err := os.Symlink(other, original); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected local copy failure")
	copies := 0
	opHook = func(op, path string) error {
		if op == "copy" && path == filepath.Join(w.configDir, "item") {
			copies++
			if copies == 2 {
				return injected
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if !errors.Is(err, injected) {
		t.Fatalf("injected error missing: %v", err)
	}
	if target, err := os.Readlink(original); err != nil || target != other {
		t.Fatalf("unrelated symlink not restored: %q %v", target, err)
	}
}

func TestRecoverRejectsMissingParentAncestorRedirect(t *testing.T) {
	w := newWorld(t)
	source := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(w.localDir, "ancestor")
	if err := os.Mkdir(ancestor, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(ancestor, "missing", "file")
	if err := w.db.UpsertEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	swapped := false
	opHook = func(op, path string) error {
		if !swapped && op == "mkdir" && path == filepath.Dir(original) {
			swapped = true
			if err := os.Rename(ancestor, ancestor+"-saved"); err != nil {
				return err
			}
			return os.Symlink(w.configDir, ancestor)
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !swapped || err == nil {
		t.Fatalf("ancestor redirect accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("wrote into cloud: %v", err)
	}
}

func TestRemoveCreatedDirsPreservesNonEmptyDirectory(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "created")
	created, err := ensureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "foreign"), "keep")
	if err := removeCreatedDirs(created); err == nil {
		t.Fatal("expected nonempty failure")
	}
	if got := readFile(t, filepath.Join(dir, "foreign")); got != "keep" {
		t.Fatalf("data moved: %q", got)
	}
}

func TestRemoveCreatedDirsPreservesReplacement(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "created")
	created, err := ensureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := removeCreatedDirs(created); err == nil {
		t.Fatal("replaced directory was accepted")
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		t.Fatalf("replacement removed: %v", err)
	}
}

func TestRecoverRejectsParentReplacementBeforeCopy(t *testing.T) {
	w := newWorld(t)
	parent := filepath.Join(w.localDir, "nested")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(parent, "source")
	writeFile(t, original, "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	swapped := false
	opHook = func(op, path string) error {
		if !swapped && op == "remove" && filepath.Dir(path) == parent && strings.Contains(filepath.Base(path), "-recover") {
			swapped = true
			if err := os.Rename(parent, parent+"-moved"); err != nil {
				return err
			}
			if err := os.Mkdir(parent, 0700); err != nil {
				return err
			}
			return os.Symlink(cloud, path)
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !swapped || err == nil {
		t.Fatalf("recover committed to replaced parent: %v", err)
	}
	if _, err := os.Lstat(original); !os.IsNotExist(err) {
		t.Fatalf("unexpected write in replacement parent: %v", err)
	}
}

func TestRecoverLargeOrdinaryTree(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "many")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 260; i++ {
		if err := os.WriteFile(filepath.Join(original, fmt.Sprintf("%03d", i)), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "many"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "many", false); err != nil {
		t.Fatalf("ordinary large tree must recover: %v", err)
	}
	if got := readFile(t, filepath.Join(original, "259")); got != "data" {
		t.Fatalf("restored content: %q", got)
	}
	if got := readFile(t, filepath.Join(w.configDir, ".bak", "many", "259")); got != "data" {
		t.Fatalf("backup content: %q", got)
	}
}

func TestRecoverRejectsReplacementAtComparison(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	writeFile(t, original, "local")
	swapped := false
	opHook = func(op, path string) error {
		if op == "compare-backup" {
			swapped = true
			info, err := os.Stat(original)
			if err != nil {
				return err
			}
			if err := os.Rename(original, original+"-swapped"); err != nil {
				return err
			}
			return os.WriteFile(original, []byte("cloud"), info.Mode().Perm())
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if !swapped || err == nil {
		t.Fatalf("replaced copy was accepted: %v", err)
	}
}

func TestRecoverComparesPinnedBackupDirectory(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud-A")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	writeFile(t, original, "local-L")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.WriteFile(src, []byte("cloud-B"), 0600); err != nil {
			return err
		}
		return copyNodeWithIdentity(src, dst, expected, manifest, created)
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	bak := filepath.Join(w.configDir, ".bak")
	swapped := false
	opHook = func(op, path string) error {
		if op == "compare-backup" {
			swapped = true
			if err := os.Rename(bak, bak+"-saved"); err != nil {
				return err
			}
			if err := os.Mkdir(bak, 0700); err != nil {
				return err
			}
			info, err := os.Stat(original)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(bak, "item"), []byte("cloud-B"), info.Mode().Perm())
		}
		if op == "compare-backup-finished" {
			if err := os.RemoveAll(bak); err != nil {
				return err
			}
			return os.Rename(bak+"-saved", bak)
		}
		return nil
	}
	defer func() {
		opHook = nil
		if swapped {
			_ = os.RemoveAll(bak)
			_ = os.Rename(bak+"-saved", bak)
		}
	}()
	_, err := Recover(w.db, w.configDir, "item", true)
	if !swapped || err == nil {
		t.Fatalf("untrusted backup accepted: %v", err)
	}
}

func TestRecoverRejectsNestedMutationBetweenCopies(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "tree")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(original, "child"), "cloud-A")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(original, "child"), "local-L")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.WriteFile(filepath.Join(src, "child"), []byte("cloud-B"), 0600); err != nil {
			return err
		}
		return copyNodeWithIdentity(src, dst, expected, manifest, created)
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if err == nil {
		t.Fatal("nested cloud mutation accepted")
	}
	if got := readFile(t, filepath.Join(original, "child")); got != "local-L" {
		t.Fatalf("original data lost: %q", got)
	}
}

func TestRecoverRejectsInPlaceCloudMutationBetweenCopies(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud-A")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	writeFile(t, original, "local-L")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.WriteFile(src, []byte("cloud-B"), 0600); err != nil {
			return err
		}
		return copyNodeWithIdentity(src, dst, expected, manifest, created)
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if err == nil {
		t.Fatal("cloud mutation accepted after backup")
	}
	if got := readFile(t, original); got != "local-L" {
		t.Fatalf("original data lost: %q", got)
	}
}

func TestRecoverRejectsTransientCloudDirectoryReplacement(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "tree")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(original, "content"), "cloud-A")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(original, "content"), "local-L")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.Rename(cloud, cloud+"-saved"); err != nil {
			return err
		}
		if err := os.Mkdir(cloud, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cloud, "content"), []byte("cloud-B"), 0600); err != nil {
			return err
		}
		copyErr := copyNodeWithIdentity(src, dst, expected, manifest, created)
		if err := os.RemoveAll(cloud); err != nil {
			return err
		}
		if err := os.Rename(cloud+"-saved", cloud); err != nil {
			return err
		}
		return copyErr
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if err == nil {
		t.Fatal("transient directory replacement accepted")
	}
	if got := readFile(t, filepath.Join(original, "content")); got != "local-L" {
		t.Fatalf("original data lost: %q", got)
	}
}

func TestRecoverRejectsTransientCloudTargetReplacement(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud-A")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	real := filepath.Join(w.configDir, "real")
	if err := os.Rename(cloud, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, cloud); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	writeFile(t, original, "local-L")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.Rename(real, real+"-saved"); err != nil {
			return err
		}
		if err := os.WriteFile(real, []byte("cloud-B"), 0600); err != nil {
			return err
		}
		copyErr := copyNodeWithIdentity(src, dst, expected, manifest, created)
		if err := os.Remove(real); err != nil {
			return err
		}
		if err := os.Rename(real+"-saved", real); err != nil {
			return err
		}
		return copyErr
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if err == nil {
		t.Fatal("transient source replacement accepted")
	}
	if got := readFile(t, original); got != "local-L" {
		t.Fatalf("original data lost: %q", got)
	}
}

func TestRecoverRejectsCloudSymlinkTargetReplacement(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "old")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	real := filepath.Join(w.configDir, "real")
	if err := os.Rename(cloud, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, cloud); err != nil {
		t.Fatal(err)
	}
	swapped := false
	opHook = func(op, path string) error {
		if !swapped && op == "rename" && path == original {
			swapped = true
			if err := os.Rename(real, real+"-saved"); err != nil {
				return err
			}
			return os.WriteFile(real, []byte("new"), 0600)
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !swapped || err == nil {
		t.Fatalf("replaced symlink target accepted: %v", err)
	}
	assertSymlink(t, original, cloud)
}

func TestRecoverRejectsCloudReplacementAfterBackup(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "old")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	swapped := false
	opHook = func(op, path string) error {
		if !swapped && op == "rename" && path == original {
			swapped = true
			if err := os.Rename(cloud, cloud+"-saved"); err != nil {
				return err
			}
			return os.WriteFile(cloud, []byte("new"), 0600)
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !swapped || err == nil {
		t.Fatalf("replaced source was accepted: %v", err)
	}
	assertSymlink(t, original, cloud)
}

func TestRecoverForceRejectsReplacedLocalBeforeCommit(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	writeFile(t, original, "local")
	swapped := false
	opHook = func(op, path string) error {
		if !swapped && op == "rename" && path == original {
			swapped = true
			if err := os.Rename(original, original+"-saved"); err != nil {
				return err
			}
			return os.WriteFile(original, []byte("foreign"), 0600)
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if !swapped || err == nil || strings.Contains(err.Error(), "completed but cleanup failed") {
		t.Fatalf("expected rejection before commit: %v", err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("foreign file not restored: %q %v", data, err)
	}
}

func TestRecoverAcceptsRelativeSymlinkToTrackedTarget(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(w.configDir, "item")
	rel, err := filepath.Rel(filepath.Dir(original), target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, original); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "item", false); err != nil {
		t.Fatalf("relative link to tracked target rejected: %v", err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "cloud" {
		t.Fatalf("recovered %q %v", data, err)
	}
}

func TestRecoverAcceptsAliasedParentOfTrackedTarget(t *testing.T) {
	w := newWorld(t)
	original := w.write("source", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(w.localDir, "cloud-alias")
	if err := os.Symlink(w.configDir, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(alias, "item"), original); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "item", false); err != nil {
		t.Fatalf("aliased link to tracked target rejected: %v", err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "cloud" {
		t.Fatalf("recovered %q %v", data, err)
	}
}

func TestRecoverReplacesReadOnlyPreviousBackup(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "tree")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filepath.Walk(w.configDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
		_ = os.Chmod(source, 0700)
	})
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "tree"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(w.configDir, "tree"), 0500); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "tree", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "tree", true); err != nil {
		t.Fatalf("second recover: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(source, "file"))
	if err != nil || string(data) != "data" {
		t.Fatalf("result %q %v", data, err)
	}
}

func TestRecoverRejectsDetachedOriginalParent(t *testing.T) {
	w := newWorld(t)
	source := w.write("source", "local")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(source)
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := copyNodeWithIdentity(src, dst, expected, manifest, created); err != nil {
			return err
		}
		if err := os.Rename(parent, parent+"-moved"); err != nil {
			return err
		}
		return os.Mkdir(parent, 0700)
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if err == nil {
		t.Fatal("recover committed after its destination parent was detached")
	}
	if data, err := os.ReadFile(filepath.Join(parent+"-moved", "source")); err != nil || string(data) != "local" {
		t.Fatalf("copied data not retained: %q %v", data, err)
	}
}

func TestRecoverDoesNotRemoveConcurrentSymlinkReplacement(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "source")
	writeFile(t, original, "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	swapped := false
	opHook = func(op, path string) error {
		if !swapped && path == original && (op == "remove" || op == "rename") {
			swapped = true
			if err := os.Rename(original, original+"-saved"); err != nil {
				return err
			}
			return os.WriteFile(original, []byte("foreign"), 0600)
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !swapped || err == nil {
		t.Fatalf("expected replacement detection: swapped=%v err=%v", swapped, err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("foreign file lost: %q %v", data, err)
	}
}

func TestRecoverCopyCallbackDoesNotClaimReplacement(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "source")
	writeFile(t, original, "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("copy failed after replacement")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		identity, err := f.Stat()
		if err != nil {
			return err
		}
		if err := os.Rename(dst, dst+"-ours"); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte("foreign"), 0600); err != nil {
			return err
		}
		created(identity)
		return injected
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !errors.Is(err, injected) {
		t.Fatalf("expected injected failure: %v", err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "foreign" {
		t.Fatalf("replacement was removed or changed: %q %v", data, err)
	}
}

func TestRecoverRollsBackEmptyInaccessibleLocalCopy(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "source")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected inaccessible copy")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.Mkdir(dst, 0700); err != nil {
			return err
		}
		identity, err := os.Lstat(dst)
		if err != nil {
			return err
		}
		created(identity)
		if err := os.Chmod(dst, 0000); err != nil {
			return err
		}
		return injected
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !errors.Is(err, injected) || strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("unexpected rollback: %v", err)
	}
	if _, err := os.Readlink(original); err != nil {
		t.Fatalf("link not restored: %v", err)
	}
}

func TestRecoverRollsBackPartialReadOnlyLocalCopy(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "source")
	if err := os.Mkdir(original, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(original, "data"), "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected partial local copy")
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.Mkdir(dst, 0700); err != nil {
			return err
		}
		identity, err := os.Lstat(dst)
		if err != nil {
			return err
		}
		created(identity)
		child := filepath.Join(dst, "readonly")
		if err := os.Mkdir(child, 0700); err != nil {
			return err
		}
		writeFile(t, filepath.Join(child, "file"), "partial")
		if err := os.Chmod(child, 0500); err != nil {
			return err
		}
		return injected
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !errors.Is(err, injected) || strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("unexpected rollback: %v", err)
	}
	if target, err := os.Readlink(original); err != nil || target != filepath.Join(w.configDir, "item") {
		t.Fatalf("link not restored: %q %v", target, err)
	}
}

func TestRemoveReportsPostCommitCleanupFailure(t *testing.T) {
	w := newWorld(t)
	cloud := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(cloud, []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	opHook = func(op, path string) error {
		if op == "removeall" && strings.Contains(filepath.Base(path), ".ianchor-tmp-") {
			return errors.New("injected cleanup failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Remove(w.db, w.configDir, "item")
	if err == nil || !strings.Contains(err.Error(), "completed") || !strings.Contains(err.Error(), ".ianchor-tmp-") {
		t.Fatalf("must report completed operation and retained path: %v", err)
	}
	var committed *committedCleanupError
	if !errors.As(err, &committed) {
		t.Fatalf("expected committed cleanup error: %v", err)
	}
	if _, statErr := os.Lstat(committed.path); statErr != nil {
		t.Fatalf("reported stale retained path %q: %v; error: %v", committed.path, statErr, err)
	}
	entry, err := w.db.GetEntry("item")
	if err != nil || entry != nil {
		t.Fatalf("database operation did not commit: %v %v", entry, err)
	}
}

func TestRemoveCleanupBeforeQuarantineReportsDetachedLocationUnknown(t *testing.T) {
	w := newWorld(t)
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(w.configDir, ".bak")
	swapped := false
	opHook = func(op, path string) error {
		if op == "db.delete" {
			swapped = true
			if err := os.Rename(bak, bak+"-detached"); err != nil {
				return err
			}
			if err := os.Mkdir(bak, 0700); err != nil {
				return err
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Remove(w.db, w.configDir, "item")
	var committed *committedCleanupError
	if !swapped || !errors.As(err, &committed) {
		t.Fatalf("expected committed cleanup failure: swapped=%v err=%v", swapped, err)
	}
	if !strings.Contains(committed.path, "location unknown") {
		t.Fatalf("reported stale location: %v", err)
	}
}

func TestRemoveCleanupFailureReportsDetachedLocationUnknown(t *testing.T) {
	w := newWorld(t)
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(w.configDir, ".bak")
	swapped := false
	opHook = func(op, path string) error {
		if op == "removeall" && strings.Contains(filepath.Base(path), "-undo") {
			swapped = true
			if err := os.Rename(bak, bak+"-detached"); err != nil {
				return err
			}
			if err := os.Mkdir(bak, 0700); err != nil {
				return err
			}
			return errors.New("injected cleanup failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Remove(w.db, w.configDir, "item")
	var committed *committedCleanupError
	if !swapped || !errors.As(err, &committed) {
		t.Fatalf("expected injected committed cleanup failure: swapped=%v err=%v", swapped, err)
	}
	if !strings.Contains(committed.path, "location unknown") || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("unknown location not reported: %v", err)
	}
	entries, err := os.ReadDir(bak + "-detached")
	if err != nil || len(entries) != 1 {
		t.Fatalf("quarantined data missing: %v %v", entries, err)
	}
}

func TestRollbackStopsBeforeDependentDestructiveSteps(t *testing.T) {
	var j journal
	ran := false
	j.add("restore displaced data", func() error { ran = true; return nil })
	j.add("move new data to safety", func() error { return errors.New("injected failure") })
	failures := j.rollback()
	if ran {
		t.Fatal("rollback continued into a potentially destructive dependent step")
	}
	if len(failures) == 0 || !strings.Contains(strings.Join(failures, " "), "move new data to safety") {
		t.Fatalf("missing failure: %v", failures)
	}
}

func TestReservedNamesIgnoreCase(t *testing.T) {
	for _, name := range []string{"IANCHOR.DB", "IAnchor.Db-WAL", ".BAK"} {
		if err := ValidateEntryName(name); err == nil {
			t.Errorf("reserved name %q accepted", name)
		}
	}
}

func TestAddRejectsSourceInsideCloudThroughParentSymlink(t *testing.T) {
	w := newWorld(t)
	cloudFile := filepath.Join(w.configDir, "old")
	if err := os.WriteFile(cloudFile, []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(w.root, "alias")
	if err := os.Symlink(w.configDir, alias); err != nil {
		t.Fatal(err)
	}
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: filepath.Join(alias, "old"), Name: "new"})
	if err == nil {
		t.Fatal("source inside cloud via symlink was accepted")
	}
	data, err := os.ReadFile(cloudFile)
	if err != nil || string(data) != "cloud" {
		t.Fatalf("cloud data changed: %q %v", data, err)
	}
}

func TestAddRejectsDanglingCloudTargetWithoutForce(t *testing.T) {
	w := newWorld(t)
	cloud := filepath.Join(w.configDir, "item")
	if err := os.Symlink("missing", cloud); err != nil {
		t.Fatal(err)
	}
	source := w.write("item", "local")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err == nil {
		t.Fatal("dangling target overwritten without force")
	}
	if target, err := os.Readlink(cloud); err != nil || target != "missing" {
		t.Fatalf("link changed: %q %v", target, err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "local" {
		t.Fatalf("source changed: %q %v", data, err)
	}
}

func TestForceAddRejectsUnsafePreviousOriginal(t *testing.T) {
	w := newWorld(t)
	dest := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(dest, []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(w.configDir, "unsafe-link")
	if err := os.Symlink(dest, old); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &old, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	source := w.write("new", "new data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item", Force: true}); err == nil {
		t.Fatal("unsafe previous original accepted")
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != "cloud" {
		t.Fatalf("cloud modified: %q %v", data, err)
	}
	if _, err := os.Lstat(old); err != nil {
		t.Fatalf("old link lost: %v", err)
	}
}

func TestBackupDirectorySymlinkIsNotFollowed(t *testing.T) {
	w := newWorld(t)
	external := filepath.Join(w.root, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "item"), []byte("external data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(w.configDir, bakDirName)); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(cloud, []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := backupToBak(w.configDir, "item", cloud); err == nil {
		t.Fatal("symlinked backup directory accepted")
	}
	data, err := os.ReadFile(filepath.Join(external, "item"))
	if err != nil || string(data) != "external data" {
		t.Fatalf("external data modified: %q %v", data, err)
	}
}

func TestRenameRefusesUnexpectedDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	dest := filepath.Join(dir, "destination")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rename(source, dest); err == nil {
		t.Fatal("unexpected destination overwritten")
	}
	if got := readFile(t, dest); got != "unrelated" {
		t.Fatalf("destination changed: %q", got)
	}
	if got := readFile(t, source); got != "source" {
		t.Fatalf("source changed: %q", got)
	}
}

func TestRemoveCrossVolumeFailureRestoresCloudData(t *testing.T) {
	w := newWorld(t)
	cloud := filepath.Join(w.configDir, "item")
	if err := os.Mkdir(cloud, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloud, "lost"), []byte("valuable"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("lost", filepath.Join(cloud, "link")); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", nil, "directory", "linked"); err != nil {
		t.Fatal(err)
	}
	failedRename := false
	opHook = func(op, path string) error {
		if op == "rename" && path == cloud && !failedRename {
			failedRename = true
			return syscall.EXDEV
		}
		if op == "rename" && path == cloud && failedRename {
			return errors.New("injected sibling staging failure")
		}
		if op == "copy" {
			t.Fatal("cross-volume remove must not copy and delete the original")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Remove(w.db, w.configDir, "item"); err == nil {
		t.Fatal("expected failure")
	}
	data, err := os.ReadFile(filepath.Join(cloud, "lost"))
	if err != nil || string(data) != "valuable" {
		t.Fatalf("cloud data lost: %q %v", data, err)
	}
}

func TestRecoverCopiesTopLevelSymlinkTargetLikeNode(t *testing.T) {
	w := newWorld(t)
	target := filepath.Join(w.root, "target")
	if err := os.WriteFile(target, []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "item")
	rel, err := filepath.Rel(w.configDir, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, cloud); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(w.localDir, "item")
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "item", false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{original, filepath.Join(w.configDir, ".bak", "item")} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			t.Fatalf("expected regular copy at %s: %v %v", path, info, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "contents" {
			t.Fatalf("copy at %s: %q %v", path, data, err)
		}
	}
}

func TestRelinkDoesNotDeleteExistingBackupDirectory(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "local")
	cloud := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(cloud, []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	backup := original + ".bak"
	if err := os.Mkdir(backup, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "valuable"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Relink(w.db, w.configDir, "item"); err == nil {
		t.Fatal("expected incompatible existing backup to reject relink")
	}
	for path, want := range map[string]string{original: "local", filepath.Join(backup, "valuable"): "keep"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", path, data, err)
		}
	}
}

func TestRelinkRejectsBackupOverlappingCloud(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "cloud")
	cloud := original + ".bak"
	if err := os.Mkdir(cloud, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloud, "item"), []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(cloud)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	if err := Relink(db, cloud, "item"); err == nil {
		t.Fatal("unsafe backup collision accepted")
	}
	for path, want := range map[string]string{original: "local", filepath.Join(cloud, "item"): "cloud"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("data changed at %s: %q %v", path, data, err)
		}
	}
}

func TestRelinkRollbackDoesNotRestoreReplacedLocalBackup(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "current")
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	opHook = func(op, path string) error {
		if op == "symlink" && path == original {
			if err := os.Rename(original+".bak", original+".saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, original+".bak", "foreign")
			return errors.New("injected")
		}
		return nil
	}
	defer func() { opHook = nil }()
	err := Relink(w.db, w.configDir, "item")
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected rollback failure: %v", err)
	}
	if got := readFile(t, original+".bak"); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, original+".saved"); got != "current" {
		t.Fatalf("saved %q", got)
	}
}

func TestRelinkCleanupDoesNotDeleteReplacedPreviousBackup(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "current")
	writeFile(t, original+".bak", "old backup")
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	var stage string
	opHook = func(op, path string) error {
		if op == "db.status" {
			matches, _ := filepath.Glob(filepath.Join(w.localDir, ".ianchor-tmp-*"))
			if len(matches) != 1 {
				t.Fatalf("stages: %v", matches)
			}
			stage = matches[0]
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	err := Relink(w.db, w.configDir, "item")
	if err == nil {
		t.Fatal("replacement must be reported")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, stage+"-saved"); got != "old backup" {
		t.Fatalf("old backup %q", got)
	}
}

func TestRelinkFailurePreservesPreviousBackup(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "current")
	if err := os.WriteFile(original+".bak", []byte("old backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.configDir, "item"), []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	opHook = func(op, path string) error {
		if op == "symlink" && path == original {
			return errors.New("injected symlink failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if err := Relink(w.db, w.configDir, "item"); err == nil {
		t.Fatal("expected failure")
	}
	for path, want := range map[string]string{original: "current", original + ".bak": "old backup"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("%s: got %q, %v", path, data, err)
		}
	}
}

func TestCrossVolumeRollbackPreservesStickyMode(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(source, "config"), "data")
	if err := os.Chmod(source, 0755|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		if op == "symlink" && path == source {
			return errors.New("injected link failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err == nil || !strings.Contains(err.Error(), "injected link failure") {
		t.Fatalf("expected injected link failure: %v", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSticky == 0 {
		t.Fatalf("sticky bit lost on rollback: %s", info.Mode())
	}
}

func TestAddRollbackDoesNotDeleteConcurrentReplacement(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	opHook = func(op, path string) error {
		if op == "db.add" {
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("concurrent"), 0600); err != nil {
				t.Fatal(err)
			}
			return errors.New("injected db failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := readFile(t, source); got != "concurrent" {
		t.Fatalf("concurrent file lost: %q (%v)", got, err)
	}
}

func TestAddRemoveBackupRootSwapDoesNotTouchOutside(t *testing.T) {
	for _, action := range []string{"add", "remove"} {
		t.Run(action, func(t *testing.T) {
			w := newWorld(t)
			source := w.write("item", "local")
			cloud := filepath.Join(w.configDir, "item")
			if action == "remove" {
				if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
					t.Fatal(err)
				}
			}
			backupRoot := filepath.Join(w.configDir, ".bak")
			if err := os.MkdirAll(backupRoot, 0700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside")
			writeFile(t, filepath.Join(outside, "sentinel"), "foreign")
			swapped := false
			opHook = func(op, path string) error {
				want := source
				if action == "remove" {
					want = cloud
				}
				if op == "rename" && path == want && !swapped {
					swapped = true
					if err := os.Rename(backupRoot, backupRoot+"-saved"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, backupRoot); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			defer func() { opHook = nil }()
			var err error
			if action == "add" {
				_, err = Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
			} else {
				_, err = Remove(w.db, w.configDir, "item")
			}
			if !swapped {
				t.Fatal("did not inject root swap")
			}
			if err == nil {
				t.Fatal("swapped root accepted")
			}
			entries, e := os.ReadDir(outside)
			if e != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
				t.Fatalf("outside changed: %v %v (%v)", entries, e, err)
			}
			if got := readFile(t, filepath.Join(outside, "sentinel")); got != "foreign" {
				t.Fatalf("outside modified: %q", got)
			}
		})
	}
}

func TestRecoverCleanupDoesNotDeleteReplacedPreviousBackup(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(w.configDir, ".bak")
	writeFile(t, filepath.Join(bak, "item"), "old")
	copies := 0
	var previous string
	opHook = func(op, path string) error {
		if op == "copy" && path == filepath.Join(w.configDir, "item") {
			copies++
			if copies == 2 {
				matches, err := filepath.Glob(filepath.Join(bak, ".ianchor-tmp-*"))
				if err != nil || len(matches) != 1 {
					t.Fatalf("stages %v %v", matches, err)
				}
				previous = matches[0]
				if err := os.Rename(previous, previous+"-saved"); err != nil {
					t.Fatal(err)
				}
				writeFile(t, previous, "foreign")
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if err == nil {
		t.Fatal("replacement not reported")
	}
	if got := readFile(t, previous); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
}

func TestAddRefusesReplacedConfigDirectory(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "source")
	cfg := w.configDir
	swapped := false
	opHook = func(op, path string) error {
		if op == "rename" && filepath.Dir(path) == filepath.Join(cfg, ".bak") && !swapped {
			swapped = true
			if err := os.Rename(cfg, cfg+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(cfg, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: cfg, Source: source, Name: "item"})
	if !swapped || err == nil {
		t.Fatalf("config replacement accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cfg, "item")); !os.IsNotExist(err) {
		t.Fatalf("wrote replacement directory: %v", err)
	}
}

func TestAddForceCleanupDoesNotDeleteReplacedStage(t *testing.T) {
	w := newWorld(t)
	first := w.write("first", "old")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	second := w.write("second", "new")
	var stage string
	opHook = func(op, path string) error {
		if op == "db.upsert" {
			matches, err := filepath.Glob(filepath.Join(w.configDir, ".bak", ".ianchor-tmp-*-item-replaced"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("stages %v %v", matches, err)
			}
			stage = matches[0]
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "item", Force: true})
	if err == nil {
		t.Fatal("replacement not reported")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
}

func TestRecoverRefusesCommitWhenBackupRootDetached(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(w.configDir, ".bak")
	writeFile(t, filepath.Join(bak, "item"), "old-backup")
	copies := 0
	opHook = func(op, path string) error {
		if op == "copy" && path == filepath.Join(w.configDir, "item") {
			copies++
			if copies == 2 {
				if err := os.Rename(bak, bak+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(bak, 0700); err != nil {
					t.Fatal(err)
				}
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if copies != 2 || err == nil {
		t.Fatalf("detachment ignored: copies=%d err=%v", copies, err)
	}
	matches, _ := filepath.Glob(filepath.Join(bak+"-saved", ".ianchor-tmp-*"))
	if len(matches) != 1 {
		t.Fatalf("previous backup not retained: %v", matches)
	}
	if got := readFile(t, matches[0]); got != "old-backup" {
		t.Fatalf("previous backup %q", got)
	}
}

func TestRecoverCleanupDoesNotDeleteReplacedLocalStage(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "local")
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	copyOrig := copyNodeImpl
	defer func() { copyNodeImpl = copyOrig }()
	var stage string
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := copyOrig(src, dst, expected, manifest, created); err != nil {
			return err
		}
		matches, _ := filepath.Glob(filepath.Join(w.localDir, ".ianchor-tmp-*"))
		if len(matches) != 1 {
			t.Fatalf("stages %v", matches)
		}
		stage = matches[0]
		if err := os.Rename(stage, stage+"-saved"); err != nil {
			t.Fatal(err)
		}
		writeFile(t, stage, "foreign")
		return nil
	}
	_, err := Recover(w.db, w.configDir, "item", true)
	if err == nil {
		t.Fatal("cleanup replacement must be reported")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, stage+"-saved"); got != "local" {
		t.Fatalf("saved %q", got)
	}
	if got := readFile(t, original); got != "cloud" {
		t.Fatalf("recovered %q", got)
	}
}

func TestRecoverRollbackDoesNotDeleteReplacedCopy(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	copyOrig := copyNodeImpl
	defer func() { copyNodeImpl = copyOrig }()
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := copyOrig(src, dst, expected, manifest, created); err != nil {
			return err
		}
		if err := os.Rename(dst, dst+"-saved"); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dst, "foreign")
		return errors.New("injected partial failure")
	}
	_, err := Recover(w.db, w.configDir, "item", false)
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected rollback failure: %v", err)
	}
	if got := readFile(t, original); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, original+"-saved"); got != "cloud" {
		t.Fatalf("saved %q", got)
	}
}

func TestRemoveRestoresOriginalWhenStageStatFails(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "source")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	injected := false
	opHook = func(op, path string) error {
		if op == "lstat-stage" {
			injected = true
			return errors.New("injected stage stat failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Remove(w.db, w.configDir, "item")
	if !injected || err == nil || !strings.Contains(err.Error(), "injected stage stat failure") {
		t.Fatalf("error=%v injected=%v", err, injected)
	}
	if got := readFile(t, filepath.Join(w.configDir, "item")); got != "source" {
		t.Fatalf("cloud=%q", got)
	}
	if got := readFile(t, original); got != "source" {
		t.Fatalf("original=%q", got)
	}
}

func TestAddLongNameDoesNotStrandSource(t *testing.T) {
	w := newWorld(t)
	name := strings.Repeat("a", 200)
	source := w.write(name, "contents")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: name}); err != nil {
		t.Fatalf("add long name: %v", err)
	}
	if got := readFile(t, source); got != "contents" {
		t.Fatalf("content %q", got)
	}
}

func TestBackupDoesNotPublishReplacedStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("cloud-source", "source")
	root, err := openBackupRoot(w.configDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	j := &journal{}
	var stage string
	replaced := false
	opHook = func(op, path string) error {
		if op == "rename" && strings.HasPrefix(filepath.Base(path), ".ianchor-tmp-") && !replaced {
			replaced = true
			stage = path
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, _, err = stageBackup(root, "item", source, nil, j)
	if err == nil {
		t.Fatal("replaced stage accepted")
	}
	err = j.fail(err)
	if !replaced {
		t.Fatal("injection did not fire")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, stage+"-saved"); got != "source" {
		t.Fatalf("saved %q", got)
	}
	if _, err := root.root.Lstat("item"); !os.IsNotExist(err) {
		t.Fatalf("foreign published: %v", err)
	}
}

func TestBackupRollbackDoesNotRestoreReplacedPrevious(t *testing.T) {
	w := newWorld(t)
	source := w.write("cloud-source", "new")
	bak := filepath.Join(w.configDir, ".bak")
	if err := os.MkdirAll(bak, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bak, "item"), "old")
	root, err := openBackupRoot(w.configDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	j := &journal{}
	var previous string
	opHook = func(op, path string) error {
		if op == "rename" && strings.Contains(filepath.Base(path), ".ianchor-tmp-") {
			matches, _ := filepath.Glob(filepath.Join(bak, ".ianchor-tmp-*"))
			if len(matches) == 2 {
				for _, p := range matches {
					data, _ := os.ReadFile(p)
					if string(data) == "old" {
						previous = p
					}
				}
				if previous != "" {
					if err := os.Rename(previous, previous+"-saved"); err != nil {
						t.Fatal(err)
					}
					writeFile(t, previous, "foreign")
					return errors.New("injected publish failure")
				}
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, _, err = stageBackup(root, "item", source, nil, j)
	if err == nil {
		t.Fatal("expected publish failure")
	}
	opHook = nil // the injected error happens only once
	err = j.fail(err)
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected rollback failure, got %v", err)
	}
	if got := readFile(t, previous); got != "foreign" {
		t.Fatalf("foreign previous %q", got)
	}
	if got := readFile(t, previous+"-saved"); got != "old" {
		t.Fatalf("old backup %q", got)
	}
}

func TestAddDoesNotPublishReplacedStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "source")
	var stage string
	replaced := false
	opHook = func(op, path string) error {
		if op == "rename" && strings.HasPrefix(filepath.Base(path), ".ianchor-tmp-") && filepath.Dir(path) == filepath.Join(w.configDir, ".bak") && !replaced {
			replaced = true
			stage = path
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("replaced stage accepted")
	}
	if !replaced {
		t.Fatal("injection did not fire")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, stage+"-saved"); got != "source" {
		t.Fatalf("saved %q", got)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "item")); !os.IsNotExist(err) {
		t.Fatalf("foreign published: %v", err)
	}
}

func TestAddRollbackDoesNotMoveReplacedDestination(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "source")
	destination := filepath.Join(w.configDir, "item")
	saved := destination + "-saved"
	opHook = func(op, path string) error {
		if op == "db.add" {
			if err := os.Rename(destination, saved); err != nil {
				t.Fatal(err)
			}
			writeFile(t, destination, "foreign")
			return errors.New("injected db failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("expected failure")
	}
	if got := readFile(t, destination); got != "foreign" {
		t.Fatalf("foreign destination %q", got)
	}
	if got := readFile(t, saved); got != "source" {
		t.Fatalf("saved source %q", got)
	}
}

func TestRemoveRollbackDoesNotRestoreReplacedStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	var stage string
	opHook = func(op, path string) error {
		if op == "db.delete" {
			matches, err := filepath.Glob(filepath.Join(w.configDir, ".bak", ".ianchor-tmp-*"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("stages %v %v", matches, err)
			}
			stage = matches[0]
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
			return errors.New("injected db failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Remove(w.db, w.configDir, "item")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign stage %q", got)
	}
	if got := readFile(t, stage+"-saved"); got != "cloud" {
		t.Fatalf("saved original %q", got)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "item")); !os.IsNotExist(err) {
		t.Fatalf("wrong object restored: %v", err)
	}
}

func TestRemoveCleanupDoesNotDeleteReplacedStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	var stage string
	opHook = func(op, path string) error {
		if op == "db.delete" {
			matches, err := filepath.Glob(filepath.Join(w.configDir, ".bak", ".ianchor-tmp-*"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("stages %v %v", matches, err)
			}
			stage = matches[0]
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Remove(w.db, w.configDir, "item")
	if err == nil {
		t.Fatal("replacement was not reported")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign stage %q", got)
	}
}

func TestRecoverRollbackDoesNotRemoveReoccupiedStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	var stage string
	opHook = func(op, path string) error {
		if op == "rename" && filepath.Dir(path) == filepath.Join(w.configDir, ".bak") && strings.Contains(filepath.Base(path), "ianchor") {
			stage = path
		}
		if op == "remove" && filepath.Dir(path) == filepath.Dir(source) && strings.Contains(filepath.Base(path), "-recover") {
			if stage == "" {
				t.Fatal("stage not observed")
			}
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(stage, "foreign"), "keep")
			return errors.New("injected failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("expected failure: %v", err)
	}
	if got := readFile(t, filepath.Join(stage, "foreign")); got != "keep" {
		t.Fatalf("foreign content %q", got)
	}
}

func TestBackupRootSwappedToSymlinkDoesNotTouchOutside(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "local")
	cloud := filepath.Join(w.configDir, "item")
	writeFile(t, cloud, "cloud")
	backupRoot := filepath.Join(w.configDir, ".bak")
	writeFile(t, filepath.Join(backupRoot, "item"), "previous")
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, filepath.Join(outside, "item"), "foreign data")
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	swapped := false
	opHook = func(op, path string) error {
		if op == "copy" && path == cloud && !swapped {
			swapped = true
			if err := os.Rename(backupRoot, backupRoot+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, backupRoot); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if !swapped {
		t.Fatal("backup root swap did not occur")
	}
	if got := readFile(t, filepath.Join(outside, "item")); got != "foreign data" {
		t.Fatalf("outside modified: %q (%v)", got, err)
	}
	if err == nil {
		t.Fatal("unsafe root replacement was accepted")
	}
}

func TestRecoverRollbackDoesNotDeleteConcurrentBackup(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "local")
	cloud := filepath.Join(w.configDir, "item")
	writeFile(t, cloud, "cloud")
	backup := filepath.Join(w.configDir, ".bak", "item")
	writeFile(t, backup, "old backup")
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	opHook = func(op, path string) error {
		if op == "rename" && path == original {
			if err := os.Remove(backup); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(backup, []byte("concurrent backup"), 0600); err != nil {
				t.Fatal(err)
			}
			return errors.New("injected local staging failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Recover(w.db, w.configDir, "item", true); err == nil {
		t.Fatal("expected error")
	}
	if got := readFile(t, backup); got != "concurrent backup" {
		t.Fatalf("concurrent backup lost: %q", got)
	}
}

func TestRelinkRollbackDoesNotDeleteConcurrentReplacement(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "local")
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	opHook = func(op, path string) error {
		if op == "db.status" {
			if err := os.Remove(original); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(original, []byte("concurrent"), 0600); err != nil {
				t.Fatal(err)
			}
			return errors.New("injected db failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if err := Relink(w.db, w.configDir, "item"); err == nil {
		t.Fatal("expected error")
	}
	if got := readFile(t, original); got != "concurrent" {
		t.Fatalf("concurrent file lost: %q", got)
	}
	if got := readFile(t, original+".bak"); got != "local" {
		t.Fatalf("prior local data lost: %q", got)
	}
}

func TestCrossVolumePublishedTreePreservesSpecialModes(t *testing.T) {
	// Isolates the copy mechanics; this is not a metadata-admission test.
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(source, "nested", "config"), "data")
	for _, path := range []string{source, filepath.Join(source, "nested")} {
		if err := os.Chmod(path, 0755|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(w.configDir, "item"), filepath.Join(w.configDir, "item", "nested")} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSticky == 0 {
			t.Fatalf("published special mode lost: %s %v", path, info.Mode())
		}
	}
}

func TestCrossVolumeStableSpecialPermissionsAreNotMisclassified(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(source, "config"), "data")
	if err := os.Chmod(source, 0755|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatalf("stable source rejected: %v", err)
	}
	if got := readFile(t, filepath.Join(w.configDir, "item", "config")); got != "data" {
		t.Fatalf("content: %q", got)
	}
}

func TestForceReplacementHandlesOldLinkOnOtherVolume(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	next := w.write("new", "new data")
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == old && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: next, Name: "item", Force: true}); err != nil {
		t.Fatalf("force replace: %v", err)
	}
	assertMissing(t, old)
	assertSymlink(t, next, filepath.Join(w.configDir, "item"))
	if got := readFile(t, next); got != "new data" {
		t.Fatalf("new data %q", got)
	}
}

func TestForceReplacementRestoresOldCrossVolumeLinkOnFailure(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	next := w.write("new", "new data")
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == old && first {
			first = false
			return syscall.EXDEV
		}
		if op == "db.upsert" {
			return errors.New("injected db failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: next, Name: "item", Force: true})
	if err == nil || !strings.Contains(err.Error(), "injected db failure") {
		t.Fatalf("failure: %v", err)
	}
	assertSymlink(t, old, filepath.Join(w.configDir, "item"))
	if got := readFile(t, old); got != "old data" {
		t.Fatalf("old %q", got)
	}
	if got := readFile(t, next); got != "new data" {
		t.Fatalf("new %q", got)
	}
}

func TestCrossVolumeCopyCallbackDoesNotClaimReplacement(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	var stage string
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil; copyIntoBackup = fsx.CopyIntoRoot }()
	copyIntoBackup = func(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
		return fsx.CopyIntoRoot(src, root, dst, func(identity os.FileInfo) {
			stage = filepath.Join(w.configDir, ".bak", dst)
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
			created(identity)
		})
	}
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected safe failure: %v", err)
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, source); got != "original" {
		t.Fatalf("source %q", got)
	}
}

func TestCrossVolumePartialCopyDoesNotDeleteReplacement(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	var stage string
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil; copyIntoBackup = fsx.CopyIntoRoot }()
	copyIntoBackup = func(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
		if err := fsx.CopyIntoRoot(src, root, dst, created); err != nil {
			return err
		}
		stage = filepath.Join(w.configDir, ".bak", dst)
		if err := os.Rename(stage, stage+"-saved"); err != nil {
			t.Fatal(err)
		}
		writeFile(t, stage, "foreign")
		return errors.New("injected copy failure")
	}
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil || !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected rollback failure: %v", err)
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, source); got != "original" {
		t.Fatalf("source %q", got)
	}
}

func TestCrossVolumeCleanupDoesNotDeleteReplacedBackupSnapshot(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(source, "config"), "original")
	first := true
	var stage string
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		if op == "db.add" {
			matches, _ := filepath.Glob(filepath.Join(w.configDir, ".bak", ".ianchor-tmp-*-item"))
			if len(matches) != 1 {
				t.Fatalf("snapshot %v", matches)
			}
			stage = matches[0]
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(stage, "foreign"), "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	if got := readFile(t, filepath.Join(stage, "foreign")); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, filepath.Join(stage+"-saved", "config")); got != "original" {
		t.Fatalf("saved %q", got)
	}
}

func TestCrossVolumeRejectsSourceChangeAtStaging(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	calls := 0
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			calls++
			if calls == 1 {
				return syscall.EXDEV
			}
			if calls == 2 {
				writeFile(t, source, "modified")
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("staged source change accepted")
	}
	if got := readFile(t, source); got != "modified" {
		t.Fatalf("source %q", got)
	}
	assertMissing(t, filepath.Join(w.configDir, "item"))
}

func TestCrossVolumeCleanupDoesNotDeleteReplacedSourceStage(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	first := true
	var stage string
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		if op == "db.add" {
			matches, _ := filepath.Glob(filepath.Join(w.localDir, ".ianchor-tmp-*-source"))
			if len(matches) != 1 {
				t.Fatalf("source stages %v", matches)
			}
			stage = matches[0]
			if err := os.Rename(stage, stage+"-saved"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, stage, "foreign")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	if got := readFile(t, stage); got != "foreign" {
		t.Fatalf("foreign %q", got)
	}
	if got := readFile(t, stage+"-saved"); got != "original" {
		t.Fatalf("saved %q", got)
	}
}

func TestCrossVolumeRejectsCorruptPublishedFileAndRetainsOriginal(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil; copyIntoBackup = fsx.CopyIntoRoot }()
	copyIntoBackup = func(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
		if err := fsx.CopyIntoRoot(src, root, dst, created); err != nil {
			return err
		}
		return root.WriteFile(dst, []byte("modified"), 0600)
	}
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil {
		t.Fatal("corrupt copy accepted")
	}
	if got := readFile(t, source); got != "original" {
		t.Fatalf("source %q", got)
	}
	assertMissing(t, filepath.Join(w.configDir, "item"))
	entry, e := w.db.GetEntry("item")
	if e != nil || entry != nil {
		t.Fatalf("entry %v error %v", entry, e)
	}
}

func TestCrossVolumeTransientChangeDuringSecondCopyIsRejected(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	config := filepath.Join(source, "config")
	writeFile(t, config, "old")
	copies := 0
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			return syscall.EXDEV
		}
		if op == "copy" && path == source {
			copies++
			if copies == 2 {
				if err := os.WriteFile(config, []byte("new"), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if op == "rename" && strings.HasSuffix(path, "-item-install") {
			if err := os.WriteFile(config, []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil || copies != 2 || !strings.Contains(err.Error(), "published copy differs") {
		t.Fatalf("expected detected transient change after two copies, got: %v", err)
	}
	if got := readFile(t, config); got != "old" {
		t.Fatalf("source changed: %q", got)
	}
	assertMissing(t, filepath.Join(w.configDir, "item"))
}

func TestCrossVolumeFileChangeBeforeDeletionIsRejected(t *testing.T) {
	w := newWorld(t)
	source := w.write("file", "old")
	copied := false
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			return syscall.EXDEV
		}
		if op == "copy" && path == source {
			copied = true
		}
		if op == "rename" && copied && strings.HasPrefix(filepath.Base(path), ".ianchor-tmp-") {
			copied = false
			if err := os.WriteFile(source, []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil || !strings.Contains(err.Error(), "source changed during cross-volume add") {
		t.Fatalf("expected change error: %v", err)
	}
	if got := readFile(t, source); got != "new" {
		t.Fatalf("source lost: %q", got)
	}
	assertMissing(t, filepath.Join(w.configDir, "item"))
}

func TestCrossVolumeConcurrentSourceChangeSurvivesRollback(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(source, "config"), "old")
	copies := 0
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			return syscall.EXDEV
		}
		if op == "copy" && path == source {
			copies++
			if copies == 2 {
				if err := os.WriteFile(filepath.Join(source, "config"), []byte("new"), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
		if op == "symlink" && path == source {
			return errors.New("symlink failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"})
	if err == nil || !strings.Contains(err.Error(), "source changed during cross-volume add") {
		t.Fatalf("expected detected source change, got %v", err)
	}
	if got := readFile(t, filepath.Join(source, "config")); got != "new" {
		t.Fatalf("concurrent source update lost: %q", got)
	}
}

func TestRollbackFailureReportsRetainedCloudPath(t *testing.T) {
	w := newWorld(t)
	first := w.write("first", "old cloud")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	second := w.write("second", "new local")
	opHook = func(op, path string) error {
		if op == "db.upsert" || (op == "remove" && strings.HasSuffix(path, "-second-undo")) {
			return errors.New("injected")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "item", Force: true})
	if err == nil {
		t.Fatal("expected rollback failure")
	}
	paths, globErr := filepath.Glob(filepath.Join(w.configDir, bakDirName, ".ianchor-tmp-*-item-replaced"))
	if globErr != nil || len(paths) != 1 {
		t.Fatalf("old cloud staging not found: %v %v", paths, globErr)
	}
	if got := readFile(t, paths[0]); got != "old cloud" {
		t.Fatalf("staged data: %q", got)
	}
	if !strings.Contains(err.Error(), paths[0]) {
		t.Fatalf("diagnostic omits retained old data path %s: %v", paths[0], err)
	}
}

func TestRecoverConflictStillRefreshesExistingBackup(t *testing.T) {
	w := newWorld(t)
	writeFile(t, filepath.Join(w.configDir, "item"), "cloud")
	original := w.write("item", "local")
	backup := filepath.Join(w.configDir, bakDirName, "item")
	writeFile(t, backup, "old backup")
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	_, err := Recover(w.db, w.configDir, "item", false)
	if err == nil || !strings.Contains(err.Error(), "File already exists") {
		t.Fatalf("expected conflict: %v", err)
	}
	if got := readFile(t, backup); got != "cloud" {
		t.Fatalf("backup not refreshed: %q", got)
	}
	if got := readFile(t, original); got != "local" {
		t.Fatalf("original changed: %q", got)
	}
	stages, err := filepath.Glob(filepath.Join(w.configDir, bakDirName, ".ianchor-tmp-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("backup stages: %v %v", stages, err)
	}
}

func TestRecoverFailureRestoresPriorBackup(t *testing.T) {
	w := newWorld(t)
	cloud := filepath.Join(w.configDir, "item")
	writeFile(t, cloud, "new cloud")
	original := w.write("item", "local original")
	backup := filepath.Join(w.configDir, bakDirName, "item")
	writeFile(t, backup, "valuable old backup")
	if err := w.db.AddEntry("item", &original, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	injected := errors.New("local copy failed")
	opHook = func(op, path string) error {
		if op == "copy" {
			calls++
			if calls == 2 {
				return injected
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if !errors.Is(err, injected) {
		t.Fatalf("expected copy error: %v", err)
	}
	if got := readFile(t, backup); got != "valuable old backup" {
		t.Fatalf("prior backup lost: %q", got)
	}
	if got := readFile(t, original); got != "local original" {
		t.Fatalf("original lost: %q", got)
	}
	if got := readFile(t, cloud); got != "new cloud" {
		t.Fatalf("cloud changed: %q", got)
	}
}

func TestPartialParentCreationRollsBack(t *testing.T) {
	for _, op := range []string{"recover", "relink"} {
		t.Run(op, func(t *testing.T) {
			w := newWorld(t)
			writeFile(t, filepath.Join(w.configDir, "entry"), "cloud")
			original := filepath.Join(w.localDir, "new-parent", "child", "entry")
			if err := w.db.AddEntry("entry", &original, "file", "unlinked"); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("mkdir failure")
			opHook = func(kind, path string) error {
				if kind == "mkdir" && path == filepath.Dir(original) {
					return injected
				}
				return nil
			}
			defer func() { opHook = nil }()
			var err error
			if op == "recover" {
				_, err = Recover(w.db, w.configDir, "entry", false)
			} else {
				err = Relink(w.db, w.configDir, "entry")
			}
			if !errors.Is(err, injected) {
				t.Fatalf("expected injected mkdir error, got %v", err)
			}
			if _, err := os.Lstat(filepath.Join(w.localDir, "new-parent")); !os.IsNotExist(err) {
				t.Fatalf("partial directory not rolled back: %v", err)
			}
		})
	}
}

func TestForceIgnoresUnrelatedLegacyReservedName(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("ianchor.db.old", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	next := w.write("new", "new data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: next, Name: "item", Force: true}); err != nil {
		t.Fatalf("unrelated legacy entry blocked force: %v", err)
	}
}

func TestForceRejectsHardlinkToOtherTrackedEntry(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(w.configDir, "original"), filepath.Join(w.configDir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	next := w.write("new", "new data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: next, Name: "hardlink", Force: true}); err == nil || !strings.Contains(err.Error(), `Name "hardlink" aliases existing tracked entry "original"`) {
		t.Fatalf("hardlink not rejected for the expected reason: %v", err)
	}
	if entry, err := w.db.GetEntry("hardlink"); err != nil || entry != nil {
		t.Fatalf("unexpected hardlink record: %v %v", entry, err)
	}
	if readFile(t, old) != "old data" || readFile(t, next) != "new data" {
		t.Fatal("source data changed")
	}
}

func TestRecoverPartialBackupWithReadOnlyDirectoryRollsBack(t *testing.T) {
	w := newWorld(t)
	// Ensure a deliberately failed regression can clean its readonly fixtures.
	t.Cleanup(func() {
		_ = filepath.Walk(w.configDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(path, 0700)
			}
			return nil
		})
	})
	source := filepath.Join(w.localDir, "tree")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(source, "a")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(child, 0400); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(source, "b"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "tree"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(w.configDir, ".bak")
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("add left backup root: %v", err)
	}
	_, err := Recover(w.db, w.configDir, "tree", false)
	if err == nil || (!strings.Contains(err.Error(), "unsupported special file") && !strings.Contains(err.Error(), "inaccessible")) {
		t.Fatalf("expected unsafe-copy rejection: %v", err)
	}
	if strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("unexpected incomplete rollback: %v", err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup directory remains: %v", err)
	}
	assertSymlink(t, source, filepath.Join(w.configDir, "tree"))
}

func TestRecoverDirectoryBackupRollbackWithRestrictiveUmask(t *testing.T) {
	if os.Getenv("IANCHOR_TEST_BACKUP_UMASK") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecoverDirectoryBackupRollbackWithRestrictiveUmask$")
		cmd.Env = append(os.Environ(), "IANCHOR_TEST_BACKUP_UMASK=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess: %v\n%s", err, out)
		}
		return
	}
	w := newWorld(t)
	source := filepath.Join(w.localDir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	old := syscall.Umask(0777)
	_, err := Recover(w.db, w.configDir, "item", false)
	syscall.Umask(old)
	if err == nil || strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected recoverable error without rollback failure: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(w.configDir, ".bak"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected backup residue: %v", entries)
	}
}

func TestBackupRootOpenFailurePreservesReplacement(t *testing.T) {
	configDir := t.TempDir()
	backup := filepath.Join(configDir, ".bak")
	swapped := false
	opHook = func(op, path string) error {
		if op == "remove-failed-backup-root" {
			swapped = true
			if err := os.Rename(backup, backup+"-saved"); err != nil {
				return err
			}
			return os.Mkdir(backup, 0700)
		}
		return nil
	}
	defer func() { opHook = nil }()
	old := syscall.Umask(0777)
	root, err := openBackupRoot(configDir)
	syscall.Umask(old)
	if root != nil {
		root.Close()
	}
	if err == nil || !swapped {
		t.Fatalf("expected failure and replacement; swapped=%v err=%v", swapped, err)
	}
	if info, err := os.Lstat(backup); err != nil || !info.IsDir() {
		t.Fatalf("replacement lost: %v", err)
	}
	_ = os.Chmod(backup, 0700)
	_ = os.Chmod(backup+"-saved", 0700)
}

func TestBackupRootOpenFailureRemovesNewDirectory(t *testing.T) {
	configDir := t.TempDir()
	old := syscall.Umask(0777)
	root, err := openBackupRoot(configDir)
	syscall.Umask(old)
	if root != nil {
		root.Close()
	}
	if err == nil {
		t.Fatal("expected open failure under restrictive umask")
	}
	if _, err := os.Lstat(filepath.Join(configDir, ".bak")); !os.IsNotExist(err) {
		t.Fatalf("failed root open left .bak: %v", err)
	}
}

func TestBackupRootRollbackDoesNotRemoveReplacement(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "source")
	if err := os.WriteFile(source, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(w.configDir, ".bak")
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("add left backup root: %v", err)
	}
	injected := errors.New("injected copy failure")
	swapped := false
	opHook = func(op, path string) error {
		if op == "copy" {
			return injected
		}
		if op == "remove-backup-root" && !swapped {
			swapped = true
			if err := os.Rename(backup, backup+"-saved"); err != nil {
				return err
			}
			if err := os.Mkdir(backup, 0700); err != nil {
				return err
			}
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Recover(w.db, w.configDir, "item", false)
	if !swapped || !errors.Is(err, injected) {
		t.Fatalf("swap/failure not reached: swapped=%v err=%v", swapped, err)
	}
	info, err := os.Lstat(backup)
	if err != nil || !info.IsDir() {
		t.Fatalf("replacement was removed: %v", err)
	}
}

func TestFailedOperationsRemoveNewEmptyBackupRoot(t *testing.T) {
	for _, operation := range []string{"recover", "remove"} {
		t.Run(operation, func(t *testing.T) {
			w := newWorld(t)
			source := filepath.Join(w.localDir, "source")
			if err := os.WriteFile(source, []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(w.configDir, ".bak")
			if _, err := os.Lstat(backup); !os.IsNotExist(err) {
				t.Fatalf("add left backup root: %v", err)
			}
			injected := errors.New("injected failure")
			opHook = func(op, path string) error {
				if operation == "recover" && op == "copy" {
					return injected
				}
				if operation == "remove" && op == "db.delete" {
					return injected
				}
				return nil
			}
			defer func() { opHook = nil }()
			var err error
			if operation == "recover" {
				_, err = Recover(w.db, w.configDir, "item", false)
			} else {
				_, err = Remove(w.db, w.configDir, "item")
			}
			if !errors.Is(err, injected) {
				t.Fatalf("failure not injected: %v", err)
			}
			if _, err := os.Lstat(backup); !os.IsNotExist(err) {
				t.Fatalf("new .bak remains: %v", err)
			}
			assertSymlink(t, source, filepath.Join(w.configDir, "item"))
		})
	}
}

func TestAddCrossVolumeResolvesRelativeLinksLikeNode(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../external", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(source, "dangling")); err != nil {
		t.Fatal(err)
	}
	first := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && first {
			first = false
			return syscall.EXDEV
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"link": filepath.Join(w.localDir, "external"), "dangling": filepath.Join(source, "missing")} {
		got, err := os.Readlink(filepath.Join(w.configDir, "item", name))
		if err != nil || got != want {
			t.Fatalf("%s: got %q want %q: %v", name, got, want, err)
		}
	}
}

func TestAddCrossVolumeDatabaseFailureRestoresOriginalLinks(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	firstRename := true
	opHook = func(op, path string) error {
		if op == "rename" && path == source && firstRename {
			firstRename = false
			return syscall.EXDEV
		}
		if op == "db.add" {
			return errors.New("injected database failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err == nil || !strings.Contains(err.Error(), "injected database failure") || strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("expected clean rollback after database failure, got %v", err)
	}
	if fsx.Lexists(filepath.Join(w.configDir, "item")) {
		t.Fatal("cloud copy left after successful rollback")
	}
	data, err := os.ReadFile(filepath.Join(source, "file"))
	if err != nil || string(data) != "data" {
		t.Fatalf("file not restored %q %v", data, err)
	}
	target, err := os.Readlink(filepath.Join(source, "link"))
	if err != nil || target != "file" {
		t.Fatalf("link not restored %q %v", target, err)
	}
}

func TestAddCrossVolumePartialCopyIsCleaned(t *testing.T) {
	w := newWorld(t)
	source := w.write("item", "original")
	opHook = func(op, path string) error {
		if op == "rename" && path == source {
			return syscall.EXDEV
		}
		return nil
	}
	copyIntoBackup = func(src string, root *os.Root, dst string, created func(os.FileInfo)) error {
		if err := root.WriteFile(dst, []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		info, err := root.Lstat(dst)
		if err != nil {
			t.Fatal(err)
		}
		created(info)
		return errors.New("injected partial copy failure")
	}
	defer func() { opHook = nil; copyIntoBackup = fsx.CopyIntoRoot }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "item"}); err == nil || !strings.Contains(err.Error(), "injected partial copy failure") {
		t.Fatalf("expected injected failure: %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "original" {
		t.Fatalf("source changed: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(w.configDir, bakDirName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial staging files remain: %v", entries)
	}
}

func TestRecoverCopyCollisionDoesNotDeleteForeignFile(t *testing.T) {
	w := newWorld(t)
	original := w.write("item", "original")
	cloud := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(cloud, []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.WriteFile(dst, []byte("foreign"), 0600); err != nil {
			t.Fatal(err)
		}
		return copyNodeWithIdentity(src, dst, expected, manifest, created)
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	_, err := Recover(w.db, w.configDir, "item", true)
	if err == nil {
		t.Fatal("expected collision error")
	}
	if got := readFile(t, original); got != "foreign" {
		t.Fatalf("foreign entry lost: %q", got)
	}
	if !strings.Contains(err.Error(), "restore replaced local file from") {
		t.Fatalf("missing recovery instructions: %v", err)
	}
	entries, err := os.ReadDir(w.localDir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ianchor-tmp-") && readFile(t, filepath.Join(w.localDir, entry.Name())) == "original" {
			found = true
		}
	}
	if !found {
		t.Fatal("displaced original was not preserved")
	}
}

func TestRecoverPartialCopyFailureRestoresOriginal(t *testing.T) {
	w := newWorld(t)
	w.write("item/original", "original")
	original := filepath.Join(w.localDir, "item")
	if err := os.Mkdir(filepath.Join(w.configDir, "item"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.configDir, "item", "cloud"), []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "directory", "linked"); err != nil {
		t.Fatal(err)
	}
	copyNodeImpl = func(src, dst string, expected os.FileInfo, manifest *fsx.PermissionManifest, created func(os.FileInfo)) error {
		if err := os.Mkdir(dst, 0700); err != nil {
			t.Fatal(err)
		}
		identity, err := os.Lstat(dst)
		if err != nil {
			return err
		}
		created(identity)
		if err := os.WriteFile(filepath.Join(dst, "partial"), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		return errors.New("injected partial copy failure")
	}
	defer func() { copyNodeImpl = copyNodeWithIdentity }()
	if _, err := Recover(w.db, w.configDir, "item", true); err == nil || !strings.Contains(err.Error(), "injected partial copy failure") {
		t.Fatalf("expected copy failure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(original, "original"))
	if err != nil || string(data) != "original" {
		t.Fatalf("original lost: %q %v", data, err)
	}
}

func TestBackupCopyFailurePreservesExistingBackup(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	backup := backupPath(w.configDir, "item")
	if err := os.MkdirAll(filepath.Dir(backup), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("old backup"), 0600); err != nil {
		t.Fatal(err)
	}
	opHook = func(op, path string) error {
		if op == "copy" {
			return errors.New("injected copy failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := backupToBak(w.configDir, "item", source); err == nil {
		t.Fatal("expected failure")
	}
	data, err := os.ReadFile(backup)
	if err != nil || string(data) != "old backup" {
		t.Fatalf("old backup lost: %q %v", data, err)
	}
}

func TestRecoverRejectsDanglingSymlinkBeforeBackup(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "item")
	target := filepath.Join(w.root, "outside")
	if err := os.Symlink(target, original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.configDir, "item"), []byte("cloud"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(w.db, w.configDir, "item", true); err == nil {
		t.Fatal("dangling symlink accepted")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("outside target was changed: %v", err)
	}
	if got, err := os.Readlink(original); err != nil || got != target {
		t.Fatalf("link changed: %q %v", got, err)
	}
	if _, err := os.Lstat(backupPath(w.configDir, "item")); !os.IsNotExist(err) {
		t.Fatalf("backup changed: %v", err)
	}
}

func TestOperationsRejectLegacyOriginalInsideCloud(t *testing.T) {
	for _, operation := range []string{"remove", "recover", "relink"} {
		t.Run(operation, func(t *testing.T) {
			w := newWorld(t)
			original := filepath.Join(w.configDir, "victim")
			if err := os.WriteFile(original, []byte("must survive"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(w.configDir, "item"), []byte("cloud"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
				t.Fatal(err)
			}
			var err error
			switch operation {
			case "remove":
				_, err = Remove(w.db, w.configDir, "item")
			case "recover":
				_, err = Recover(w.db, w.configDir, "item", true)
			case "relink":
				err = Relink(w.db, w.configDir, "item")
			}
			if err == nil {
				t.Fatal("unsafe original path accepted")
			}
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "must survive" {
				t.Fatalf("victim changed: %q %v", data, err)
			}
		})
	}
}

func TestRemoveRejectsDangerousLegacyName(t *testing.T) {
	w := newWorld(t)
	victim := filepath.Join(filepath.Dir(w.configDir), "victim")
	if err := os.WriteFile(victim, []byte("must survive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("../victim", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(w.db, w.configDir, "../victim"); err == nil {
		t.Fatal("dangerous name was accepted")
	}
	data, err := os.ReadFile(victim)
	if err != nil || string(data) != "must survive" {
		t.Fatalf("victim changed: %q %v", data, err)
	}
	row, err := w.db.GetEntry("../victim")
	if err != nil || row == nil {
		t.Fatalf("legacy record changed: %v %v", row, err)
	}
}

func TestRecursiveCleanupReportsPartialChildDeletion(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "tree")
	if err := os.MkdirAll(filepath.Join(source, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(source, "child", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "tree"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fsx.TestCopyFault = func(src, dst string) error {
		if strings.HasSuffix(src, "/second") {
			calls++
			return errors.New("injected copy interruption")
		}
		return nil
	}
	defer func() { fsx.TestCopyFault = nil }()
	_, err := Recover(w.db, w.configDir, "tree", false)
	if err == nil || !strings.Contains(err.Error(), "injected copy interruption") {
		t.Fatalf("cleanup error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("interruption calls = %d", calls)
	}
	assertSymlink(t, source, filepath.Join(w.configDir, "tree"))
	data, readErr := os.ReadFile(filepath.Join(source, "child", "first"))
	if readErr != nil || string(data) != "first" {
		t.Fatalf("source changed: %q %v", data, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(w.configDir, ".bak")); !os.IsNotExist(statErr) {
		t.Fatalf("backup remains: %v", statErr)
	}
}

func TestRecoverReportsPartialPermissionRestore(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "tree")
	if err := os.MkdirAll(filepath.Join(source, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "child", "item"), []byte("item"), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "tree"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fsx.TestPermissionFault = func(path string) error {
		if strings.HasSuffix(path, "item") {
			calls++
			return errors.New("injected permission restore failure")
		}
		return nil
	}
	defer func() { fsx.TestPermissionFault = nil }()
	_, err := Recover(w.db, w.configDir, "tree", false)
	if err == nil || !strings.Contains(err.Error(), "injected permission restore failure") {
		t.Fatalf("%v", err)
	}
	if calls != 1 {
		t.Fatalf("permission calls = %d", calls)
	}
	assertSymlink(t, source, filepath.Join(w.configDir, "tree"))
	data, readErr := os.ReadFile(filepath.Join(source, "child", "item"))
	if readErr != nil || string(data) != "item" {
		t.Fatalf("source changed: %q %v", data, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(w.configDir, ".bak")); !os.IsNotExist(statErr) {
		t.Fatalf("backup remains: %v", statErr)
	}
}

func TestRecoverConflictDoesNotChmodPreviousBackup(t *testing.T) {
	w := newWorld(t)
	writeFile(t, filepath.Join(w.configDir, "item", "child"), "cloud")
	original := filepath.Join(w.localDir, "item")
	if err := os.MkdirAll(filepath.Join(original, "local-child"), 0700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(w.configDir, bakDirName, "item")
	if err := os.MkdirAll(filepath.Join(backup, "old-child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "old-child", "file"), []byte("old"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(backup, "old-child"), 0500); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "directory", "unlinked"); err != nil {
		t.Fatal(err)
	}
	_, err = Recover(w.db, w.configDir, "item", false)
	if err == nil || !strings.Contains(err.Error(), "File already exists") {
		t.Fatalf("expected conflict: %v", err)
	}
	after, statErr := os.Lstat(backup)
	if statErr != nil || os.SameFile(before, after) || after.Mode().Perm() != 0755 {
		t.Fatalf("published backup: %+v %v", after, statErr)
	}
	stages, globErr := filepath.Glob(filepath.Join(w.configDir, bakDirName, ".ianchor-tmp-*"))
	if globErr != nil || len(stages) != 0 {
		t.Fatalf("old backup retained or chmodded: %v %v", stages, globErr)
	}
}

func TestCleanupRejectsDirectoryReplacedAfterListing(t *testing.T) {
	base := t.TempDir()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Mkdir(filepath.Join(base, "old"), 0700); err != nil {
		t.Fatal(err)
	}
	original := opHook
	opHook = func(op, path string) error {
		if op == "removeall-dir" && path == "old" {
			if err := os.Remove(filepath.Join(base, "old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(base, "old"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "old", "foreign"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}
	t.Cleanup(func() { opHook = original })
	if err := removeAllWithinRoot(root, "old"); err == nil {
		t.Fatal("replaced directory deleted")
	}
	if _, err := os.Lstat(filepath.Join(base, "old", "foreign")); err != nil {
		t.Fatalf("foreign content lost: %v", err)
	}
}

func TestCleanupDoesNotFollowDirectorySymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := removeAllWithinRoot(nil, link)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Lstat(filepath.Join(target, "keep")); statErr != nil {
		t.Fatalf("followed symlink and deleted target: %v", statErr)
	}
	if _, statErr := os.Lstat(link); !os.IsNotExist(statErr) {
		t.Fatalf("symlink remains: %v", statErr)
	}
}
