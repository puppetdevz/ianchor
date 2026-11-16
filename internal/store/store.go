// Package store is the SQLite-backed registry of tracked config entries.
package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
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

	// order is the column order the database returned, and raw holds those
	// columns' values as the driver reported them. The Node implementation read
	// rows with SELECT *, so a database that gained columns keeps reporting them
	// in JS property order; order is nil for entries built in code, which then
	// marshal as schemaColumns.
	order []string
	raw   map[string]any
}

// schemaColumns is the column order Schema creates.
var schemaColumns = []string{"id", "name", "original_path", "type", "status", "created_at", "updated_at"}

// MarshalJSON writes the columns in JS property order with the values the Node
// driver reports, so the payload keeps SELECT * semantics instead of a fixed
// struct shape.
func (e Entry) MarshalJSON() ([]byte, error) {
	columns := e.order
	if columns == nil {
		columns = schemaColumns
	}
	columns = nodeKeyOrder(columns)
	if !hasColumn(columns, "status") {
		// The Node list command spreads { ...entry, status: live }, so a table
		// whose columns lack status gains the live one at the end.
		columns = append(columns, "status")
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, column := range columns {
		if i > 0 {
			buf.WriteByte(',')
		}
		value, err := marshalNode(e.value(column))
		if err != nil {
			return nil, err
		}
		buf.Write(marshalKey(column))
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// hasColumn reports whether columns already names column.
func hasColumn(columns []string, column string) bool {
	for _, name := range columns {
		if name == column {
			return true
		}
	}
	return false
}

// value returns the reported value of column: the live status the list command
// just computed, the stored value for everything else, the typed field only for
// entries built in code. That is the shape Node's `{ ...entry, status }` spread
// produces, so opaque bytes stay a Buffer wherever they were stored.
func (e Entry) value(column string) any {
	if column == "status" {
		return e.Status
	}
	if stored, ok := e.raw[column]; ok {
		return stored
	}
	switch column {
	case "id":
		return e.ID
	case "name":
		return e.Name
	case "original_path":
		return e.OriginalPath
	case "type":
		return e.Type
	case "created_at":
		return e.CreatedAt
	case "updated_at":
		return e.UpdatedAt
	}
	return nil
}

// nodeKeyOrder puts the columns in the order JSON.stringify would enumerate
// them: canonical array-index keys first, ascending, then the rest in column
// order. A database column named "2" therefore precedes "name" exactly like a
// JavaScript object property does.
func nodeKeyOrder(columns []string) []string {
	indexes := make([]string, 0, len(columns))
	rest := make([]string, 0, len(columns))
	for _, column := range columns {
		if !isArrayIndex(column) {
			rest = append(rest, column)
			continue
		}
		indexes = append(indexes, column)
	}
	sort.Slice(indexes, func(i, j int) bool {
		left, _ := strconv.ParseUint(indexes[i], 10, 64)
		right, _ := strconv.ParseUint(indexes[j], 10, 64)
		return left < right
	})
	return append(indexes, rest...)
}

// isArrayIndex reports whether name is a canonical array index, the only string
// keys JavaScript orders numerically instead of by insertion.
func isArrayIndex(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	if len(name) > 1 && name[0] == '0' {
		return false
	}
	value, err := strconv.ParseUint(name, 10, 64)
	return err == nil && value <= math.MaxUint32-1
}

// marshalKey encodes a column name with JSON string rules. Encoding/json writes
// the line separators U+2028 and U+2029 as escapes while JSON.stringify leaves
// them in place, so they are put back.
func marshalKey(column string) []byte {
	encoded, _ := marshalJSON(column)
	encoded = bytes.ReplaceAll(encoded, []byte(`\u2028`), []byte("\u2028"))
	return bytes.ReplaceAll(encoded, []byte(`\u2029`), []byte("\u2029"))
}

// marshalNode encodes a stored value the way the Node driver reports it: opaque
// bytes arrive as a Buffer, numbers as JavaScript numbers (so non-finite values
// become null and integers beyond 2^53 round), null as null.
func marshalNode(value any) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return []byte("null"), nil
	case *string:
		if v == nil {
			return []byte("null"), nil
		}
		return marshalJSON(*v)
	case string:
		return marshalJSON(v)
	case int64:
		return marshalNumber(float64(v))
	case float64:
		return marshalNumber(v)
	case bool:
		return marshalJSON(v)
	case []byte:
		return marshalBuffer(v), nil
	}
	return marshalJSON(value)
}

// marshalNumber encodes a JavaScript number. JSON.stringify writes null for NaN
// and the infinities, which is what a caller sees instead of a failed command.
func marshalNumber(value float64) ([]byte, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return []byte("null"), nil
	}
	return marshalJSON(value)
}

