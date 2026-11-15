package anchor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/puppetdevz/ianchor/internal/store"
)

// This file covers DELTA-004 / AUTO-007: every destructive filesystem and
// database boundary is failed on purpose, and the resulting state must either
// be the untouched starting state (when the operation reported an error) or the
// fully applied state (when the operation succeeded despite a failed cleanup),
// with any residue confined to the hidden .bak directory.

// world is one isolated scenario: an iCloud config directory, local files and a
// database, all under a single root so the whole state can be snapshotted.
type world struct {
	t         *testing.T
	root      string
	configDir string
	localDir  string
	db        *store.DB
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "icloud", "ianchor")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	db, err := store.Open(configDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	localDir := filepath.Join(root, "home")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	return &world{t: t, root: root, configDir: configDir, localDir: localDir, db: db}
}

func (w *world) write(rel, content string) string {
	w.t.Helper()
	path := filepath.Join(w.localDir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// snapshot renders the whole world as stable text (paths, types, symlink
// targets, content hashes) plus the database rows.
func (w *world) snapshot() string {
	w.t.Helper()
	var lines []string
	err := filepath.Walk(w.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == w.root {
			return nil
		}
		rel, err := filepath.Rel(w.root, path)
		if err != nil {
			return err
		}
		if path == filepath.Join(w.configDir, "ianchor.db") || path == filepath.Join(w.configDir, "ianchor.db-wal") || path == filepath.Join(w.configDir, "ianchor.db-shm") {
			return nil
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("L %s -> %s", rel, strings.ReplaceAll(target, w.root, "<ROOT>")))
		case info.IsDir():
			lines = append(lines, fmt.Sprintf("D %s mode=%04o", rel, info.Mode().Perm()))
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("F %s mode=%04o %s", rel, info.Mode().Perm(), data))
		}
		return nil
	})
	if err != nil {
		w.t.Fatalf("snapshot: %v", err)
	}
	sort.Strings(lines)
	entries, err := w.db.AllEntries()
	if err != nil {
		w.t.Fatalf("rows: %v", err)
	}
	for _, entry := range entries {
		original := "<nil>"
		if entry.OriginalPath != nil {
			original = strings.ReplaceAll(*entry.OriginalPath, w.root, "<ROOT>")
		}
		created, updated := "<nil>", "<nil>"
		if entry.CreatedAt != nil {
			created = *entry.CreatedAt
		}
		if entry.UpdatedAt != nil {
			updated = *entry.UpdatedAt
		}
		lines = append(lines, fmt.Sprintf("R %d %s %s %s %s\t%s\t%s", entry.ID, entry.Name, entry.Type, entry.Status, original, created, updated))
	}
	return strings.Join(lines, "\n")
}

// countOps runs one clean operation with a recording hook and returns the
// operation sequence plus the resulting state. Seeding happens before the hook
// is installed so only the operation under test is measured.
func countOps(t *testing.T, in injection) ([]string, string) {
	t.Helper()
	w := newWorld(t)
	in.seed(w)
	var recorded []string
	forcedVolume := false
	opHook = func(op, path string) error {
		if in.crossVolume && op == "rename" && !forcedVolume {
			forcedVolume = true
			return syscall.EXDEV
		}
		recorded = append(recorded, op)
		return nil
	}
	defer func() { opHook = nil }()
	if err := in.run(w); err != nil && !in.expectedError {
		t.Fatalf("clean run failed: %v", err)
		t.Fatalf("clean run failed: %v", err)
	}
	return recorded, w.snapshot()
}

type injection struct {
	name string
	// crossVolume forces the first source-to-cloud rename into EXDEV.
	// That routing event is distinct from the subsequent failure boundaries.
	expectedError bool
	crossVolume   bool
	directory     bool
	seed          func(w *world)
	run           func(w *world) error
	// verify runs against a world where the operation succeeded (possibly with
	// failed cleanup) and must confirm the intended outcome.
	verify func(t *testing.T, w *world)
	// expectedOps, when non-nil, proves the selected branch recorded every
	// expected destructive boundary rather than a different successful path.
	expectedOps []string
}

