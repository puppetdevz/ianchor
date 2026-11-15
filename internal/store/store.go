// Package store is the SQLite-backed registry of tracked config entries.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Schema is byte-identical to the schema the Node implementation created, so
// existing ianchor.db files keep working without a migration.
const Schema = `CREATE TABLE IF NOT EXISTS entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  original_path TEXT,
  type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'linked',
  created_at TEXT DEFAULT (datetime('now')),
  updated_at TEXT DEFAULT (datetime('now'))
);`

// busyTimeoutMillis matches better-sqlite3's default 5000ms busy timeout.
const busyTimeoutMillis = 5000

// execHook is nil in production. Tests use it to force a database execution
// error without changing the statements issued on the normal path.
var execHook func(query string) error

// Entry mirrors one row of the entries table. Nullable columns use pointers so
// JSON marshalling can emit null exactly like the Node implementation.
type Entry struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	OriginalPath *string `json:"original_path"`
	Type         string  `json:"type"`
	Status       string  `json:"status"`
	CreatedAt    *string `json:"created_at"`
	UpdatedAt    *string `json:"updated_at"`
}

// DB is an open handle to ianchor.db.
type DB struct {
	sql      *sql.DB
	path     string
	readonly bool
}

// ErrReadOnly is returned when a write is attempted on a read-only handle.
var ErrReadOnly = errors.New("attempt to write a readonly database")

// Path returns the database file location.
func (db *DB) Path() string { return db.path }

// Open opens (creating if needed) the database inside configDir and enables
// WAL, like the Node implementation does.
func Open(configDir string) (*DB, error) {
	return open(filepath.Join(configDir, "ianchor.db"), false)
}

// OpenReadOnly opens an existing database without creating or modifying it.
func OpenReadOnly(configDir string) (*DB, error) {
	return open(filepath.Join(configDir, "ianchor.db"), true)
}

func open(path string, readonly bool) (*DB, error) {
	dsn, err := checkedDSN(path, readonly)
	if err != nil {
		return nil, err
	}
	conn, err := openSQL(dsn)
	if err != nil {
		return nil, NormalizeError(err)
	}
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, NormalizeError(err)
	}
	db := &DB{sql: conn, path: path, readonly: readonly}
	if !readonly {
		if err := execChecked(conn, "PRAGMA journal_mode = WAL"); err != nil {
			conn.Close()
			return nil, NormalizeError(err)
		}
		if err := execChecked(conn, Schema); err != nil {
			conn.Close()
			return nil, NormalizeError(err)
		}
	}
	return db, nil
}

func checkedDSN(path string, readonly bool) (string, error) {
	if err := openSetupError("dsn"); err != nil {
		return "", err
	}
	return dsnFor(path, readonly)
}

func openSQL(dsn string) (*sql.DB, error) {
	if err := openSetupError("sql"); err != nil {
		return nil, err
	}
	return sql.Open("sqlite", dsn)
}

func openSetupError(stage string) error {
	if execHook == nil {
		return nil
	}
	return execHook("open " + stage)
}

func execChecked(conn *sql.DB, query string) error {
	if err := execHookError(query); err != nil {
		return err
	}
	_, err := conn.Exec(query)
	return err
}

func execHookError(query string) error {
	if execHook == nil {
		return nil
	}
	return execHook(query)
}

func dsnFor(path string, readonly bool) (string, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMillis))
	if readonly {
		q.Set("mode", "ro")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Close releases the handle.
func (db *DB) Close() error { return db.sql.Close() }

// NormalizeError turns driver noise into the stable wording ianchor reports.
func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to open database file"),
		strings.Contains(msg, "SQLITE_CANTOPEN"),
		strings.Contains(msg, "cannot open"):
		return errors.New("unable to open database file")
	case strings.Contains(msg, "database is locked"),
		strings.Contains(msg, "SQLITE_BUSY"),
		strings.Contains(msg, "database table is locked"):
		return errors.New("database is locked")
	case strings.Contains(msg, "readonly database"):
		return ErrReadOnly
	}
	return errors.New(msg)
}

func (db *DB) writable() error {
	if db.readonly {
		return ErrReadOnly
	}
	return nil
}

// AddEntry inserts a new row only when the name is free, mirroring
// INSERT OR IGNORE.
func (db *DB) AddEntry(name string, originalPath *string, kind, status string) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(
		"INSERT OR IGNORE INTO entries (name, original_path, type, status) VALUES (?, ?, ?, ?)",
		name, originalPath, kind, status,
	)
	return NormalizeError(err)
}

// UpsertEntry records a name, taking over an existing row when --force lets
// ianchor replace whatever lived in the iCloud directory. id and created_at are
// preserved; original_path, type, status and updated_at follow the new source.
func (db *DB) UpsertEntry(name string, originalPath *string, kind, status string) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(`INSERT INTO entries (name, original_path, type, status) VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
  original_path = excluded.original_path,
  type = excluded.type,
  status = excluded.status,
  updated_at = datetime('now')`,
		name, originalPath, kind, status)
	return NormalizeError(err)
}

