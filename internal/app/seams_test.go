package app

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/puppetdevz/ianchor/internal/anchor"
	"github.com/puppetdevz/ianchor/internal/completions"
	"github.com/puppetdevz/ianchor/internal/store"
)

// TestSeamsDefaultToRealCalls guards the base assumption of the coverage
// strategy: with no test replacing a seam, every seam is the real call. Four of
// these branches exist only because a seam can be replaced, so a seam that
// quietly became a stub would make the shipped code differ from the tested one.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	direct := map[string][2]uintptr{
		"openDBFunc":       {reflect.ValueOf(openDBFunc).Pointer(), reflect.ValueOf(store.Open).Pointer()},
		"listEntries":      {reflect.ValueOf(listEntries).Pointer(), reflect.ValueOf(anchor.List).Pointer()},
		"completionScript": {reflect.ValueOf(completionScript).Pointer(), reflect.ValueOf(completions.Script).Pointer()},
	}
	for name, pair := range direct {
		if pair[0] != pair[1] {
			t.Errorf("%s is not the real call", name)
		}
	}

	// encodeListPayload must keep the encoder settings of `list --json`: no
	// HTML escaping and two-space indentation.
	encoded, err := encodeListPayload([]store.Entry{{ID: 1, Name: "<name>", Type: "file", Status: "linked"}})
	if err != nil {
		t.Fatalf("encodeListPayload: %v", err)
	}
	if want := "[\n  {\n    \"id\": 1,\n    \"name\": \"<name>\",\n"; len(encoded) < len(want) || encoded[:len(want)] != want {
		t.Fatalf("encodeListPayload = %q, want the list --json encoding", encoded)
	}
	var decoded []store.Entry
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("encodeListPayload produced invalid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Name != "<name>" {
		t.Fatalf("encodeListPayload round trip = %+v", decoded)
	}

	// entryAfterRelink must be a plain row read.
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	cloud := filepath.Join(dir, "entry")
	if err := db.AddEntry("entry", &cloud, "file", "linked"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	entry, err := entryAfterRelink(db, "entry")
	if err != nil {
		t.Fatalf("entryAfterRelink: %v", err)
	}
	if entry.Name != "entry" || entry.OriginalPath == nil || *entry.OriginalPath != cloud {
		t.Fatalf("entryAfterRelink = %+v, want the stored row", entry)
	}
}
