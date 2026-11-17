package anchor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/puppetdevz/ianchor/internal/store"
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

// TestRemoveRefusesAFileAnotherRowStillTracks is the reverse direction of the
// alias: two rows name one file, so moving it would delete what the other row
// still tracks. The refusal names the row that owns the file.
func TestRemoveRefusesAFileAnotherRowStillTracks(t *testing.T) {
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
		t.Fatal("a file tracked by two rows was removed")
	}
	want := `Refusing to remove "toolrc": its iCloud copy is also tracked as entry "ToolRC". Run "ianchor remove ToolRC" first.`
	if err.Error() != want {
		t.Fatalf("err = %q\nwant  %q", err, want)
	}
	if _, err := os.Lstat(cloud); err != nil {
		t.Fatalf("shared copy changed: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRemoveAllowsACaseOnlyRenameOnDisk pins the other half of DELTA-019: a
// file renamed inside the config directory is still the entry's file, so it
// stays removable even though its spelling no longer matches the row.
func TestRemoveAllowsACaseOnlyRenameOnDisk(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "ToolRC"}); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(w.configDir, "ToolRC")
	if err := os.Rename(cloud, filepath.Join(w.configDir, "toolrc")); err != nil {
		t.Fatal(err)
	}
	result, err := Remove(w.db, w.configDir, "ToolRC")
	if err != nil {
		t.Fatalf("case-only rename was refused: %v", err)
	}
	if result.Name != "ToolRC" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); !os.IsNotExist(err) {
		t.Fatalf("cloud entry survived: %v", err)
	}
	if _, err := os.Lstat(first); !os.IsNotExist(err) {
		t.Fatalf("symlink survived: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRemoveSkipsRowsWhoseCopyIsGone proves a stale row cannot block the
// removal of an unrelated entry: only a live, same-file row is a conflict.
func TestRemoveSkipsRowsWhoseCopyIsGone(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "toolrc"}); err != nil {
		t.Fatal(err)
	}
	stale := w.write("stale/other", "two")
	if err := dbAdd(w.db, "other", &stale, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(w.db, w.configDir, "toolrc"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "other" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRemoveRefusesALegacyNameThatNamesThisFile proves a path-shaped legacy
// row is caught when it resolves back to the file being removed, instead of
// being skipped because its name is not a single component.
func TestRemoveRefusesALegacyNameThatNamesThisFile(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "toolrc"}); err != nil {
		t.Fatal(err)
	}
	escape := "/tmp/x"
	if err := dbAdd(w.db, "../ianchor/toolrc", &escape, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	_, err := Remove(w.db, w.configDir, "toolrc")
	want := `Refusing to remove "toolrc": its iCloud copy is also tracked as entry "../ianchor/toolrc". Run "ianchor remove ../ianchor/toolrc" first.`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %q", err, want)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); err != nil {
		t.Fatalf("cloud entry changed: %v", err)
	}
}

// TestRemoveSkipsALegacyNameThatNamesAnotherFile proves the alias check does
// not block every removal in a database that still holds a path-shaped row:
// only a row that resolves to this very file is a conflict.
func TestRemoveSkipsALegacyNameThatNamesAnotherFile(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "toolrc"}); err != nil {
		t.Fatal(err)
	}
	escape := "/tmp/x"
	if err := dbAdd(w.db, "../escape", &escape, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(w.db, w.configDir, "toolrc"); err != nil {
		t.Fatalf("unrelated legacy row blocked the removal: %v", err)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "../escape" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRemoveReportsAnUnreadableOtherCopy proves the alias check fails closed
// when another row's location cannot be inspected for a reason other than
// "missing": skipping it would delete a file that row might still track.
func TestRemoveReportsAnUnreadableOtherCopy(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "toolrc"}); err != nil {
		t.Fatal(err)
	}
	other := w.write("other/other", "two")
	if err := dbAdd(w.db, "other", &other, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	restore := lstatPath
	lstatPath = func(path string) (os.FileInfo, error) {
		if filepath.Base(path) == "other" && filepath.Base(filepath.Dir(path)) == filepath.Base(w.configDir) {
			return nil, os.ErrPermission
		}
		return os.Lstat(path)
	}
	t.Cleanup(func() { lstatPath = restore })
	_, err := Remove(w.db, w.configDir, "toolrc")
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want the unreadable row reported", err)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); err != nil {
		t.Fatalf("cloud entry changed: %v", err)
	}
}

// TestAddRefusesALegacyNameThatNamesTheNewCopy proves the add-side alias check
// follows the path a legacy name resolves to, so it cannot recreate the
// two-rows-one-file state DELTA-019 rules out.
func TestAddRefusesALegacyNameThatNamesTheNewCopy(t *testing.T) {
	w := newWorld(t)
	escape := "/tmp/x"
	if err := dbAdd(w.db, "../ianchor/toolrc", &escape, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	source := w.write("first/toolrc", "one")
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "toolrc"})
	want := `"toolrc" differs only by case from tracked entry "../ianchor/toolrc", which is the same file name on macOS. Run "ianchor remove ../ianchor/toolrc" first.`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %q", err, want)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); !os.IsNotExist(err) {
		t.Fatalf("cloud entry created: %v", err)
	}
}

// TestAddIgnoresALegacyNameThatNamesAnotherFile proves a path-shaped row that
// points elsewhere does not block adding the entry.
func TestAddIgnoresALegacyNameThatNamesAnotherFile(t *testing.T) {
	w := newWorld(t)
	escape := "/tmp/x"
	if err := dbAdd(w.db, "../escape", &escape, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	source := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "toolrc"}); err != nil {
		t.Fatalf("unrelated legacy row blocked the add: %v", err)
	}
}

// TestAddForceStillRefusesACaseVariantOfAStaleRow pins the documented scope of
// DELTA-019: the refusal is not a takeover `--force` can express, so a stale
// row spelled differently still has to be removed first.
func TestAddForceStillRefusesACaseVariantOfAStaleRow(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "ToolRC"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w.configDir, "ToolRC")); err != nil {
		t.Fatal(err)
	}
	second := w.write("second/toolrc", "two")
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "toolrc", Force: true})
	want := `"toolrc" differs only by case from tracked entry "ToolRC", which is the same file name on macOS. Run "ianchor remove ToolRC" first.`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %q", err, want)
	}
	rows, err := w.db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "ToolRC" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestRemoveReportsRowListingFailure proves a database that cannot list its
// rows fails the removal instead of proceeding without the alias check.
func TestRemoveReportsRowListingFailure(t *testing.T) {
	w := newWorld(t)
	first := w.write("first/toolrc", "one")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "toolrc"}); err != nil {
		t.Fatal(err)
	}
	restore := dbAllEntries
	dbAllEntries = func(*store.DB) ([]store.Entry, error) { return nil, errors.New("rows refused") }
	t.Cleanup(func() { dbAllEntries = restore })
	_, err := Remove(w.db, w.configDir, "toolrc")
	if err == nil || !strings.Contains(err.Error(), "rows refused") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(w.configDir, "toolrc")); err != nil {
		t.Fatalf("cloud entry changed: %v", err)
	}
}
