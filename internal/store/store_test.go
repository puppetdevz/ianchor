package store

import (
	"database/sql"
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