func runInjection(t *testing.T, in injection) {
	t.Helper()
	ops, appliedState := countOps(t, in)
	if len(ops) == 0 {
		t.Fatalf("%s: no operations recorded", in.name)
	}
	if in.expectedOps != nil && !equalStrings(ops, in.expectedOps) {
		t.Fatalf("%s operations = %#v, want %#v", in.name, ops, in.expectedOps)
	}
	for index, op := range ops {
		index, op := index, op
		t.Run(fmt.Sprintf("%02d-%s", index+1, strings.ReplaceAll(op, " ", "-")), func(t *testing.T) {
			w := newWorld(t)
			in.seed(w)
			before := w.snapshot()
			count := 0
			injected := errors.New("injected failure")
			forcedVolume := false
			opHook = func(operation, path string) error {
				if in.crossVolume && operation == "rename" && !forcedVolume {
					forcedVolume = true
					return syscall.EXDEV
				}
				count++
				if count == index+1 {
					return injected
				}
				return nil
			}
			defer func() { opHook = nil }()
			err := in.run(w)
			if count < index+1 || !errors.Is(err, injected) {
				t.Fatalf("failure at operation %d (%s) was not propagated: calls=%d error=%v", index+1, op, count, err)
			}
			after := w.snapshot()
			if strings.HasPrefix(in.name, "recover") {
				backupPath := filepath.Join(w.configDir, bakDirName, "zshrc")
				if in.directory {
					backupPath = filepath.Join(backupPath, "child")
				}
				backup := readFile(t, backupPath)
				var committed *committedCleanupError
				if errors.As(err, &committed) || (strings.Contains(in.name, "conflict") && index >= 4) {
					if backup != "cloud\n" {
						t.Fatalf("committed backup corrupted: %q", backup)
					}
				} else if backup != "previous backup\n" {
					t.Fatalf("prior backup not restored: %q", backup)
				}
			}
			var committed *committedCleanupError
			if err != nil && !errors.As(err, &committed) {
				// Recovery refreshes the .bak copy before it starts, so backup
				// files legitimately survive a failed run.
				if diff := diffSnapshots(normalizeRollback(before, in.name), normalizeRollback(after, in.name)); len(diff) > 0 && !(strings.Contains(in.name, "conflict") && index >= 4) {
					t.Fatalf("failure at op %d (%s) did not restore state:\n%s", index+1, op, strings.Join(diff, "\n"))
				}
				return
			}
			// The injected step happened after the commit point (cleanup).
			// It must be reported while preserving the completed operation.
			if diff := diffSnapshots(stripRowTimes(appliedState), stripRowTimes(after)); len(diff) > 0 {
				for _, line := range diff {
					if !allowedResidue(line, w.root, err) {
						t.Fatalf("failed cleanup at op %d (%s) changed unexpected state:\n%s\napplied:\n%s", index+1, op, strings.Join(diff, "\n"), appliedState)
					}
				}
			}
			in.verify(t, w)
		})
	}
}

// allowedResidue only permits additions at or below the actual temporary path
// disclosed in the committed-cleanup error; it may contain valuable data.
func allowedResidue(line, root string, err error) bool {
	if !strings.HasPrefix(line, "+ ") {
		return false
	}
	fields := strings.Fields(strings.TrimPrefix(line, "+ "))
	if len(fields) < 2 || (fields[0] != "F" && fields[0] != "D" && fields[0] != "L") {
		return false
	}
	path := filepath.Join(root, fields[1])
	var committed *committedCleanupError
	if fields[0] == "D" && filepath.Base(path) == bakDirName && errors.As(err, &committed) {
		// A newly created staging root may remain if its child cleanup
		// failed, or if removal of the empty root itself failed.
		for _, retained := range strings.Split(committed.path, ", ") {
			if path == retained || filepath.Dir(retained) == path {
				return true
			}
		}
	}
	for current := path; current != root && current != filepath.Dir(current); current = filepath.Dir(current) {
		if strings.HasPrefix(filepath.Base(current), ".ianchor-tmp-") && strings.Contains(err.Error(), current) {
			return true
		}
	}
	return false
}

