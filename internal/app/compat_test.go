package app_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/puppetdevz/ianchor/internal/app"
	"github.com/puppetdevz/ianchor/internal/cli"
	_ "modernc.org/sqlite"
)

// The fixtures in testdata/compat.json were recorded from the Node.js
// implementation (v1.1.1) before it was removed. They pin the user-visible
// behaviour that the Go rewrite must reproduce byte for byte: argument
// parsing, help and error text, exit codes, prompts, table/JSON output, and
// the resulting file tree and database rows.

type expectation struct {
	Code   int    `json:"code"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type compatCase struct {
	Args   []string    `json:"args"`
	Stdin  string      `json:"stdin"`
	Expect expectation `json:"expect"`
}

type treeEntry struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Target string `json:"target"`
	Size   int64  `json:"size"`
}

type dbRow struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	OriginalPath *string `json:"original_path"`
	Type         string  `json:"type"`
	Status       string  `json:"status"`
	CreatedAt    *string `json:"created_at"`
	UpdatedAt    *string `json:"updated_at"`
}

type snapshot struct {
	Tree []treeEntry `json:"tree"`
	Rows []dbRow     `json:"rows"`
}

type goldenFile struct {
	Cases []compatCase `json:"cases"`
	Steps []compatCase `json:"steps"`
	Final struct {
		InitFixtures bool     `json:"initFixtures"`
		Final        snapshot `json:"final"`
	} `json:"fixtures"`
}

var tsPattern = regexp.MustCompile(`"(created_at|updated_at)": "([^"]*)"`)
var homeStarted sync.Map // isolated HOME -> UTC start instant

func timestampWithin(value string, start, end time.Time) bool {
	if !validSQLiteTimestamp(value) {
		return false
	}
	parsed, _ := time.Parse("2006-01-02 15:04:05", value)
	// SQLite records whole seconds; account only for that truncation.
	return !parsed.Before(start.UTC().Truncate(time.Second)) && !parsed.After(end.UTC().Truncate(time.Second))
}

func TestTimestampRangeRejectsValidButWrongDates(t *testing.T) {
	start := time.Date(2026, 11, 14, 21, 0, 1, 500000000, time.UTC)
	end := start.Add(3 * time.Second)
	if !timestampWithin("2026-11-14 21:00:01", start, end) || timestampWithin("2000-01-01 00:00:00", start, end) || timestampWithin("2099-01-01 00:00:00", start, end) {
		t.Fatal("timestamp range validation failed")
	}
}

func validSQLiteTimestamp(value string) bool {
	parsed, err := time.Parse("2006-01-02 15:04:05", value)
	return err == nil && parsed.Format("2006-01-02 15:04:05") == value
}

func TestTimestampNormalisationRejectsInvalidValues(t *testing.T) {
	valid := `"created_at": "2026-11-14 21:00:00"`
	invalid := []string{`"created_at": "tomorrow"`, `"updated_at": "2026-02-30 00:00:00"`, `"updated_at": "2026-01-01T00:00:00Z"`}
	if got := normalise(valid, "home"); got != `"created_at": "<TS>"` {
		t.Fatalf("valid timestamp: %s", got)
	}
	for _, input := range invalid {
		if got := normalise(input, "home"); got != input {
			t.Fatalf("invalid timestamp was hidden: %s", got)
		}
	}
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "compat.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden goldenFile
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	return golden
}

