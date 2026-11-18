package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDeleteEntryIfUnchangedPreservesStorageClasses(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.sql.Exec(`INSERT INTO entries(name, type, status) VALUES ('blob', x'66696c65', 'linked')`); err != nil {
		t.Fatal(err)
	}
	entry, err := db.GetEntry("blob")
	if err != nil || entry == nil {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	if deleted, err := db.DeleteEntryIfUnchanged(entry); err != nil || !deleted {
		t.Fatalf("deleted=%v err=%v", deleted, err)
	}
	if deleted, err := db.DeleteEntryIfUnchanged(&Entry{Name: "missing"}); err != nil || deleted {
		t.Fatalf("fallback deleted=%v err=%v", deleted, err)
	}
}

func TestDeleteEntryIfUnchanged(t *testing.T) {
	db, dir := openTemp(t)
	if err := db.AddEntry("item", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	entry, err := db.GetEntry("item")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertEntry("item", nil, "directory", "linked"); err != nil {
		t.Fatal(err)
	}
	if deleted, err := db.DeleteEntryIfUnchanged(entry); err != nil || deleted {
		t.Fatalf("changed: deleted=%v err=%v", deleted, err)
	}
	entry, err = db.GetEntry("item")
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := db.DeleteEntryIfUnchanged(entry); err != nil || !deleted {
		t.Fatalf("unchanged: deleted=%v err=%v", deleted, err)
	}
	if deleted, err := db.DeleteEntryIfUnchanged(entry); err != nil || deleted {
		t.Fatalf("missing: deleted=%v err=%v", deleted, err)
	}
	readonly, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if _, err := readonly.DeleteEntryIfUnchanged(entry); err != ErrReadOnly {
		t.Fatalf("readonly error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DeleteEntryIfUnchanged(entry); err == nil {
		t.Fatal("expected closed database error")
	}
}

func TestAddEntryExclusive(t *testing.T) {
	db, dir := openTemp(t)
	if err := db.AddEntryExclusive("item", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddEntryExclusive("item", nil, "file", "linked"); err == nil {
		t.Fatal("expected duplicate error")
	}
	readonly, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if err := readonly.AddEntryExclusive("other", nil, "file", "linked"); err != ErrReadOnly {
		t.Fatalf("readonly error = %v", err)
	}
}

func TestAddDiscoveredAtomicAndConflicts(t *testing.T) {
	db, dir := openTemp(t)
	original := filepath.Join(dir, "original")
	if err := db.AddEntry("existing", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	count, err := db.AddDiscovered([]DiscoveredEntry{
		{Name: "existing", Type: "directory"},
		{Name: "new", Type: "file"},
	})
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	existing, err := db.GetEntry("existing")
	if err != nil || existing == nil || existing.OriginalPath == nil || *existing.OriginalPath != original || existing.Status != "linked" {
		t.Fatalf("existing=%+v err=%v", existing, err)
	}
	newEntry, err := db.GetEntry("new")
	if err != nil || newEntry == nil || newEntry.Status != "unlinked" {
		t.Fatalf("new=%+v err=%v", newEntry, err)
	}
}

func TestAddDiscoveredRollsBackBatchFailure(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.sql.Exec(`CREATE TRIGGER reject_bad BEFORE INSERT ON entries WHEN NEW.name = 'bad' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := db.AddDiscovered([]DiscoveredEntry{{Name: "good", Type: "file"}, {Name: "bad", Type: "file"}})
	if err == nil {
		t.Fatal("expected insert failure")
	}
	entry, err := db.GetEntry("good")
	if err != nil || entry != nil {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

func TestAddDiscoveredReportsRollbackFailure(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.sql.Exec(`CREATE TRIGGER rollback_bad BEFORE INSERT ON entries WHEN NEW.name = 'bad' BEGIN SELECT RAISE(ROLLBACK, 'forced rollback'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := db.AddDiscovered([]DiscoveredEntry{{Name: "good", Type: "file"}, {Name: "bad", Type: "file"}})
	if err == nil || !strings.Contains(err.Error(), "discovery rollback failed") {
		t.Fatalf("expected rollback failure to be reported: %v", err)
	}
	entry, err := db.GetEntry("good")
	if err != nil || entry != nil {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

func TestAddDiscoveredCommitFailure(t *testing.T) {
	db, _ := openTemp(t)
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE parent (name TEXT PRIMARY KEY)`,
		`CREATE TABLE children (name TEXT REFERENCES parent(name) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER deferred_failure AFTER INSERT ON entries BEGIN INSERT INTO children(name) VALUES (NEW.name); END`,
	} {
		if _, err := db.sql.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.AddDiscovered([]DiscoveredEntry{{Name: "orphan", Type: "file"}}); err == nil {
		t.Fatal("expected commit failure")
	}
	entry, err := db.GetEntry("orphan")
	if err != nil || entry != nil {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
}

func TestAddDiscoveredRejectsReadOnlyAndClosed(t *testing.T) {
	db, dir := openTemp(t)
	readonly, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if _, err := readonly.AddDiscovered(nil); err != ErrReadOnly {
		t.Fatalf("readonly error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddDiscovered(nil); err == nil {
		t.Fatal("expected closed database error")
	}
}