// marshalBuffer encodes bytes as the object JSON.stringify builds for a Node
// Buffer, including the empty one.
func marshalBuffer(value []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"type":"Buffer","data":[`)
	for i, b := range value {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(strconv.Itoa(int(b)))
	}
	buf.WriteString("]}")
	return buf.Bytes()
}

// marshalJSON encodes value without escaping <, > and &, matching
// JSON.stringify in the Node implementation.
func marshalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
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
	entries, err := db.readEntries(" WHERE name = ?", name)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return &entries[0], nil
}

// selectAll mirrors the Node implementation's SELECT *, so a database carrying
// extra columns reports them too.
const selectAll = "SELECT * FROM entries"

// AllEntries returns every tracked entry ordered by name, like the Node list
// command.
func (db *DB) AllEntries() ([]Entry, error) {
	return db.readEntries(" ORDER BY name")
}

// readEntries runs the entries query with where appended. When the result set
// carries a column declared as a date type, the driver would coerce its text
// into a time.Time; the stored values are read once more through a statement
// that keeps them, because the Node driver reports the stored text.
func (db *DB) readEntries(where string, args ...any) ([]Entry, error) {
	statement := selectAll + where
	for {
		entries, storedValues, err := db.queryEntries(statement, args...)
		if err != nil || storedValues == "" {
			return entries, err
		}
		statement = storedValues + where
	}
}

// queryEntries runs one statement. When its result set would lose stored values
// to driver coercion, it returns the statement that reads them instead of the
// rows, so no value is ever reported in a converted form.
func (db *DB) queryEntries(statement string, args ...any) ([]Entry, string, error) {
	rows, err := db.sql.Query(statement, args...)
	if err != nil {
		return nil, "", NormalizeError(err)
	}
	defer rows.Close()
	columns, err := scanColumns(rows)
	if err != nil {
		return nil, "", NormalizeError(err)
	}
	if storedValues := storedValueStatement(columns, rows); storedValues != "" {
		return nil, storedValues, nil
	}
	entries := []Entry{}
	for rows.Next() {
		if err := execHookError("scan entries"); err != nil {
			return nil, "", NormalizeError(err)
		}
		scanner := rowScanner(rows)
		if replacement := scanReplacement(); replacement != nil {
			scanner = replacement
		}
		entry, err := scanRow(columns, scanner)
		if err != nil {
			return nil, "", NormalizeError(err)
		}
		entries = append(entries, *entry)
	}
	if err := rowsResultError(rows); err != nil {
		return nil, "", NormalizeError(err)
	}
	if err := execHookError("entries result"); err != nil {
		return nil, "", NormalizeError(err)
	}
	return entries, "", nil
}

// storedValueStatement returns the statement that reads the same columns without
// the driver's date coercion, or "" when the statement reports stored values
// already. A CASE expression has no declared type, so SQLite hands back the
// stored text for a column declared DATE, DATETIME or TIMESTAMP.
func storedValueStatement(columns []string, rows *sql.Rows) string {
	columnTypes, _ := rows.ColumnTypes()
	wrapped := make([]bool, len(columns))
	coerced := false
	for i, columnType := range columnTypes {
		switch columnType.DatabaseTypeName() {
		case "DATE", "DATETIME", "TIMESTAMP":
			wrapped[i] = true
			coerced = true
		}
	}
	if !coerced {
		return ""
	}
	parts := make([]string, len(columns))
	for i, column := range columns {
		identifier := quoteIdentifier(column)
		if wrapped[i] {
			parts[i] = fmt.Sprintf("CASE WHEN 1 THEN %s ELSE %s END AS %s", identifier, identifier, identifier)
			continue
		}
		parts[i] = fmt.Sprintf("%s AS %s", identifier, identifier)
	}
	return "SELECT " + strings.Join(parts, ", ") + " FROM entries"
}

// quoteIdentifier quotes a column name for SQL, doubling embedded quotes like
// the SQL standard requires.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
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

type rowScanner interface {
	Scan(dest ...any) error
}

type errScanner struct{ err error }

func (s errScanner) Scan(dest ...any) error { return s.err }

var scanReplacementHook func() rowScanner

// scanColumns is a package-level variable so tests can prove how a failure to
// describe the result set is reported. Its default is the real method
// expression, so the guard in seams_test.go can compare code pointers.
var scanColumns = (*sql.Rows).Columns

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

// scanRow reads one row whose columns are named by columns. The typed fields are
// decoded from the stored values for ianchor's own use; raw keeps every stored
// value so the JSON payload can report it unchanged, the way SELECT * does.
func scanRow(columns []string, row rowScanner) (*Entry, error) {
	values := make([]any, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	entry := Entry{order: columns, raw: make(map[string]any, len(columns))}
	for i, column := range columns {
		entry.raw[column] = values[i]
		switch column {
		case "id":
			entry.ID = intValue(values[i])
		case "name":
			entry.Name, _ = textValue(values[i])
		case "original_path":
			if original, ok := textValue(values[i]); ok {
				entry.OriginalPath = &original
			}
		case "type":
			entry.Type, _ = textValue(values[i])
		case "status":
			entry.Status, _ = textValue(values[i])
		case "created_at":
			if createdAt, ok := textValue(values[i]); ok {
				entry.CreatedAt = &createdAt
			}
		case "updated_at":
			if updatedAt, ok := textValue(values[i]); ok {
				entry.UpdatedAt = &updatedAt
			}
		}
	}
	return &entry, nil
}

// intValue decodes a stored integer. A column carrying any other storage class
// is not a usable id, and reports zero instead of guessing.
func intValue(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

// textValue decodes a stored string. SQLite hands text back as a string and
// opaque bytes as []byte; both name the value the Node driver would report, and
// null reports no value at all.
func textValue(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case []byte:
		return string(v), true
	}
	return "", false
}
