package anchor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/puppetdevz/ianchor/internal/store"
)

func addWorld(t *testing.T, name string) (*world, string, string) {
	t.Helper()
	w := newWorld(t)
	source := w.write(name, "cloud\n")
	return w, source, filepath.Join(w.configDir, name)
}

func forceEXDEV(t *testing.T) {
	t.Helper()
	forced := false
	setHook(t, func(op, _ string) error {
		if op == "rename" && !forced {
			forced = true
			return syscall.EXDEV
		}
		return nil
	})
}

func TestAddEarlyErrors(t *testing.T) {
	t.Run("working directory", func(t *testing.T) {
		w := newWorld(t)
		override(t, &absPath, func(string) (string, error) { return "", errSeam })
		if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: "relative"}); err == nil {
			t.Fatal("expected absolute path failure")
		}
	})
	t.Run("source eval", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		override(t, &evalSymlinksPath, func(string) (string, error) { return "", errSeam })
		mustContain(t, addErr(t, w, source, false), errSeam.Error())
	})
	t.Run("config eval", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		realEval := evalSymlinksPath
		calls := 0
		override(t, &evalSymlinksPath, func(path string) (string, error) {
			calls++
			if calls == 2 {
				return "", errSeam
			}
			return realEval(path)
		})
		mustContain(t, addErr(t, w, source, false), errSeam.Error())
	})
	t.Run("identity overlap", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		realStat := statPath
		calls := 0
		override(t, &statPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realStat(path)
		})
		mustContain(t, addErr(t, w, source, false), errSeam.Error())
	})
	t.Run("conflict hook", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		writeFile(t, filepath.Join(w.configDir, "item"), "existing")
		_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, OnConflict: func(string) (string, error) {
			return "", errSeam
		}})
		mustContain(t, err, errSeam.Error())
	})
	t.Run("conflict prefix", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		writeFile(t, filepath.Join(w.configDir, "item"), "existing")
		_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, OnConflict: func(string) (string, error) {
			return "bad/prefix", nil
		}})
		if err == nil {
			t.Fatal("expected name validation failure")
		}
	})
	t.Run("entry lookup", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		if err := w.db.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err == nil {
			t.Fatal("expected database failure")
		}
	})
}

func TestAddForceErrors(t *testing.T) {
	t.Run("destination lstat", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == dest {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, addErr(t, w, source, true), errSeam.Error())
	})
	t.Run("all entries", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		override(t, &dbAllEntries, func(*store.DB) ([]store.Entry, error) { return nil, errSeam })
		mustContain(t, addErr(t, w, source, true), errSeam.Error())
	})
	t.Run("unrelated legacy tracked name", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		original := source
		// A legacy empty name resolves to the config directory itself, which
		// is never the destination, so it no longer blocks the takeover.
		if err := w.db.AddEntry("", &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Force: true}); err != nil {
			t.Fatalf("Add() error = %v, want success past an unrelated legacy row", err)
		}
	})
	t.Run("displaced lstat", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == dest {
				calls++
				if calls == 2 {
					return nil, errSeam
				}
			}
			return realLstat(path)
		})
		mustContain(t, addErr(t, w, source, true), errSeam.Error())
	})
	t.Run("displaced staging", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		realStaging := stagingPath
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			if strings.Contains(ref, "-replaced") {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, addErr(t, w, source, true), errSeam.Error())
	})
	t.Run("other entry lstat", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		other := filepath.Join(w.configDir, "other")
		writeFile(t, other, "other")
		original := source
		if err := w.db.AddEntry("other", &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == other {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, addErr(t, w, source, true), errSeam.Error())
	})
}

func TestAddStagingErrors(t *testing.T) {
	t.Run("backup root", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		override(t, &rootMkdir, func(*os.Root, string, os.FileMode) error { return errSeam })
		mustContain(t, mustFailSafely(t, w, source, false), errSeam.Error())
	})
	t.Run("staging", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		override(t, &stagingPath, func(string, string) (string, error) { return "", errSeam })
		mustContain(t, mustFailSafely(t, w, source, false), errSeam.Error())
	})
	t.Run("source identity", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == source {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, mustFailSafely(t, w, source, false), errSeam.Error())
	})
	t.Run("install staging", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(source, "child"), "cloud\n")
		forceEXDEV(t)
		realStaging := stagingPath
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			if strings.Contains(ref, "-install") {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, mustFailSafely(t, w, source, false), errSeam.Error())
	})
	t.Run("created link", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			info, err := realLstat(path)
			if err == nil && path == source && info.Mode()&os.ModeSymlink != 0 {
				return fakeInfoFor(t, 999), nil
			}
			return info, err
		})
		mustContain(t, mustFailSafely(t, w, source, false), "cannot verify created symlink")
	})
}

