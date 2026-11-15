//go:build darwin

package anchor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForceRejectsDifferentRecordWithSameFilesystemName(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "Foo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.configDir, "foo")); err != nil {
		t.Skipf("case-sensitive volume: %v", err)
	}
	next := w.write("new", "new data")
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: next, Name: "foo", Force: true})
	if err == nil || !strings.Contains(err.Error(), `Name "foo" aliases existing tracked entry "Foo"`) {
		t.Fatalf("case alias was not rejected for the expected reason: %v", err)
	}
	if got := readFile(t, old); got != "old data" {
		t.Fatalf("old data %q", got)
	}
	if got := readFile(t, next); got != "new data" {
		t.Fatalf("new data %q", got)
	}
	entry, e := w.db.GetEntry("foo")
	if e != nil || entry != nil {
		t.Fatalf("unexpected second entry %v, %v", entry, e)
	}
}

func TestForceRejectsUnicodeAliasOfTrackedName(t *testing.T) {
	w := newWorld(t)
	old := w.write("old", "old data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: old, Name: "caf\u00e9"}); err != nil {
		t.Fatal(err)
	}
	alias := "cafe\u0301"
	if _, err := os.Lstat(filepath.Join(w.configDir, alias)); err != nil {
		t.Skipf("volume does not expose Unicode alias: %v", err)
	}
	next := w.write("new", "new data")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: next, Name: alias, Force: true}); err == nil || !strings.Contains(err.Error(), "aliases existing tracked entry") || !strings.Contains(err.Error(), alias) || !strings.Contains(err.Error(), "caf\u00e9") {
		t.Fatalf("unicode alias was not rejected for the expected reason: %v", err)
	}
	if readFile(t, old) != "old data" || readFile(t, next) != "new data" {
		t.Fatal("source data changed")
	}
	if entry, err := w.db.GetEntry(alias); err != nil || entry != nil {
		t.Fatalf("unexpected entry: %v %v", entry, err)
	}
}

func TestUnicodeAliasDoesNotEscapeICloudBoundary(t *testing.T) {
	w := newWorld(t)
	config := filepath.Join(filepath.Dir(w.configDir), "caf\u00e9")
	if err := os.Mkdir(config, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(config), "cafe\u0301")
	if _, err := os.Stat(alias); err != nil {
		t.Skipf("volume does not expose Unicode alias: %v", err)
	}
	source := filepath.Join(alias, "file")
	writeFile(t, source, "data")
	if _, err := Add(w.db, AddOptions{ConfigDir: config, Source: source, Name: "safe"}); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("unicode alias accepted: %v", err)
	}
	legacy := filepath.Join(alias, "missing", "file")
	if err := validateOriginal(config, &legacy); err == nil {
		t.Fatalf("unicode legacy path accepted: %s", legacy)
	}
}

func TestCaseAliasDoesNotEscapeICloudBoundary(t *testing.T) {
	w := newWorld(t)
	alias := filepath.Join(filepath.Dir(w.configDir), strings.ToUpper(filepath.Base(w.configDir)))
	if _, err := os.Stat(alias); err != nil {
		t.Skipf("case-sensitive test volume does not expose case alias: %v", err)
	}
	source := filepath.Join(alias, "ianchor.db")
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: "foreign"})
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("alias source accepted: %v", err)
	}
	legacy := filepath.Join(alias, "missing-parent", "file")
	if err := validateOriginal(w.configDir, &legacy); err == nil {
		t.Fatalf("alias legacy path accepted: %s", legacy)
	}
}
