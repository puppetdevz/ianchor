package app_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestListAndCompleteReportBrokenEntryTable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "ianchor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "ianchor.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE entries (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI([]string{"list"}, "")
	if code == 0 || !strings.Contains(stderr, "Error:") || stdout != "" {
		t.Fatalf("list code=%d out=%q err=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI([]string{"__complete"}, "")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("complete code=%d out=%q err=%q", code, stdout, stderr)
	}
}
