package anchor

import (
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
	t.Run("reserved tracked name", func(t *testing.T) {
		w, source, dest := addWorld(t, "item")
		writeFile(t, dest, "existing")
		original := source
		if err := w.db.AddEntry("", &original, "file", "linked"); err != nil {
			t.Fatal(err)
		}
		if err := addErr(t, w, source, true); err == nil {
			t.Fatal("expected validation failure")
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
	remaining := map[string]int{}
	for _, line := range strings.Split(after, "\n") {
		if strings.HasPrefix(line, "F ") {
			remaining[line[strings.LastIndex(line, " ")+1:]]++
		}
	}
	for _, line := range strings.Split(before, "\n") {
		if !strings.HasPrefix(line, "F ") {
			continue
		}
		content := line[strings.LastIndex(line, " ")+1:]
		if remaining[content] == 0 {
			t.Fatalf("failed add lost file content %q:\n--- before ---\n%s\n--- after ---\n%s", content, before, after)
		}
		remaining[content]--
	}
	// A residue is allowed only when it is either a backup/disposable staging
	// path, or the error names it. Reporting "rollback failed" is not a licence
	// to leave unexplained files behind: the operator needs the exact location.
	for _, line := range diffSnapshot(before, after) {
		if strings.Contains(line, "/.bak") || strings.Contains(line, ".ianchor-tmp-") {
			continue
		}
		if namesResidue(line, err) {
			continue
		}
		t.Fatalf("failed operation left unreported residue %q: %v", line, err)
	}
}

// namesResidue reports whether the error text identifies this snapshot line's
// path, so a partial recovery points the operator at the real location.
func namesResidue(line string, err error) bool {
	if err == nil {
		return false
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return false
	}
	path := fields[1]
	if _, _, ok := strings.Cut(path, " -> "); ok {
		path, _, _ = strings.Cut(path, " -> ")
	}
	return strings.Contains(err.Error(), path)
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