func TestAddCrossVolumeErrors(t *testing.T) {
	t.Run("source staging", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		forceEXDEV(t)
		realStaging := stagingPath
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			if strings.Contains(ref, "-source") {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, mustFailSafely(t, w, source, false), errSeam.Error())
	})
	t.Run("rollback refuses overwrite", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		forceEXDEV(t)
		realCopy := copyHash
		override(t, &copyHash, func(dst io.Writer, src io.Reader) (int64, error) {
			if file, ok := src.(*os.File); ok && strings.Contains(file.Name(), "-source") {
				writeFile(t, source, "new entry\n")
				return 0, errSeam
			}
			return realCopy(dst, src)
		})
		mustContain(t, mustFailSafely(t, w, source, false), "refusing to overwrite new entry")
	})
	t.Run("source stage lstat", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		forceEXDEV(t)
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if strings.Contains(path, "-source") {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, mustFailSafely(t, w, source, false), errSeam.Error())
	})
	t.Run("source replaced", func(t *testing.T) {
		w, source, _ := addWorld(t, "item")
		forceEXDEV(t)
		realLstat := lstatPath
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if strings.Contains(path, "-source") {
				return fakeInfoFor(t, 12345), nil
			}
			return realLstat(path)
		})
		mustContain(t, mustFailSafely(t, w, source, false), "source was replaced")
	})
}

func TestAddForceTakeoverErrors(t *testing.T) {
	prepare := func(t *testing.T) (*world, string, string, string) {
		t.Helper()
		w := newWorld(t)
		first := w.write("first", "cloud\n")
		if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: first, Name: "item"}); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(w.configDir, "item")
		if err := os.Remove(first); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(w.localDir, "legacy")
		if err := os.Symlink(dest, link); err != nil {
			t.Fatal(err)
		}
		if err := w.db.UpsertEntry("item", &link, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		source := w.write("second", "second\n")
		return w, source, link, dest
	}
	t.Run("previous link lstat", func(t *testing.T) {
		w, source, link, _ := prepare(t)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			if path == link {
				calls++
				if calls == 2 {
					return nil, errSeam
				}
			}
			return realLstat(path)
		})
		mustContain(t, mustFailSafelyNamed(t, w, source, "item"), errSeam.Error())
	})
	t.Run("link staging", func(t *testing.T) {
		w, source, _, _ := prepare(t)
		realStaging := stagingPath
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			if strings.Contains(ref, "-link") {
				return "", errSeam
			}
			return realStaging(dir, ref)
		})
		mustContain(t, mustFailSafelyNamed(t, w, source, "item"), errSeam.Error())
	})
	t.Run("link cross volume staging", func(t *testing.T) {
		w, source, _, _ := prepare(t)
		realStaging := stagingPath
		calls := 0
		override(t, &stagingPath, func(dir, ref string) (string, error) {
			if strings.Contains(ref, "-link") {
				calls++
				if calls == 2 {
					return "", errSeam
				}
			}
			return realStaging(dir, ref)
		})
		setHook(t, func(op, path string) error {
			if op == "rename" && strings.Contains(path, "legacy") {
				return syscall.EXDEV
			}
			return nil
		})
		mustContain(t, mustFailSafelyNamed(t, w, source, "item"), errSeam.Error())
	})
	t.Run("link source rename", func(t *testing.T) {
		w, source, link, _ := prepare(t)
		renames := 0
		setHook(t, func(op, path string) error {
			if op == "rename" && path == link {
				renames++
				if renames == 1 {
					return syscall.EXDEV
				}
				return errSeam
			}
			return nil
		})
		mustContain(t, mustFailSafelyNamed(t, w, source, "item"), errSeam.Error())
	})
}