// GetEntry returns the row for name, or nil when it is not tracked.
func (db *DB) GetEntry(name string) (*Entry, error) {
	row := db.sql.QueryRow(selectColumns+" FROM entries WHERE name = ?", name)
	entry, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, NormalizeError(err)
	}
	return entry, nil
}

// GetEntryByID returns the row with the given id, or nil.
func (db *DB) GetEntryByID(id int64) (*Entry, error) {
	row := db.sql.QueryRow(selectColumns+" FROM entries WHERE id = ?", id)
	entry, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, NormalizeError(err)
	}
	return entry, nil
}

const selectColumns = "SELECT id, name, original_path, type, status, created_at, updated_at"

// AllEntries returns every tracked entry ordered by name, like the Node list
// command.
func (db *DB) AllEntries() ([]Entry, error) {
	rows, err := db.sql.Query(selectColumns + " FROM entries ORDER BY name")
	if err != nil {
		return nil, NormalizeError(err)
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		if err := execHookError("scan entries"); err != nil {
			return nil, NormalizeError(err)
		}
		scanner := rowScanner(rows)
		if replacement := scanReplacement(); replacement != nil {
			scanner = replacement
		}
		entry, err := scanEntry(scanner)
		if err != nil {
			return nil, NormalizeError(err)
		}
		entries = append(entries, *entry)
	}
	if err := rowsResultError(rows); err != nil {
		return nil, NormalizeError(err)
	}
	if err := execHookError("entries result"); err != nil {
		return nil, NormalizeError(err)
	}
	return entries, nil
}

// SetStatus updates the status column and refreshes updated_at.
func (db *DB) SetStatus(name, status string) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(
		"UPDATE entries SET status = ?, updated_at = datetime('now') WHERE name = ?",
		status, name,
	)
	return NormalizeError(err)
}

// SetOriginalPath updates original_path and refreshes updated_at.
func (db *DB) SetOriginalPath(name string, originalPath *string) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(
		"UPDATE entries SET original_path = ?, updated_at = datetime('now') WHERE name = ?",
		originalPath, name,
	)
	return NormalizeError(err)
}

// DeleteEntry removes the row for name.
func (db *DB) DeleteEntry(name string) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec("DELETE FROM entries WHERE name = ?", name)
	return NormalizeError(err)
}

// PutEntry writes a full row, preserving id and timestamps. It is used to undo
// a delete during compensation.
func (db *DB) PutEntry(entry Entry) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(
		"INSERT INTO entries (id, name, original_path, type, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		entry.ID, entry.Name, entry.OriginalPath, entry.Type, entry.Status, entry.CreatedAt, entry.UpdatedAt,
	)
	return NormalizeError(err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

type errScanner struct{ err error }

func (s errScanner) Scan(dest ...any) error { return s.err }

var scanReplacementHook func() rowScanner

func rowsResultError(rows interface{ Err() error }) error {
	err := rows.Err()
	if injected := openSetupError("rows-err"); err == nil {
		err = injected
	}
	if err != nil {
		return err
	}
	return openSetupError("rows")
}

func scanReplacement() rowScanner {
	if scanReplacementHook == nil {
		return nil
	}
	return scanReplacementHook()
}

func scanEntry(row rowScanner) (*Entry, error) {
	var (
		entry     Entry
		orig      sql.NullString
		createdAt sql.NullString
		updatedAt sql.NullString
	)
	if err := row.Scan(&entry.ID, &entry.Name, &orig, &entry.Type, &entry.Status, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if orig.Valid {
		v := orig.String
		entry.OriginalPath = &v
	}
	if createdAt.Valid {
		v := createdAt.String
		entry.CreatedAt = &v
	}
	if updatedAt.Valid {
		v := updatedAt.String
		entry.UpdatedAt = &v
	}
	return &entry, nil
}

// RestoreEntry writes a full row back, replacing any row with the same name or
// id. It is the compensation path for a delete or an overwrite.
func (db *DB) RestoreEntry(entry Entry) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(`INSERT OR REPLACE INTO entries (id, name, original_path, type, status, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.Name, entry.OriginalPath, entry.Type, entry.Status, entry.CreatedAt, entry.UpdatedAt)
	return NormalizeError(err)
}

// RestoreFields restores original_path, type, status and timestamps of an
// existing row, preserving its id.
func (db *DB) RestoreFields(entry Entry) error {
	if err := db.writable(); err != nil {
		return err
	}
	_, err := db.sql.Exec(
		"UPDATE entries SET original_path = ?, type = ?, status = ?, created_at = ?, updated_at = ? WHERE name = ?",
		entry.OriginalPath, entry.Type, entry.Status, entry.CreatedAt, entry.UpdatedAt, entry.Name)
	return NormalizeError(err)
}
