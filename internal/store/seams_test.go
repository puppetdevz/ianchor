package store

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

// seamAddress returns the code pointer of fn, which is how the seam guards in
// this repository compare a package-level variable with the real call.
func seamAddress(fn any) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

// TestSeamsDefaultToRealCalls keeps every seam in this package honest: a seam
// whose default is not the real call would let the tests pass while production
// took a different path. Add a seam here when you add one to the package.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	// execHook and scanReplacementHook are injection points only: production
	// must leave them nil so no hook can silently divert a real call.
	if execHook != nil {
		t.Error("execHook is not nil in production")
	}
	if scanReplacementHook != nil {
		t.Error("scanReplacementHook is not nil in production")
	}
	// scanColumns is a seam over a real call, so its default must be that call.
	if got, want := seamAddress(scanColumns), seamAddress((*sql.Rows).Columns); got != want {
		t.Errorf("scanColumns = %#x, want (*sql.Rows).Columns %#x", got, want)
	}
	// storedValueStatement is the same kind of seam: its default must be the
	// real describer, not a stub that would skip the stored-value re-read.
	if got, want := seamAddress(storedValueStatement), seamAddress(describeStoredValues); got != want {
		t.Errorf("storedValueStatement = %#x, want describeStoredValues %#x", got, want)
	}

	// execHook is nil in production; when a test installs it the hook must be
	// the function the seam calls. Prove that by observing the callback.
	called := ""
	execHook = func(query string) error { called = query; return nil }
	t.Cleanup(func() { execHook = nil })
	if err := execHookError("probe"); err != nil {
		t.Fatalf("execHookError: %v", err)
	}
	if called != "probe" {
		t.Fatalf("hook called with %q, want probe", called)
	}

	// scanReplacementHook is nil in production; the seam must return nil, and
	// a replacement once installed.
	if scanReplacement() != nil {
		t.Fatal("scanReplacement returned a scanner without a hook")
	}
	installed := errScanner{err: errString("probe")}
	scanReplacementHook = func() rowScanner { return installed }
	t.Cleanup(func() { scanReplacementHook = nil })
	if got := scanReplacement(); got != installed {
		t.Fatalf("scanReplacement = %v, want the installed scanner", got)
	}

	// scanColumns must still describe a real result set.
	db, _ := openTemp(t)
	path := "/tmp/seams"
	if err := db.AddEntry("zshrc", &path, "file", "linked"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	rows, err := db.sql.Query(selectAll)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	columns, err := scanColumns(rows)
	if err != nil {
		t.Fatalf("scanColumns: %v", err)
	}
	if !reflect.DeepEqual(columns, schemaColumns) {
		t.Fatalf("columns = %v, want %v", columns, schemaColumns)
	}

	// The real describer must report no rewrite for this schema and a rewrite
	// for a column whose declared type is a date. Rows are closed first because
	// SQLite serialises statements on one database file.
	statement, err := storedValueStatement(columns, rows)
	if err != nil || statement != "" {
		t.Fatalf("storedValueStatement = %q, %v, want no rewrite", statement, err)
	}
	rows.Close()
	dated, _ := openTemp(t)
	if _, err := dated.sql.Exec(`CREATE TABLE dated (id INTEGER PRIMARY KEY, stamp DATETIME)`); err != nil {
		t.Fatalf("create dated table: %v", err)
	}
	datedRows, err := dated.sql.Query("SELECT * FROM dated")
	if err != nil {
		t.Fatalf("query dated: %v", err)
	}
	defer datedRows.Close()
	datedColumns, err := scanColumns(datedRows)
	if err != nil {
		t.Fatalf("dated columns: %v", err)
	}
	statement, err = storedValueStatement(datedColumns, datedRows)
	if err != nil {
		t.Fatalf("dated storedValueStatement: %v", err)
	}
	if !strings.Contains(statement, "CASE WHEN 1") {
		t.Fatalf("dated storedValueStatement = %q, want a CASE wrapper", statement)
	}
}