// mustFailSafely runs Add and requires a reported error plus a safe outcome:
// no tracked file content may disappear, and any residue outside the hidden
// backup/staging names must be announced by a rollback failure in the error.
// A seam that also breaks compensation would make an exact-snapshot assertion
// meaningless, so the invariant is data preservation plus disclosure.
func mustFailSafely(t *testing.T, w *world, source string, force bool) error {
	t.Helper()
	before := w.snapshot()
	err := addErr(t, w, source, force)
	checkSafeFailure(t, before, w.snapshot(), err)
	return err
}

func mustFailSafelyNamed(t *testing.T, w *world, source, name string) error {
	t.Helper()
	before := w.snapshot()
	err := addErrNamed(t, w, source, name)
	checkSafeFailure(t, before, w.snapshot(), err)
	return err
}

func checkSafeFailure(t *testing.T, before, after string, err error) {
	t.Helper()
	// Compare whole snapshot lines, not just file contents: a file that came
	// back with the same bytes but a different mode, or a file that moved, is
	// still a lost original.
	remaining := map[string]int{}
	for _, line := range snapshotLines(after) {
		if strings.HasPrefix(line, "F ") {
			remaining[line]++
		}
	}
	afterLines := snapshotLines(after)
	for _, line := range snapshotLines(before) {
		if !strings.HasPrefix(line, "F ") {
			continue
		}
		if remaining[line] == 0 {
			// The original may be parked at a staging path, but only if its
			// mode and bytes survived and the error tells the operator where
			// it went.
			if !displacedAndNamed(line, afterLines, err) {
				t.Fatalf("failed add lost original file %q:\n--- before ---\n%s\n--- after ---\n%s", line, before, after)
			}
			continue
		}
		remaining[line]--
	}
	// A residue is allowed only when the error names its path exactly. Saying
	// "rollback failed" is not a licence to leave unexplained files behind:
	// the operator needs to know where the leftover is.
	for _, line := range diffSnapshot(before, after) {
		if namesResidue(line, err) {
			continue
		}
		t.Fatalf("failed operation left unreported residue %q: %v", line, err)
	}
}

// displacedAndNamed reports whether the original described by want survives as
// one of the after lines with the same mode and bytes, and the error names that
// line's path.
func displacedAndNamed(want string, afterLines []string, err error) bool {
	tail := "mode=" + strings.SplitN(want, "mode=", 2)[1]
	for _, line := range afterLines {
		if !strings.HasPrefix(line, "F ") || !strings.HasSuffix(line, tail) {
			continue
		}
		if namesResidue(line, err) {
			return true
		}
	}
	return false
}

// namesResidue reports whether the error text identifies this snapshot line's
// path, so a partial recovery points the operator at the real location.
func namesResidue(line string, err error) bool {
	if err == nil {
		return false
	}
	return namesPath(err.Error(), snapshotPath(line))
}

// snapshotPath pulls the path out of a snapshot line. Paths may contain
// spaces, so the fields cannot be split: the record shape's own separators
// bound the path instead.
func snapshotPath(line string) string {
	if len(line) < 3 || line[1] != ' ' {
		return ""
	}
	rest := line[2:]
	if i := strings.Index(rest, " mode="); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.Index(rest, " -> "); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// namesPath reports whether text mentions path as a whole path. A prefix hit
// such as "local/item" inside "local/item-undo" does not count, because the
// operator would look in the wrong place.
func namesPath(text, path string) bool {
	if path == "" {
		return false
	}
	for start := 0; start <= len(text); {
		i := strings.Index(text[start:], path)
		if i < 0 {
			return false
		}
		i += start
		end := i + len(path)
		// Snapshot paths are relative to the world root while messages carry
		// absolute paths, so a leading separator is a real boundary; a
		// trailing one is not, because it would extend the path.
		if !pathLeadByte(text, i-1) && !pathByte(text, end) {
			return true
		}
		start = i + 1
	}
	return false
}

// pathLeadByte is pathByte minus the separator: a match may start right after
// a slash, since messages name absolute paths.
func pathLeadByte(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return false
	}
	if text[index] == '/' {
		return false
	}
	return pathByte(text, index)
}

