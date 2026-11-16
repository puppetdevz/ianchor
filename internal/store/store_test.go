package store

import (
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func TestBusyTimeoutPersistsAcrossReopenedConnections(t *testing.T) {
	db, _ := openTemp(t)
	// Expire every connection after use, forcing database/sql to reconnect.
	db.sql.SetConnMaxLifetime(time.Nanosecond)
	for i := 0; i < 3; i++ {
		var timeout int
		if err := db.sql.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		if timeout != 5000 {
			t.Fatalf("connection %d busy_timeout=%d, want 5000", i, timeout)
		}
	}
}

func TestSpecialDatabasePathRoundTrip(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "空 格#?路径")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddEntry("item", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	entry, err := ro.GetEntry("item")
	if err != nil || entry == nil {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dir) {
		t.Fatalf("unexpected root paths: %v", entries)
	}
}

func TestReadOnlyDoesNotInitializeExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ianchor.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("CREATE TABLE unrelated (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := ro.sql.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("mode=%q err=%v", mode, err)
	}
	var count int
	if err := ro.sql.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='entries'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("entries count=%d err=%v", count, err)
	}
	if _, err := ro.sql.Exec("CREATE TABLE forbidden (value TEXT)"); err == nil {
		t.Fatal("driver allowed writes through read-only connection")
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaMatchesBaseline(t *testing.T) {
	db, dir := openTemp(t)
	rows, err := db.sql.Query(`PRAGMA table_info(entries)`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	defer rows.Close()
	type column struct {
		name    string
		kind    string
		notNull int
		dflt    sql.NullString
		pk      int
	}
	var columns []column
	for rows.Next() {
		var c column
		var cid int
		if err := rows.Scan(&cid, &c.name, &c.kind, &c.notNull, &c.dflt, &c.pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		columns = append(columns, c)
	}
	want := []column{
		{name: "id", kind: "INTEGER", notNull: 0, pk: 1},
		{name: "name", kind: "TEXT", notNull: 1, pk: 0},
		{name: "original_path", kind: "TEXT", notNull: 0, pk: 0},
		{name: "type", kind: "TEXT", notNull: 1, pk: 0},
		{name: "status", kind: "TEXT", notNull: 1, dflt: sql.NullString{String: "'linked'", Valid: true}, pk: 0},
		{name: "created_at", kind: "TEXT", notNull: 0, dflt: sql.NullString{String: "datetime('now')", Valid: true}, pk: 0},
		{name: "updated_at", kind: "TEXT", notNull: 0, dflt: sql.NullString{String: "datetime('now')", Valid: true}, pk: 0},
	}
	if len(columns) != len(want) {
		t.Fatalf("columns = %d, want %d: %+v", len(columns), len(want), columns)
	}
	for i, w := range want {
		g := columns[i]
		if g.name != w.name || g.kind != w.kind || g.notNull != w.notNull || g.pk != w.pk || g.dflt != w.dflt {
			t.Fatalf("column %d = %+v, want %+v", i, g, w)
		}
	}
	// The stable file name and WAL mode are part of the on-disk contract.
	if got := db.Path(); got != filepath.Join(dir, "ianchor.db") {
		t.Fatalf("path = %q", got)
	}
	var mode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q", mode)
	}
}

func TestEntryLifecycle(t *testing.T) {
	db, _ := openTemp(t)
	original := "/tmp/x/.zshrc"
	if err := db.AddEntry("zshrc", &original, "file", "linked"); err != nil {
		t.Fatalf("add: %v", err)
	}
	entry, err := db.GetEntry("zshrc")
	if err != nil || entry == nil {
		t.Fatalf("get: %v %v", entry, err)
	}
	if entry.Status != "linked" || *entry.OriginalPath != original || entry.Type != "file" {
		t.Fatalf("entry = %+v", *entry)
	}
	if entry.CreatedAt == nil || entry.UpdatedAt == nil {
		t.Fatalf("timestamps missing: %+v", *entry)
	}
	// Duplicate insert is ignored, matching INSERT OR IGNORE.
	if err := db.AddEntry("zshrc", nil, "directory", "unlinked"); err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	again, _ := db.GetEntry("zshrc")
	if again.Type != "file" || *again.OriginalPath != original {
		t.Fatalf("duplicate insert overwrote the row: %+v", *again)
	}
	if err := db.SetStatus("zshrc", "unlinked"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := db.SetOriginalPath("zshrc", nil); err != nil {
		t.Fatalf("set path: %v", err)
	}
	updated, _ := db.GetEntry("zshrc")
	if updated.Status != "unlinked" || updated.OriginalPath != nil {
		t.Fatalf("updated = %+v", *updated)
	}
	if err := db.DeleteEntry("zshrc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if gone, _ := db.GetEntry("zshrc"); gone != nil {
		t.Fatalf("row still present")
	}
	if err := db.DeleteEntry("zshrc"); err != nil {
		t.Fatalf("delete missing row: %v", err)
	}
}

// The fixture was generated by lib/db.js at git commit 941d933 with the
// locked better-sqlite3 dependency, then closed after WAL checkpoint. It is
// copied per test and never opened in place.
func TestLegacyNodeDatabaseCompatibility(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "legacy-v1.1.1.db"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ianchor.db")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := before.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='entries'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err := before.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var after string
	if err := db.sql.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='entries'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != schema {
		t.Fatalf("legacy schema changed: %q != %q", after, schema)
	}
	entries, err := db.AllEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "Alpha" || entries[1].Name != "zeta" {
		t.Fatalf("legacy ordering: %+v", entries)
	}
	a := entries[0]
	if a.ID != 2 || a.OriginalPath == nil || *a.OriginalPath != "/Users/legacy/.config/a" || a.Type != "file" || a.Status != "linked" || a.CreatedAt == nil || *a.CreatedAt != "2001-02-03 04:05:06" || a.UpdatedAt == nil || *a.UpdatedAt != "2002-03-04 05:06:07" {
		t.Fatalf("legacy Alpha: %+v", a)
	}
	z := entries[1]
	if z.ID != 1 || z.OriginalPath != nil || z.Type != "directory" || z.Status != "unlinked" {
		t.Fatalf("legacy zeta: %+v", z)
	}
	if err := db.UpsertEntry("Alpha", nil, "directory", "unlinked"); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetEntry("Alpha")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != a.ID || *updated.CreatedAt != *a.CreatedAt || updated.OriginalPath != nil || updated.Type != "directory" {
		t.Fatalf("upsert lost legacy fields: %+v", updated)
	}
	if err := db.DeleteEntry("zeta"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddEntry("new", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	n, err := db.GetEntry("new")
	if err != nil || n == nil || n.ID != 4 {
		t.Fatalf("autoincrement after legacy upsert and deletion: %+v %v", n, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	persisted, err := ro.AllEntries()
	if err != nil || len(persisted) != 2 || persisted[0].Name != "Alpha" || persisted[1].Name != "new" {
		t.Fatalf("legacy database after reopen: %+v %v", persisted, err)
	}
}

func TestTimestampSemantics(t *testing.T) {
	db, _ := openTemp(t)
	_, err := db.sql.Exec(`INSERT INTO entries (name, original_path, type, status, created_at, updated_at) VALUES ('clock', NULL, 'file', 'linked', '2000-01-02 03:04:05', '2001-02-03 04:05:06')`)
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.GetEntry("clock")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddEntry("clock", nil, "directory", "unlinked"); err != nil {
		t.Fatal(err)
	}
	duplicate, err := db.GetEntry("clock")
	if err != nil {
		t.Fatal(err)
	}
	if *duplicate.CreatedAt != *before.CreatedAt || *duplicate.UpdatedAt != *before.UpdatedAt {
		t.Fatal("ignored insert changed timestamps")
	}
	if err := db.UpsertEntry("clock", nil, "directory", "unlinked"); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetEntry("clock")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != before.ID || *updated.CreatedAt != *before.CreatedAt {
		t.Fatalf("upsert changed identity or created_at: %+v", updated)
	}
	stamp, err := time.Parse("2006-01-02 15:04:05", *updated.UpdatedAt)
	if err != nil || !stamp.After(time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)) {
		t.Fatalf("invalid updated_at %q: %v", *updated.UpdatedAt, err)
	}
}

func TestOrderingAndNulls(t *testing.T) {
	db, _ := openTemp(t)
	names := []string{"中文 名称", "zeta", "Alpha", "a:b", "-n"}
	for _, name := range names {
		if err := db.AddEntry(name, nil, "file", "unlinked"); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	entries, err := db.AllEntries()
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name)
		if e.OriginalPath != nil {
			t.Fatalf("%s original path = %v, want nil", e.Name, *e.OriginalPath)
		}
	}
	// SQLite's binary collation: uppercase first, then punctuation/digits,
	// then UTF-8 bytes.
	want := []string{"-n", "Alpha", "a:b", "zeta", "中文 名称"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestReadOnly(t *testing.T) {
	db, dir := openTemp(t)
	if err := db.AddEntry("zshrc", nil, "file", "linked"); err != nil {
		t.Fatalf("add: %v", err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("open readonly: %v", err)
	}
	defer ro.Close()
	if entry, err := ro.GetEntry("zshrc"); err != nil || entry == nil {
		t.Fatalf("readonly read: %v %v", entry, err)
	}
	if err := ro.AddEntry("other", nil, "file", "linked"); err == nil {
		t.Fatal("readonly write succeeded")
	} else if !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("readonly error = %v", err)
	}
}

func TestReadOnlyRequiresExistingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenReadOnly(dir); err == nil {
		t.Fatal("expected error for missing database")
	}
}

func TestOpenMissingDirectory(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unable to open database file") {
		t.Fatalf("error = %v", err)
	}
}

func TestBusyTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the busy timeout")
	}
	first, dir := openTemp(t)
	if err := first.AddEntry("one", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// Hold a write transaction so the second connection has to wait.
	if _, err := first.sql.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	start := nowMillis()
	err = second.AddEntry("two", nil, "file", "linked")
	elapsed := nowMillis() - start
	if err == nil {
		t.Fatal("expected lock error")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Fatalf("error = %v", err)
	}
	if elapsed < 4000 {
		t.Fatalf("waited %dms, expected the 5000ms busy timeout", elapsed)
	}
	if _, err := first.sql.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeError(t *testing.T) {
	if NormalizeError(nil) != nil {
		t.Fatal("nil error changed")
	}
	cases := map[string]string{
		"unable to open database file":         "unable to open database file",
		"SQLITE_CANTOPEN":                      "unable to open database file",
		"cannot open database":                 "unable to open database file",
		"database is locked (5) (SQLITE_BUSY)": "database is locked",
		"database table is locked":             "database is locked",
		"attempt to write a readonly database": ErrReadOnly.Error(),
		"unexpected driver text":               "unexpected driver text",
	}
	for input, want := range cases {
		if got := NormalizeError(errString(input)).Error(); got != want {
			t.Fatalf("%q -> %q, want %q", input, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func nowMillis() int64 {
	return time.Now().UnixNano() / 1e6
}

func TestMissingAndReadonlyStoreBranches(t *testing.T) {
	db, dir := openTemp(t)
	original := "/tmp/original"
	if err := db.AddEntry("kept", &original, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetEntry("kept")
	if err != nil || got == nil || got.Name != "kept" || *got.OriginalPath != original {
		t.Fatalf("entry=%+v err=%v", got, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := readonly.Close(); err != nil {
		t.Fatal(err)
	}
	closed, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.sql.Close(); err != nil {
		t.Fatal(err)
	}
	closedOriginal := "/tmp/original"
	for _, call := range []func() error{
		func() error { return closed.AddEntry("closed", &closedOriginal, "file", "linked") },
		func() error { return closed.UpsertEntry("closed", &closedOriginal, "file", "linked") },
		func() error { return closed.SetStatus("closed", "unlinked") },
		func() error { return closed.SetOriginalPath("closed", &closedOriginal) },
		func() error { return closed.DeleteEntry("closed") },
	} {
		if err := call(); err == nil {
			t.Fatal("closed database accepted a statement")
		}
	}
	if _, err := closed.GetEntry("closed"); err == nil {
		t.Fatal("closed database returned a row")
	}
	if _, err := closed.AllEntries(); err == nil {
		t.Fatal("closed database listed rows")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "nested")); err == nil {
		t.Fatal("missing parent accepted as database")
	}
	t.Cleanup(func() { execHook = nil })
	execHook = func(query string) error {
		if strings.Contains(query, "journal_mode") {
			return errString("forced wal failure")
		}
		return nil
	}
	if _, err := Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "forced wal failure") {
		t.Fatalf("wal err=%v", err)
	}
	execHook = func(query string) error {
		if strings.Contains(query, "CREATE TABLE") {
			return errString("forced schema failure")
		}
		return nil
	}
	if _, err := Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "forced schema failure") {
		t.Fatalf("schema err=%v", err)
	}
	execHook = nil
	listed, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer listed.Close()
	if err := listed.AddEntry("listed", nil, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	execHook = func(query string) error {
		if query == "scan entries" {
			return errString("forced scan failure")
		}
		return nil
	}
	if _, err := listed.AllEntries(); err == nil || !strings.Contains(err.Error(), "forced scan failure") {
		t.Fatalf("scan err=%v", err)
	}
	execHook = func(query string) error {
		if query == "entries result" {
			return errString("forced rows failure")
		}
		return nil
	}
	if _, err := listed.AllEntries(); err == nil || !strings.Contains(err.Error(), "forced rows failure") {
		t.Fatalf("rows err=%v", err)
	}
	scanReplacementHook = func() rowScanner { return errScanner{err: errString("forced row scan")} }
	if _, err := listed.AllEntries(); err == nil || !strings.Contains(err.Error(), "forced row scan") {
		t.Fatalf("row err=%v", err)
	}
	scanReplacementHook = nil
	execHook = func(string) error { return errString("forced dsn") }
	if _, err := Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "forced dsn") {
		t.Fatalf("dsn err=%v", err)
	}
	execHook = func(query string) error {
		if query == "open sql" {
			return errString("forced sql open")
		}
		return nil
	}
	if _, err := Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "forced sql open") {
		t.Fatalf("sql err=%v", err)
	}
	execHook = func(query string) error {
		if query == "open rows-err" {
			return errString("forced rows err")
		}
		return nil
	}
	if _, err := listed.AllEntries(); err == nil || !strings.Contains(err.Error(), "forced rows err") {
		t.Fatalf("rows err=%v", err)
	}
	execHook = func(query string) error {
		if query == "open rows" {
			return errString("forced rows result")
		}
		return nil
	}
	if _, err := listed.AllEntries(); err == nil || !strings.Contains(err.Error(), "forced rows result") {
		t.Fatalf("rows result err=%v", err)
	}
	corruptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(corruptDir, "ianchor.db"), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(corruptDir); err == nil {
		t.Fatal("corrupt database opened")
	}
	for _, call := range []func() error{
		func() error { return readonly.AddEntry("kept", &original, "file", "linked") },
		func() error { return readonly.UpsertEntry("kept", &original, "file", "linked") },
		func() error { return readonly.SetStatus("kept", "unlinked") },
		func() error { return readonly.SetOriginalPath("kept", &original) },
		func() error { return readonly.DeleteEntry("kept") },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "readonly") {
			t.Fatalf("readonly err=%v", err)
		}
	}
}

// TestAllEntriesReportsExtraColumnsInOrder proves the list payload keeps the
// SELECT * semantics of the Node implementation: a column added by a future
// version is reported, in column order, instead of being dropped by a fixed
// struct shape.
func TestAllEntriesReportsExtraColumnsInOrder(t *testing.T) {
	db, _ := openTemp(t)
	path := "/tmp/extra"
	if err := db.AddEntry("zshrc", &path, "file", "linked"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	if _, err := db.sql.Exec(`ALTER TABLE entries ADD COLUMN note TEXT`); err != nil {
		t.Fatalf("ALTER TABLE: %v", err)
	}
	if _, err := db.sql.Exec(`UPDATE entries SET note = 'kept'`); err != nil {
		t.Fatalf("UPDATE note: %v", err)
	}
	entries, err := db.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatalf("Marshal entry: %v", err)
	}
	want := `{"id":1,"name":"zshrc","original_path":"/tmp/extra","type":"file","status":"linked","created_at":"` +
		*entries[0].CreatedAt + `","updated_at":"` + *entries[0].UpdatedAt + `","note":"kept"}`
	if string(encoded) != want {
		t.Fatalf("entry JSON =\n%s\nwant\n%s", encoded, want)
	}
	entry, err := db.GetEntry("zshrc")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	single, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal single: %v", err)
	}
	if string(single) != want {
		t.Fatalf("GetEntry JSON =\n%s\nwant\n%s", single, want)
	}
}

// TestEntryMarshalUsesSchemaColumnsWhenBuiltInCode covers entries that never
// came from a row: they still marshal in schema order.
func TestEntryMarshalUsesSchemaColumnsWhenBuiltInCode(t *testing.T) {
	entry := Entry{ID: 7, Name: "vimrc", Type: "file", Status: "linked"}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"id":7,"name":"vimrc","original_path":null,"type":"file","status":"linked","created_at":null,"updated_at":null}`
	if string(encoded) != want {
		t.Fatalf("JSON = %s, want %s", encoded, want)
	}
}

// TestEntryJSONMatchesNodeDriverValues pins the payload for a database carrying
// columns beyond the schema, against output captured from the Node
// implementation (better-sqlite3 12.10.0): opacity bytes become a Buffer, a
// column declared DATETIME keeps its stored text, integers past 2^53 round like
// a JavaScript number, infinity becomes null, and numeric column names are
// enumerated before the rest.
func TestEntryJSONMatchesNodeDriverValues(t *testing.T) {
	db, _ := openTemp(t)
	for _, statement := range []string{
		`ALTER TABLE entries ADD COLUMN blob_extra BLOB`,
		`ALTER TABLE entries ADD COLUMN dt DATETIME`,
		`ALTER TABLE entries ADD COLUMN "10" TEXT`,
		`ALTER TABLE entries ADD COLUMN "2" TEXT`,
		`ALTER TABLE entries ADD COLUMN "01" TEXT`,
		`ALTER TABLE entries ADD COLUMN big INTEGER`,
		`ALTER TABLE entries ADD COLUMN inf REAL`,
		`ALTER TABLE entries ADD COLUMN "ctrl` + "\x01" + `" TEXT`,
	} {
		if _, err := db.sql.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := db.sql.Exec(`INSERT INTO entries
		(name, original_path, type, status, blob_extra, dt, "10", "2", "01", big, inf, "ctrl`+"\x01"+`")
		VALUES ('n1', '/tmp/x', 'file', 'linked', ?, '2024-01-02 03:04:05', 'ten', 'two', 'zero-one', 9007199254740993, ?, 'c')`,
		[]byte{0, 1, 255}, math.Inf(1)); err != nil {
		t.Fatalf("insert n1: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO entries (name, original_path, type, status, blob_extra)
		VALUES ('n2', '/tmp/y', 'file', 'linked', ?)`, []byte{}); err != nil {
		t.Fatalf("insert n2: %v", err)
	}

	entry, err := db.GetEntry("n1")
	if err != nil || entry == nil {
		t.Fatalf("GetEntry: %+v %v", entry, err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"2":"two","10":"ten","id":1,"name":"n1","original_path":"/tmp/x",` +
		`"type":"file","status":"linked","created_at":"` + *entry.CreatedAt +
		`","updated_at":"` + *entry.UpdatedAt +
		`","blob_extra":{"type":"Buffer","data":[0,1,255]},"dt":"2024-01-02 03:04:05",` +
		`"01":"zero-one","big":9007199254740992,"inf":null,"ctrl\u0001":"c"}`
	if string(encoded) != want {
		t.Fatalf("GetEntry JSON =\n%s\nwant\n%s", encoded, want)
	}
	// The typed fields stay usable: they carry the stored text for ianchor's own
	// decisions, not the coerced driver value.
	if entry.Status != "linked" || entry.CreatedAt == nil || *entry.CreatedAt == "" {
		t.Fatalf("typed fields = %+v", entry)
	}

	// The list path appends ORDER BY, so it repeats the whole read through the
	// stored-value statement.
	entries, err := db.AllEntries()
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	first, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatalf("Marshal first: %v", err)
	}
	if string(first) != want {
		t.Fatalf("list JSON =\n%s\nwant\n%s", first, want)
	}
	second, err := json.Marshal(entries[1])
	if err != nil {
		t.Fatalf("Marshal second: %v", err)
	}
	wantEmpty := `{"2":null,"10":null,"id":2,"name":"n2","original_path":"/tmp/y",` +
		`"type":"file","status":"linked","created_at":"` + *entries[1].CreatedAt +
		`","updated_at":"` + *entries[1].UpdatedAt +
		`","blob_extra":{"type":"Buffer","data":[]},"dt":null,` +
		`"01":null,"big":null,"inf":null,"ctrl\u0001":null}`
	if string(second) != wantEmpty {
		t.Fatalf("empty BLOB JSON =\n%s\nwant\n%s", second, wantEmpty)
	}
}

// TestEntryMarshalRejectsUnencodableValue covers a stored value JSON cannot
// represent: the failure must surface instead of being silently dropped.
func TestEntryMarshalRejectsUnencodableValue(t *testing.T) {
	entry := Entry{
		order: append(append([]string{}, schemaColumns...), "note"),
		raw:   map[string]any{"note": make(chan int)},
	}
	if _, err := json.Marshal(entry); err == nil {
		t.Fatal("Marshal accepted an unencodable value")
	}
}

// TestMarshalKeyKeepsLineSeparators pins that column names are escaped with JSON
// rules: a control character becomes \u0001, as JSON.stringify writes it, while
// the line separators stay literal characters because Go would escape them.
func TestMarshalKeyKeepsLineSeparators(t *testing.T) {
	if got := string(marshalKey("ctrl\x01")); got != `"ctrl\u0001"` {
		t.Fatalf("control key = %s", got)
	}
	if got := string(marshalKey("a\u2028b")); got != "\"a\u2028b\"" {
		t.Fatalf("line separator key = %s", got)
	}
}

// TestMarshalNodeEncodesBooleansAndNulls covers the value kinds a stored column
// cannot produce but a caller still can.
func TestMarshalNodeEncodesBooleansAndNulls(t *testing.T) {
	missing := (*string)(nil)
	path := "/tmp/x"
	for _, testCase := range []struct {
		name  string
		value any
		want  string
	}{
		{"bool", true, "true"},
		{"nil", nil, "null"},
		{"nil string", missing, "null"},
		{"string pointer", &path, `"/tmp/x"`},
		{"nan", math.NaN(), "null"},
		{"number", 12.5, "12.5"},
		{"int", int64(7), "7"},
	} {
		encoded, err := marshalNode(testCase.value)
		if err != nil || string(encoded) != testCase.want {
			t.Fatalf("%s: %s %v, want %s", testCase.name, encoded, err, testCase.want)
		}
	}
}

// TestIsArrayIndex covers the canonical array-index test JavaScript uses to
// order numeric property names.
func TestIsArrayIndex(t *testing.T) {
	for name, want := range map[string]bool{
		"":              false,
		"2":             true,
		"0":             true,
		"01":            false,
		"2a":            false,
		"4294967294":    true,
		"4294967295":    false,
		"9999999999999": false,
		"-1":            false,
	} {
		if got := isArrayIndex(name); got != want {
			t.Fatalf("isArrayIndex(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestStoredValueDecoding covers what the typed fields accept, so a column with
// an unexpected storage class cannot smuggle a value into ianchor's decisions.
func TestStoredValueDecoding(t *testing.T) {
	if got := intValue(3.0); got != 3 {
		t.Fatalf("intValue(float64) = %d", got)
	}
	if got := intValue("3"); got != 0 {
		t.Fatalf("intValue(string) = %d", got)
	}
	if text, ok := textValue([]byte("bytes")); !ok || text != "bytes" {
		t.Fatalf("textValue(bytes) = %q %v", text, ok)
	}
	if _, ok := textValue(nil); ok {
		t.Fatal("textValue(null) reported a value")
	}
	if got := quoteIdentifier(`we"ird`); got != `"we""ird"` {
		t.Fatalf("quoteIdentifier = %s", got)
	}
	if got := (Entry{}).value("note"); got != nil {
		t.Fatalf("value for an unknown column = %v", got)
	}
}

// TestScanColumnsFailureIsReported covers a result set that cannot describe
// itself, for both read paths, and the empty GetEntry result probe.
func TestScanColumnsFailureIsReported(t *testing.T) {
	db, _ := openTemp(t)
	path := "/tmp/cols"
	if err := db.AddEntry("zshrc", &path, "file", "linked"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	scanColumns = func(*sql.Rows) ([]string, error) { return nil, errString("forced columns") }
	t.Cleanup(func() { scanColumns = (*sql.Rows).Columns })

	if _, err := db.GetEntry("zshrc"); err == nil || !strings.Contains(err.Error(), "forced columns") {
		t.Fatalf("GetEntry error = %v, want forced columns", err)
	}
	if _, err := db.AllEntries(); err == nil || !strings.Contains(err.Error(), "forced columns") {
		t.Fatalf("AllEntries error = %v, want forced columns", err)
	}
}

// TestGetEntryEmptyResultReportsRowsError covers the no-row result probe: a
// failure surfacing while the empty result set is drained must be reported, not
// turned into a missing entry.
func TestGetEntryEmptyResultReportsRowsError(t *testing.T) {
	db, _ := openTemp(t)
	execHook = func(query string) error {
		if query == "open rows-err" {
			return errString("forced empty rows")
		}
		return nil
	}
	t.Cleanup(func() { execHook = nil })
	if entry, err := db.GetEntry("absent"); err == nil || !strings.Contains(err.Error(), "forced empty rows") {
		t.Fatalf("GetEntry = %v, %v, want forced empty rows", entry, err)
	}
}

// TestEntryJSONKeepsBlobInKnownColumn covers opaque bytes stored in one of the
// schema's own columns: the Node implementation spreads the row it read, so the
// value stays a Buffer instead of becoming text.
func TestEntryJSONKeepsBlobInKnownColumn(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.sql.Exec(`INSERT INTO entries (name, original_path, type, status)
		VALUES ('n1', '/tmp/x', ?, 'linked')`, []byte("bin")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	entry, err := db.GetEntry("n1")
	if err != nil || entry == nil {
		t.Fatalf("GetEntry: %+v %v", entry, err)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"id":1,"name":"n1","original_path":"/tmp/x","type":{"type":"Buffer","data":[98,105,110]},` +
		`"status":"linked","created_at":"` + *entry.CreatedAt + `","updated_at":"` + *entry.UpdatedAt + `"}`
	if string(encoded) != want {
		t.Fatalf("JSON =\n%s\nwant\n%s", encoded, want)
	}
	if entry.Type != "bin" {
		t.Fatalf("typed type = %q, want the text view", entry.Type)
	}
}

// TestEntryJSONAppendsLiveStatusForUppercaseColumns pins what the Node
// implementation reports for a hand-written schema with uppercase column names,
// captured from better-sqlite3 12.10.0: the row keeps its own column names, the
// typed fields stay empty, and the list command's live status is appended.
func TestEntryJSONAppendsLiveStatusForUppercaseColumns(t *testing.T) {
	db, _ := openTemp(t)
	if _, err := db.sql.Exec(`DROP TABLE entries`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if _, err := db.sql.Exec(`CREATE TABLE entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		"ORIGINAL_PATH" TEXT,
		"TYPE" TEXT NOT NULL,
		"STATUS" TEXT NOT NULL DEFAULT 'linked',
		"CREATED_AT" TEXT DEFAULT (datetime('now')),
		"UPDATED_AT" TEXT DEFAULT (datetime('now')))`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO entries (name, ORIGINAL_PATH, TYPE, STATUS)
		VALUES ('n1', '/tmp/x', 'file', 'linked')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	entry, err := db.GetEntry("n1")
	if err != nil || entry == nil {
		t.Fatalf("GetEntry: %+v %v", entry, err)
	}
	if entry.OriginalPath != nil || entry.Status != "" {
		t.Fatalf("typed fields = %+v, want empty like Node's row", entry)
	}
	entry.Status = "unlinked"
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"id":1,"name":"n1","ORIGINAL_PATH":"/tmp/x","TYPE":"file","STATUS":"linked",` +
		`"CREATED_AT":"` + entry.raw["CREATED_AT"].(string) + `","UPDATED_AT":"` +
		entry.raw["UPDATED_AT"].(string) + `","status":"unlinked"}`
	if string(encoded) != want {
		t.Fatalf("JSON =\n%s\nwant\n%s", encoded, want)
	}
}