func normalizeRollback(snapshot, _ string) string {
	var kept []string
	for _, line := range strings.Split(snapshot, "\n") {
		// Creating the backup directory itself is harmless.
		if strings.HasPrefix(line, "D icloud/ianchor/.bak mode=") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func stripRowTimes(snapshot string) string {
	lines := strings.Split(snapshot, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "R ") {
			lines[i] = strings.SplitN(line, "\t", 2)[0]
		}
	}
	return strings.Join(lines, "\n")
}

func diffSnapshots(want, got string) []string {
	wantLines := map[string]bool{}
	for _, line := range strings.Split(want, "\n") {
		if line != "" {
			wantLines[line] = true
		}
	}
	gotLines := map[string]bool{}
	for _, line := range strings.Split(got, "\n") {
		if line != "" {
			gotLines[line] = true
		}
	}
	var diff []string
	for line := range wantLines {
		if !gotLines[line] {
			diff = append(diff, "- "+line)
		}
	}
	for line := range gotLines {
		if !wantLines[line] {
			diff = append(diff, "+ "+line)
		}
	}
	sort.Strings(diff)
	return diff
}

func TestSnapshotIncludesUserFilesNamedLikeDatabase(t *testing.T) {
	w := newWorld(t)
	path := w.write("config/ianchor.db/ianchor.db-wal", "valuable")
	before := w.snapshot()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	after := w.snapshot()
	if before == after {
		t.Fatal("snapshot ignored user's database-named file")
	}
}

func TestInjectionAddFile(t *testing.T) {
	runInjection(t, injection{
		name: "add file",
		seed: func(w *world) {
			writeFile(w.t, filepath.Join(w.localDir, ".zshrc"), "zsh\n")
		},
		run: func(w *world) error {
			_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: filepath.Join(w.localDir, ".zshrc")})
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			cloud := filepath.Join(w.configDir, "zshrc")
			if got := readFile(t, cloud); got != "zsh\n" {
				t.Fatalf("cloud content = %q", got)
			}
			assertSymlink(t, filepath.Join(w.localDir, ".zshrc"), cloud)
			if entry, _ := w.db.GetEntry("zshrc"); entry == nil || entry.Status != "linked" {
				t.Fatalf("entry = %+v", entry)
			}
		},
	})
}

func TestInjectionAddForceDirectoryReplacement(t *testing.T) {
	runInjection(t, injection{
		name: "add force directory",
		seed: func(w *world) {
			first := filepath.Join(w.localDir, "conf")
			writeFile(t, filepath.Join(first, "old"), "stale")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "conf"}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			writeFile(t, filepath.Join(w.configDir, bakDirName, "existing-backup"), "must survive")
			writeFile(t, filepath.Join(w.root, "second", "conf", "new"), "fresh")
		},
		run: func(w *world) error {
			second := filepath.Join(w.root, "second", "conf")
			_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: second, Name: "conf", Force: true})
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			dest := filepath.Join(w.configDir, "conf")
			if got := readFile(t, filepath.Join(dest, "new")); got != "fresh" {
				t.Fatalf("content = %q", got)
			}
			assertMissing(t, filepath.Join(dest, "old"))
			assertSymlink(t, filepath.Join(w.root, "second", "conf"), dest)
		},
	})
}