// pathByte reports whether the byte at index could continue a path, which is
// what makes a substring hit ambiguous. Quotes, spaces and punctuation end a
// path in the messages ianchor produces.
func pathByte(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return false
	}
	c := text[index]
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c >= 0x80:
		return true
	}
	return c == '-' || c == '_' || c == '.' || c == '/' || c == '+' || c == '~' || c == '@' || c == '\\'
}

// snapshotLines splits a joined snapshot and drops empty records: an empty
// snapshot must not contribute a phantom residue line.
func snapshotLines(snapshot string) []string {
	var lines []string
	for _, line := range strings.Split(snapshot, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func diffSnapshot(before, after string) []string {
	counts := map[string]int{}
	for _, line := range snapshotLines(before) {
		counts["-"+line]++
	}
	var diff []string
	for _, line := range snapshotLines(after) {
		if counts["-"+line] > 0 {
			counts["-"+line]--
			continue
		}
		diff = append(diff, line)
	}
	for key, n := range counts {
		for i := 0; i < n; i++ {
			diff = append(diff, key[1:])
		}
	}
	return diff
}

func addErrNamed(t *testing.T, w *world, source, name string) error {
	t.Helper()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Name: name, Force: true})
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

func addErr(t *testing.T, w *world, source string, force bool) error {
	t.Helper()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source, Force: force})
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

// The safe-failure invariant is the substitute for a human checking every
// injected failure by hand, so the invariant itself is tested: a file that came
// back with different permissions is damage, and a staged original only counts
// as preserved when the error names where it went.
func TestSafeFailureInvariantDetectsModeOnlyDamage(t *testing.T) {
	before := "D home mode=0755\nF home/item mode=0644 cloud\n"
	modeOnly := "D home mode=0755\nF home/item mode=0600 cloud\n"
	// A permission change shows up as one removed and one added snapshot line,
	// so the diff must be non-empty even though the bytes are unchanged.
	if diff := diffSnapshot(before, modeOnly); len(diff) != 2 {
		t.Fatalf("mode-only damage diff = %v, want the changed line", diff)
	}
	if displacedAndNamed("F home/item mode=0644 cloud", snapshotLines(modeOnly), errors.New("moved")) {
		t.Fatal("a file with the wrong mode must not count as preserved")
	}

	staged := "D home mode=0755\nF home/.ianchor-tmp-x-item-stop mode=0644 cloud\n"
	if displacedAndNamed("F home/item mode=0644 cloud", snapshotLines(staged), errors.New("staging failed")) {
		t.Fatal("a staged original must not count as preserved when the error does not name it")
	}
	if !displacedAndNamed("F home/item mode=0644 cloud", snapshotLines(staged), errors.New("staging failed at /home/.ianchor-tmp-x-item-stop")) {
		t.Fatal("a staged original with matching mode and bytes must count as preserved when named")
	}

	if namesResidue("F /home/.ianchor-tmp-x-item-stop mode=0644 cloud", errors.New("left at /home/.ianchor-tmp-x-item-stop")) != true {
		t.Fatal("namesResidue must match the exact path")
	}
	if namesResidue("F /home/item mode=0644 cloud", errors.New("left at /home/other")) {
		t.Fatal("namesResidue must not match an unrelated path")
	}
}

// TestNamesResidueRequiresTheExactPath pins the boundary rule of the safe
// failure invariant: a residue is tolerated only when the error names that
// path, not a longer path that merely starts with it. Without this the
// substring match a truncated field would allow goes unnoticed.
func TestNamesResidueRequiresTheExactPath(t *testing.T) {
	line := "F world/keep mode=-rw-r--r--"
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"exact absolute", errors.New(`rollback failed: /tmp/world/keep`), true},
		{"quoted", fmt.Errorf("left behind %q", "/tmp/world/keep"), true},
		{"longer path", errors.New("rollback failed: /tmp/world/keeper"), false},
		{"child path", errors.New("rollback failed: /tmp/world/keep/nested"), false},
		{"other file", errors.New("rollback failed: /tmp/world/other"), false},
		{"nil error", nil, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := namesResidue(line, testCase.err); got != testCase.want {
				t.Fatalf("namesResidue(%q, %v) = %v, want %v", line, testCase.err, got, testCase.want)
			}
		})
	}
	if namesResidue("mode=0644", errors.New("/tmp/x")) {
		t.Fatal("a line without a path must never match")
	}
}