// newHome creates an exclusively owned, fixed-length HOME under /tmp. The
// recorded table widths depend on the path length; random names of equal length
// retain the golden contract without deleting a pre-existing user's directory.
func newHome(t *testing.T, withFixtures bool) string {
	t.Helper()
	var home string
	created := false
	for attempts := 0; attempts < 100; attempts++ {
		var suffix [3]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		home = "/tmp/ianchor-" + hex.EncodeToString(suffix[:])
		if err := os.Mkdir(home, 0o700); err != nil {
			if os.IsExist(err) {
				continue
			}
			t.Fatalf("mkdir home: %v", err)
		}
		created = true
		break
	}
	if !created {
		t.Fatal("could not allocate isolated HOME")
	}
	homeStarted.Store(home, time.Now())
	t.Cleanup(func() { homeStarted.Delete(home); os.RemoveAll(home) })
	for _, dir := range []string{
		filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs"),
		filepath.Join(home, ".config"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if withFixtures {
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o755); err != nil {
			t.Fatalf("mkdir .ssh: %v", err)
		}
		files := map[string]string{
			".zshrc":      "zsh\n",
			".ssh/config": "Host *\n",
			".DS_Store":   "junk",
			"logo.png":    "png",
		}
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
	return home
}

func useHome(t *testing.T, home string) {
	t.Helper()
	for _, kv := range [][2]string{
		{"HOME", home},
		{"PWD", home},
		{"TERM", "xterm-256color"},
		{"LANG", "C"},
		{"LC_ALL", "C"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	// The recorded Node baseline used a color-capable terminal outside CI.
	for _, name := range []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR_FORCE", "CLICOLOR", "CI", "TEAMCITY_VERSION", "TF_BUILD", "AGENT_NAME"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	// The baseline recorded resolved paths (macOS /tmp is a symlink to
	// /private/tmp), so work from the resolved directory and keep $HOME as
	// the unresolved spelling to normalise against.
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	t.Setenv("PWD", resolved)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(resolved); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
}

func normalise(s, home string) string {
	s = strings.ReplaceAll(s, home, "<HOME>")
	return tsPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := tsPattern.FindStringSubmatch(match)
		if !validSQLiteTimestamp(parts[2]) {
			return match
		}
		return `"` + parts[1] + `": "<TS>"`
	})
}

// run executes the CLI with the version recorded in the Node.js baseline.
func run(t *testing.T, home string, c compatCase) expectation {
	t.Helper()
	var stdout, stderr bytes.Buffer
	useHome(t, home)
	code := cli.Run(app.NewWithCoordinator(testCoordinator), "1.1.1", c.Args, strings.NewReader(c.Stdin), &stdout, &stderr)
	started, ok := homeStarted.Load(home)
	if !ok {
		t.Fatal("missing HOME start time")
	}
	for _, output := range []string{stdout.String(), stderr.String()} {
		for _, match := range tsPattern.FindAllStringSubmatch(output, -1) {
			if !timestampWithin(match[2], started.(time.Time), time.Now()) {
				t.Errorf("%s timestamp outside test interval: %q", match[1], match[2])
			}
		}
	}
	return expectation{
		Code:   code,
		Stdout: normalise(stdout.String(), home),
		Stderr: normalise(stderr.String(), home),
	}
}

// acceptedDeltas lists the only text divergences allowed against the baseline
// (DELTA-005: low-level driver errors are matched by cause, path, channel and
// exit code, not copied verbatim). Each entry maps the recorded Node message to
// the Go message that reports the same cause.
var acceptedDeltas = map[string]string{
	"Error: Cannot open database because the directory does not exist\n": "Error: unable to open database file\n",
}

func compare(t *testing.T, label string, got, want expectation) {
	t.Helper()
	if replacement, ok := acceptedDeltas[want.Stderr]; ok && got.Stderr == replacement && got.Code == want.Code {
		want.Stderr = replacement
	}
	if got.Code != want.Code {
		t.Errorf("%s: exit code = %d, want %d", label, got.Code, want.Code)
	}
	if got.Stdout != want.Stdout {
		t.Errorf("%s: stdout mismatch\n--- got ---\n%s\n--- want ---\n%s", label, got.Stdout, want.Stdout)
	}
	if got.Stderr != want.Stderr {
		t.Errorf("%s: stderr mismatch\n--- got ---\n%s\n--- want ---\n%s", label, got.Stderr, want.Stderr)
	}
}

func TestCompatCases(t *testing.T) {
	golden := loadGolden(t)
	if len(golden.Cases) == 0 {
		t.Fatal("no compat cases in golden file")
	}
	for _, c := range golden.Cases {
		name := "ianchor"
		if len(c.Args) > 0 {
			name += " " + strings.Join(c.Args, " ")
		}
		t.Run(name, func(t *testing.T) {
			home := newHome(t, false)
			compare(t, name, run(t, home, c), c.Expect)
		})
	}
}

func TestCompatWorkflow(t *testing.T) {
	golden := loadGolden(t)
	if len(golden.Steps) == 0 {
		t.Fatal("no workflow steps in golden file")
	}
	home := newHome(t, golden.Final.InitFixtures)
	for i, step := range golden.Steps {
		label := "step " + itoa(i) + ": ianchor " + strings.Join(step.Args, " ")
		compare(t, label, run(t, home, step), step.Expect)
	}
	assertSnapshot(t, home, golden.Final.Final)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func assertSnapshot(t *testing.T, home string, want snapshot) {
	t.Helper()
	started, ok := homeStarted.Load(home)
	if !ok {
		t.Fatal("missing HOME start time")
	}
	got, err := takeSnapshot(home, started.(time.Time), time.Now())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	gotTree, _ := json.MarshalIndent(got.Tree, "", " ")
	wantTree, _ := json.MarshalIndent(want.Tree, "", " ")
	if string(gotTree) != string(wantTree) {
		t.Errorf("file tree mismatch\n--- got ---\n%s\n--- want ---\n%s", gotTree, wantTree)
	}
	gotRows, _ := json.MarshalIndent(got.Rows, "", " ")
	wantRows, _ := json.MarshalIndent(want.Rows, "", " ")
	if string(gotRows) != string(wantRows) {
		t.Errorf("database rows mismatch\n--- got ---\n%s\n--- want ---\n%s", gotRows, wantRows)
	}
}

func takeSnapshot(home string, start, end time.Time) (snapshot, error) {
	snap := snapshot{Tree: []treeEntry{}, Rows: []dbRow{}}
	err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == home {
			return nil
		}
		base := filepath.Base(path)
		if base == "ianchor.db-wal" || base == "ianchor.db-shm" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			target = strings.ReplaceAll(target, home, "<HOME>")
			snap.Tree = append(snap.Tree, treeEntry{Type: "L", Path: rel, Target: target})
		case info.IsDir():
			snap.Tree = append(snap.Tree, treeEntry{Type: "D", Path: rel})
		default:
			entry := treeEntry{Type: "F", Path: rel, Size: info.Size()}
			if strings.HasSuffix(rel, ".db") {
				entry.SHA256, entry.Size = "<DB>", -1
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(data)
				entry.SHA256 = hex.EncodeToString(sum[:])
			}
			snap.Tree = append(snap.Tree, entry)
		}
		return nil
	})
	if err != nil {
		return snap, err
	}
	sort.Slice(snap.Tree, func(i, j int) bool { return snap.Tree[i].Path < snap.Tree[j].Path })

	dbPath := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "ianchor", "ianchor.db")
	if _, err := os.Stat(dbPath); err != nil {
		return snap, nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return snap, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, name, original_path, type, status, created_at, updated_at FROM entries ORDER BY name`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			row          dbRow
			originalPath sql.NullString
			createdAt    sql.NullString
			updatedAt    sql.NullString
		)
		if err := rows.Scan(&row.ID, &row.Name, &originalPath, &row.Type, &row.Status, &createdAt, &updatedAt); err != nil {
			return snap, err
		}
		if originalPath.Valid {
			value := strings.ReplaceAll(originalPath.String, home, "<HOME>")
			row.OriginalPath = &value
		}
		if createdAt.Valid {
			if !timestampWithin(createdAt.String, start, end) {
				return snap, fmt.Errorf("invalid created_at for %q: %q", row.Name, createdAt.String)
			}
			value := "<TS>"
			row.CreatedAt = &value
		}
		if updatedAt.Valid {
			if !timestampWithin(updatedAt.String, start, end) {
				return snap, fmt.Errorf("invalid updated_at for %q: %q", row.Name, updatedAt.String)
			}
			value := "<TS>"
			row.UpdatedAt = &value
		}
		if createdAt.Valid && updatedAt.Valid && updatedAt.String < createdAt.String {
			return snap, fmt.Errorf("updated_at precedes created_at for %q", row.Name)
		}
		snap.Rows = append(snap.Rows, row)
	}
	return snap, rows.Err()
}
