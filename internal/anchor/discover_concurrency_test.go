package anchor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovePreservesReplacementAfterReadingSymlink(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "item")
	cloud := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(cloud, []byte("cloud"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cloud, original); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	old := readlinkPath
	t.Cleanup(func() { readlinkPath = old })
	readlinkPath = func(path string) (string, error) {
		target, err := old(path)
		if path == original && err == nil {
			if err := os.Rename(path, path+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return target, err
	}
	if _, err := Remove(w.db, w.configDir, "item"); err == nil {
		t.Fatal("expected replacement to be detected")
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement lost: %q, %v", data, err)
	}
}

func TestRemovePreservesBothReplacementsWhenRestoreConflicts(t *testing.T) {
	w := newWorld(t)
	original := filepath.Join(w.localDir, "item")
	cloud := filepath.Join(w.configDir, "item")
	if err := os.WriteFile(cloud, []byte("cloud"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cloud, original); err != nil {
		t.Fatal(err)
	}
	if err := w.db.AddEntry("item", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	oldReadlink, oldLstat := readlinkPath, lstatPath
	t.Cleanup(func() { readlinkPath, lstatPath = oldReadlink, oldLstat })
	readlinkPath = func(path string) (string, error) {
		target, err := oldReadlink(path)
		if path == original && err == nil {
			if err := os.Rename(path, path+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("first replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return target, err
	}
	var staged string
	lstatPath = func(path string) (os.FileInfo, error) {
		info, err := oldLstat(path)
		if err == nil && strings.Contains(filepath.Base(path), "-removed-link") {
			staged = path
			if err := os.WriteFile(original, []byte("second replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return info, err
	}
	_, err := Remove(w.db, w.configDir, "item")
	if err == nil || staged == "" || !strings.Contains(err.Error(), staged) {
		t.Fatalf("stage=%q err=%v", staged, err)
	}
	if got := readFile(t, original); got != "second replacement" {
		t.Fatalf("original = %q", got)
	}
	if got := readFile(t, staged); got != "first replacement" {
		t.Fatalf("stage = %q", got)
	}
}

func TestAddDoesNotOverwriteConcurrentAddWithoutForce(t *testing.T) {
	w := newWorld(t)
	first := w.write("first", "first data")
	second := w.write("second", "second data")
	var hook func(string, string) error
	hook = func(op, path string) error {
		if op == "rename" && path == first {
			opHook = nil
			_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "shared"})
			opHook = hook
			if err != nil {
				t.Fatalf("concurrent add: %v", err)
			}
		}
		return nil
	}
	setHook(t, hook)
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "shared"}); err == nil {
		t.Fatal("expected the competing add to prevent overwrite")
	}
	if got := readFile(t, first); got != "first data" {
		t.Fatalf("first source = %q", got)
	}
	if got := readFile(t, filepath.Join(w.configDir, "shared")); got != "second data" {
		t.Fatalf("shared copy = %q", got)
	}
	row, err := w.db.GetEntry("shared")
	if err != nil || row == nil || row.OriginalPath == nil || *row.OriginalPath != second {
		t.Fatalf("row = %+v, %v", row, err)
	}
}

func TestRemoveDoesNotDeleteConcurrentForceAdd(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	newSource := w.write("new", "new data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "shared"}); err != nil {
		t.Fatal(err)
	}
	var hook func(string, string) error
	hook = func(op, path string) error {
		if op == "db.delete" && path == "shared" {
			opHook = nil
			_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: newSource, Name: "shared", Force: true})
			opHook = hook
			if err != nil {
				t.Fatalf("concurrent force add: %v", err)
			}
		}
		return nil
	}
	setHook(t, hook)
	_, removeErr := Remove(w.db, w.configDir, "shared")
	if removeErr == nil || !strings.Contains(removeErr.Error(), "changed during removal") {
		t.Fatalf("expected conflict: %v", removeErr)
	}
	if got := readFile(t, filepath.Join(w.configDir, "shared")); got != "new data" {
		t.Fatalf("new shared content = %q", got)
	}
	row, err := w.db.GetEntry("shared")
	if err != nil || row == nil || row.OriginalPath == nil || *row.OriginalPath != newSource {
		t.Fatalf("force-add row lost: %+v err=%v", row, err)
	}
}

func TestAddRejectsConcurrentRowInsertion(t *testing.T) {
	w := newWorld(t)
	source := w.write("source", "source data")
	other := filepath.Join(w.localDir, "other")
	setHook(t, func(op, name string) error {
		if op == "db.add" && name == "shared" {
			return w.db.AddEntry(name, &other, "file", "linked")
		}
		return nil
	})
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "shared"}); err == nil {
		t.Fatal("expected insertion conflict")
	}
	if got := readFile(t, source); got != "source data" {
		t.Fatalf("source=%q", got)
	}
	row, err := w.db.GetEntry("shared")
	if err != nil || row == nil || row.OriginalPath == nil || *row.OriginalPath != other {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "shared")); !os.IsNotExist(err) {
		t.Fatalf("unexpected cloud copy: %v", err)
	}
}

func TestDiscoverDoesNotCountConcurrentInsert(t *testing.T) {
	w := newWorld(t)
	if err := os.WriteFile(filepath.Join(w.configDir, "a"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(w.localDir, "other")
	setHook(t, func(op, name string) error {
		if op == "db.add" && name == "a" {
			return w.db.AddEntry(name, &original, "file", "linked")
		}
		return nil
	})
	count, err := discover(w.db, w.configDir)
	if err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	entry, err := w.db.GetEntry("a")
	if err != nil || entry == nil || entry.OriginalPath == nil || *entry.OriginalPath != original || entry.Status != "linked" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

// A row inserted by another operation between discovery's lookup and insert
// belongs to that operation and must survive a subsequent discovery failure.
func TestDiscoverRollbackPreservesConcurrentInsert(t *testing.T) {
	w := newWorld(t)
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(w.configDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setHook(t, func(op, name string) error {
		if op == "db.add" && name == "a" {
			path := filepath.Join(w.localDir, "other")
			return w.db.AddEntry(name, &path, "file", "linked")
		}
		if op == "db.add" && name == "b" {
			return errors.New("injected failure")
		}
		return nil
	})
	if _, err := discover(w.db, w.configDir); err == nil {
		t.Fatal("expected injected failure")
	}
	entry, err := w.db.GetEntry("a")
	if err != nil || entry == nil || entry.OriginalPath == nil || *entry.OriginalPath != filepath.Join(w.localDir, "other") {
		t.Fatalf("concurrent entry lost: entry=%+v err=%v", entry, err)
	}
}