func TestInjectionAddConflictPrompt(t *testing.T) {
	runInjection(t, injection{
		name: "add conflict prefix",
		seed: func(w *world) {
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: w.write(".zshrc", "one")}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			w.write("second/.zshrc", "two")
		},
		run: func(w *world) error {
			_, err := Add(w.db, AddOptions{
				ConfigDir:  w.configDir,
				Source:     filepath.Join(w.localDir, "second", ".zshrc"),
				OnConflict: func(string) (string, error) { return "abc", nil },
			})
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			assertSymlink(t, filepath.Join(w.localDir, "second", ".zshrc"), filepath.Join(w.configDir, "abc_zshrc"))
		},
	})
}

func TestInjectionRecover(t *testing.T) {
	runInjection(t, injection{
		name: "recover",
		seed: func(w *world) {
			source := w.write(".zshrc", "cloud\n")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			writeFile(t, filepath.Join(w.configDir, bakDirName, "zshrc"), "previous backup\n")
		},
		run: func(w *world) error {
			_, err := Recover(w.db, w.configDir, "zshrc", false)
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			if got := readFile(t, filepath.Join(w.localDir, ".zshrc")); got != "cloud\n" {
				t.Fatalf("local = %q", got)
			}
			if got := readFile(t, filepath.Join(w.configDir, "zshrc")); got != "cloud\n" {
				t.Fatalf("cloud = %q", got)
			}
		},
	})
}

func TestInjectionRecoverForce(t *testing.T) {
	runInjection(t, injection{
		name: "recover force",
		seed: func(w *world) {
			source := w.write(".zshrc", "cloud\n")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			if err := os.Remove(source); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			writeFile(t, source, "local edit\n")
			writeFile(t, filepath.Join(w.configDir, bakDirName, "zshrc"), "previous backup\n")
		},
		run: func(w *world) error {
			_, err := Recover(w.db, w.configDir, "zshrc", true)
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			if got := readFile(t, filepath.Join(w.localDir, ".zshrc")); got != "cloud\n" {
				t.Fatalf("local = %q", got)
			}
		},
	})
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestInjectionRelinkUnrelatedSymlink(t *testing.T) {
	original := func(w *world) string { return filepath.Join(w.localDir, ".zshrc") }
	runInjection(t, injection{
		name: "relink unrelated symlink",
		seed: func(w *world) {
			source := w.write(".zshrc", "cloud\n")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			if err := os.Remove(source); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			if err := os.Symlink("/unrelated", source); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
		},
		run: func(w *world) error { return Relink(w.db, w.configDir, "zshrc") },
		verify: func(t *testing.T, w *world) {
			t.Helper()
			assertSymlink(t, original(w), filepath.Join(w.configDir, "zshrc"))
		},
		expectedOps: []string{"remove", "symlink", "db.status"},
	})
}

func TestInjectionRelink(t *testing.T) {
	runInjection(t, injection{
		name: "relink",
		seed: func(w *world) {
			source := w.write(".zshrc", "cloud\n")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			if err := os.Remove(source); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
			writeFile(t, source, "local edit\n")
		},
		run: func(w *world) error {
			return Relink(w.db, w.configDir, "zshrc")
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			assertSymlink(t, filepath.Join(w.localDir, ".zshrc"), filepath.Join(w.configDir, "zshrc"))
		},
	})
}

func TestInjectionRemove(t *testing.T) {
	runInjection(t, injection{
		name: "remove",
		seed: func(w *world) {
			source := w.write(".zshrc", "cloud\n")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				w.t.Fatalf("seed: %v", err)
			}
		},
		run: func(w *world) error {
			_, err := Remove(w.db, w.configDir, "zshrc")
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			assertMissing(t, filepath.Join(w.configDir, "zshrc"))
			assertMissing(t, filepath.Join(w.localDir, ".zshrc"))
			if entry, _ := w.db.GetEntry("zshrc"); entry != nil {
				t.Fatalf("record still present")
			}
		},
	})
}

