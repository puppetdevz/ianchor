package anchor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests below pin DELTA-019. macOS resolves "toolrc" and "ToolRC" to one
// file, so two rows spelled differently can point at the same iCloud copy:
// remove under the other spelling would move the file the first row still
// tracks. Both directions are refused with the name that owns the file.

func TestAddRefusesACaseVariantOfAStaleRow(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "ToolRC"}); err != nil {
		t.Fatal(err)
	}
	// The copy is gone but the row survives, as after a manual delete; adding
	// "toolrc" would otherwise create a second row for the same file.
	if err := os.Remove(filepath.Join(w.configDir, "ToolRC")); err != nil {
		t.Fatal(err)
	}
	second := w.write("second/toolrc", "two")
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "toolrc"})
	if err == nil {
		t.Fatal("case variant of a stale row was accepted")
	}
	want := `"toolrc" differs only by case from tracked entry "ToolRC", which is the same file name on macOS. Run "ianchor remove ToolRC" first.`
	if err.Error() != want {
		t.Fatalf("err = %q\nwant  %q", err, want)
	}
	if _, err := os.Lstat(second); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); !os.IsNotExist(err) {
		t.Fatalf("cloud entry created: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "ToolRC" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestAddForceRefusesACaseVariantOfALiveRow(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "ToolRC"}); err != nil {
		t.Fatal(err)
	}
	// A case-insensitive volume already resolves the second spelling; a
	// case-sensitive one needs the hard link to model the same file.
	linkSpelling(t, filepath.Join(w.configDir, "ToolRC"), filepath.Join(w.configDir, "toolrc"))
	second := w.write("second/toolrc", "two")
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "toolrc", Force: true})
	if err == nil {
		t.Fatal("force accepted a case variant of a live row")
	}
	want := `Name "toolrc" aliases existing tracked entry "ToolRC"`
	if err.Error() != want {
		t.Fatalf("err = %q\nwant  %q", err, want)
	}
	if _, err := os.Lstat(second); err != nil {
		t.Fatalf("source changed: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "ToolRC" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestRemoveRefusesACaseVariantSpelling(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "ToolRC"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "ToolRC")
	linkSpelling(t, cloud, filepath.Join(w.configDir, "toolrc"))
	original := first
	if err := dbAdd(w.db, "toolrc", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	_, err := Remove(w.db, w.configDir, "toolrc")
	if err == nil {
		t.Fatal("case variant spelling was removed")
	}
	want := `Refusing to remove "toolrc": its iCloud copy is stored as "ToolRC", which is another entry name for the same file. Run "ianchor remove ToolRC" first.`
	if err.Error() != want {
		t.Fatalf("err = %q\nwant  %q", err, want)
	}
	if _, err := os.Lstat(cloud); err != nil {
		t.Fatalf("shared copy changed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); err != nil {
		t.Fatalf("second spelling changed: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestStoredEntryNameReportsListingFailures(t *testing.T) {
	w := newWorld(t)
	path := filepath.Join(w.configDir, "toolrc")
	if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	restore := readDirPath
	readDirPath = func(string) ([]os.DirEntry, error) { return nil, errors.New("listing refused") }
	t.Cleanup(func() { readDirPath = restore })
	name, err := storedEntryName(w.configDir, "toolrc", victim)
	if err == nil || !strings.Contains(err.Error(), "listing refused") {
		t.Fatalf("err = %v", err)
	}
	if name != "" {
		t.Fatalf("name = %q", name)
	}
}

// linkSpelling makes alias resolve to the same file as path. A
// case-insensitive volume already does that without a second directory entry.
func linkSpelling(t *testing.T, path, alias string) {
	t.Helper()
	if _, err := os.Lstat(alias); err == nil {
		return
	}
	if err := os.Link(path, alias); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
}

func TestStoredEntryNameKeepsLookingAfterAnUnreadableSpelling(t *testing.T) {
	w := newWorld(t)
	spelling := filepath.Join(w.configDir, "ToolRC")
	for _, path := range []string{spelling, filepath.Join(w.configDir, "other")} {
		if err := os.WriteFile(path, []byte("one"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	victim, err := os.Lstat(spelling)
	if err != nil {
		t.Fatal(err)
	}
	restore := lstatPath
	lstatPath = func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "ToolRC" {
			return nil, errors.New("lstat refused")
		}
		return os.Lstat(path)
	}
	t.Cleanup(func() { lstatPath = restore })
	// The only spelling that matches resolves to the victim, but its lstat
	// fails, so the helper reports that it found no readable spelling.
	name, err := storedEntryName(w.configDir, "toolrc", victim)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if name != "" {
		t.Fatalf("name = %q", name)
	}
}

func TestRemoveReportsAnUnlistableConfigDirectory(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	cloud := filepath.Join(w.configDir, "toolrc")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "toolrc"}); err != nil {
		t.Fatal(err)
	}
	restore := readDirPath
	readDirPath = func(string) ([]os.DirEntry, error) { return nil, errors.New("listing refused") }
	t.Cleanup(func() { readDirPath = restore })
	_, err := Remove(w.db, w.configDir, "toolrc")
	if err == nil || !strings.Contains(err.Error(), "listing refused") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(cloud); err != nil {
		t.Fatalf("cloud entry changed: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "linked" {
		t.Fatalf("rows = %+v", rows)
	}
}