func TestInjectionInitDiscovery(t *testing.T) {
	runInjection(t, injection{
		name: "init discover",
		seed: func(w *world) {
			writeFile(t, filepath.Join(w.configDir, ".zshrc"), "zsh\n")
		},
		run: func(w *world) error {
			_, err := Init(filepath.Join(w.root, "icloud"), "ianchor")
			return err
		},
		verify: func(t *testing.T, w *world) {
			t.Helper()
			entries, err := w.db.AllEntries()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, entry := range entries {
				if entry.Name == "zshrc" && entry.Status == "unlinked" {
					found = true
				}
			}
			if !found {
				t.Fatalf("discovered entries = %+v", entries)
			}
		},
	})
}

// TestCompensationFailureDiagnostics proves that when an undo step also fails,
// the returned error says so and names the step, instead of pretending the
// operation was reversed.
func TestCompensationFailureDiagnostics(t *testing.T) {
	w := newWorld(t)
	source := w.write(".zshrc", "zsh\n")
	count := 0
	opHook = func(op, path string) error {
		count++
		if count >= 3 {
			return errors.New("injected failure")
		}
		return nil
	}
	defer func() { opHook = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source})
	if err == nil {
		t.Fatal("expected failure")
	}
	if !strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("error does not report failed compensation: %v", err)
	}
	if !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("error lost the original cause: %v", err)
	}
}

// TestExdevFallback forces the cross-volume branch and verifies the copied
// result is a real move (content, no shared inode) followed by a symlink.
func TestExdevFallback(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			w := newWorld(t)
			var source string
			if kind == "file" {
				source = w.write(".zshrc", "zsh\n")
			} else {
				source = filepath.Join(w.localDir, ".ssh")
				writeFile(t, filepath.Join(source, "config"), "Host *\n")
			}
			injected := false
			opHook = func(op, path string) error {
				if op == "rename" && !injected {
					injected = true
					return syscall.EXDEV
				}
				return nil
			}
			defer func() { opHook = nil }()
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				t.Fatalf("add: %v", err)
			}
			if !injected {
				t.Fatal("EXDEV was not injected")
			}
			dest := filepath.Join(w.configDir, strings.TrimPrefix(filepath.Base(source), "."))
			assertSymlink(t, source, dest)
			if kind == "file" {
				if got := readFile(t, dest); got != "zsh\n" {
					t.Fatalf("cloud content = %q", got)
				}
				if got := readFile(t, source); got != "zsh\n" {
					t.Fatalf("source content = %q", got)
				}
			} else if got := readFile(t, filepath.Join(dest, "config")); got != "Host *\n" {
				t.Fatalf("cloud content = %q", got)
			}
			entries, err := filepath.Glob(filepath.Join(w.configDir, bakDirName, ".ianchor-tmp-*"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("staging leftovers: %v (glob: %v)", entries, err)
			}
		})
	}
}

// TestExdevCopyFailureIsCompensated covers a failed copy during the cross-volume
// fallback: the source must stay intact and no staging file may survive.
func TestExdevCopyFailureIsCompensated(t *testing.T) {
	// Fault mechanics are isolated from the separately tested metadata guard.
	w := newWorld(t)
	source := w.write(".zshrc", "zsh\n")
	opHook = func(op, path string) error {
		if op == "rename" {
			return syscall.EXDEV
		}
		if op == "copy" {
			return errors.New("copy failed")
		}
		return nil
	}
	defer func() { opHook = nil }()
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err == nil || !strings.Contains(err.Error(), "copy failed") {
		t.Fatalf("expected injected copy failure, got %v", err)
	}
	if got := readFile(t, source); got != "zsh\n" {
		t.Fatalf("source content = %q", got)
	}
	entries, err := filepath.Glob(filepath.Join(w.configDir, bakDirName, ".ianchor-tmp*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging leftovers: %v", entries)
	}
	if entry, _ := w.db.GetEntry("zshrc"); entry != nil {
		t.Fatalf("record created despite failure")
	}
}
