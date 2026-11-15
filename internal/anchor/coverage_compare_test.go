package anchor

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

func twoRoots(t *testing.T) (*os.Root, *os.Root, string, string) {
	t.Helper()
	left := t.TempDir()
	right := t.TempDir()
	a, err := os.OpenRoot(left)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := os.OpenRoot(right)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return a, b, left, right
}

func writeAt(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySnapshotErrors(t *testing.T) {
	setup := func(t *testing.T, kind string) (source, snapshot string) {
		t.Helper()
		dir := t.TempDir()
		source = filepath.Join(dir, "source")
		snapshot = filepath.Join(dir, "snapshot")
		switch kind {
		case "symlink":
			if err := os.Symlink("target", source); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("target", snapshot); err != nil {
				t.Fatal(err)
			}
		case "dir":
			if err := os.Mkdir(source, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(snapshot, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "child"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(snapshot, "child"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		default:
			writeAt(t, dir, "source", "data", 0o644)
			writeAt(t, dir, "snapshot", "data", 0o644)
		}
		return source, snapshot
	}
	t.Run("source readlink", func(t *testing.T) {
		source, snapshot := setup(t, "symlink")
		override(t, &readlinkPath, func(string) (string, error) { return "", errSeam })
		if err := verifySnapshot(source, snapshot); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("snapshot readlink", func(t *testing.T) {
		source, snapshot := setup(t, "symlink")
		realReadlink := readlinkPath
		calls := 0
		override(t, &readlinkPath, func(path string) (string, error) {
			calls++
			if calls == 2 {
				return "", errSeam
			}
			return realReadlink(path)
		})
		if err := verifySnapshot(source, snapshot); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("source dir listing", func(t *testing.T) {
		source, snapshot := setup(t, "dir")
		override(t, &readDirPath, func(string) ([]os.DirEntry, error) { return nil, errSeam })
		if err := verifySnapshot(source, snapshot); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("snapshot dir listing", func(t *testing.T) {
		source, snapshot := setup(t, "dir")
		realReadDir := readDirPath
		calls := 0
		override(t, &readDirPath, func(path string) ([]os.DirEntry, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realReadDir(path)
		})
		if err := verifySnapshot(source, snapshot); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("snapshot hash open", func(t *testing.T) {
		source, snapshot := setup(t, "file")
		realOpen := openPath
		override(t, &openPath, func(path string) (*os.File, error) {
			if path == snapshot {
				return nil, errSeam
			}
			return realOpen(path)
		})
		if err := verifySnapshot(source, snapshot); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("recheck source", func(t *testing.T) {
		source, snapshot := setup(t, "file")
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 3 {
				return nil, errSeam
			}
			return realLstat(path)
		})
		if err := verifySnapshot(source, snapshot); err == nil {
			t.Fatal("expected failure")
		}
	})
	t.Run("recheck mismatch", func(t *testing.T) {
		source, snapshot := setup(t, "file")
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 3 {
				return fakeInfoFor(t, 77), nil
			}
			return realLstat(path)
		})
		mustContain(t, verifySnapshot(source, snapshot), "source changed")
	})
}

func TestVerifyPublishedSnapshotErrors(t *testing.T) {
	dir := func(t *testing.T) (raw, published string) {
		t.Helper()
		base := t.TempDir()
		return filepath.Join(base, "raw"), filepath.Join(base, "published")
	}
	t.Run("published lstat", func(t *testing.T) {
		raw, published := dir(t)
		writeAt(t, filepath.Dir(raw), "raw", "x", 0o644)
		realLstat := lstatPath
		calls := 0
		override(t, &lstatPath, func(path string) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realLstat(path)
		})
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
	t.Run("type mismatch", func(t *testing.T) {
		raw, published := dir(t)
		writeAt(t, filepath.Dir(raw), "raw", "x", 0o644)
		if err := os.Mkdir(published, 0o755); err != nil {
			t.Fatal(err)
		}
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "published copy differs")
	})
	t.Run("raw readlink", func(t *testing.T) {
		raw, published := dir(t)
		if err := os.Symlink("t", raw); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("t", published); err != nil {
			t.Fatal(err)
		}
		override(t, &readlinkPath, func(string) (string, error) { return "", errSeam })
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
	t.Run("published readlink", func(t *testing.T) {
		raw, published := dir(t)
		if err := os.Symlink("t", raw); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("t", published); err != nil {
			t.Fatal(err)
		}
		realReadlink := readlinkPath
		calls := 0
		override(t, &readlinkPath, func(path string) (string, error) {
			calls++
			if calls == 2 {
				return "", errSeam
			}
			return realReadlink(path)
		})
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
	t.Run("raw listing", func(t *testing.T) {
		raw, published := dir(t)
		if err := os.Mkdir(raw, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(published, 0o755); err != nil {
			t.Fatal(err)
		}
		override(t, &readDirPath, func(string) ([]os.DirEntry, error) { return nil, errSeam })
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
	t.Run("published listing", func(t *testing.T) {
		raw, published := dir(t)
		if err := os.Mkdir(raw, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(published, 0o755); err != nil {
			t.Fatal(err)
		}
		realReadDir := readDirPath
		calls := 0
		override(t, &readDirPath, func(path string) ([]os.DirEntry, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realReadDir(path)
		})
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
	t.Run("entry count", func(t *testing.T) {
		raw, published := dir(t)
		writeAt(t, raw, "a", "x", 0o644)
		writeAt(t, published, "a", "x", 0o644)
		writeAt(t, published, "b", "x", 0o644)
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "published copy differs")
	})
	t.Run("entry name", func(t *testing.T) {
		raw, published := dir(t)
		writeAt(t, raw, "a", "x", 0o644)
		writeAt(t, published, "b", "x", 0o644)
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "published copy differs")
	})
	t.Run("raw hash open", func(t *testing.T) {
		raw, published := dir(t)
		writeAt(t, filepath.Dir(raw), "raw", "x", 0o644)
		writeAt(t, filepath.Dir(raw), "published", "x", 0o644)
		realOpen := openPath
		override(t, &openPath, func(path string) (*os.File, error) {
			if path == raw {
				return nil, errSeam
			}
			return realOpen(path)
		})
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
	t.Run("published hash open", func(t *testing.T) {
		raw, published := dir(t)
		writeAt(t, filepath.Dir(raw), "raw", "x", 0o644)
		writeAt(t, filepath.Dir(raw), "published", "x", 0o644)
		realOpen := openPath
		override(t, &openPath, func(path string) (*os.File, error) {
			if path == published {
				return nil, errSeam
			}
			return realOpen(path)
		})
		mustContain(t, verifyPublishedSnapshot(raw, published, "/orig"), "seam failure")
	})
}

func TestFileHashErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	override(t, &closeHash, func(*os.File) error { return errSeam })
	if _, err := fileHash(path); err == nil {
		t.Fatal("expected close failure")
	}
}

func TestVerifySnapshotsErrors(t *testing.T) {
	run := func(t *testing.T, left, right func(dir string)) error {
		t.Helper()
		a, b, leftDir, rightDir := twoRoots(t)
		left(leftDir)
		right(rightDir)
		return verifyRootSnapshot(a, "node", b, "node")
	}
	t.Run("second lstat", func(t *testing.T) {
		realLstat := rootLstat
		calls := 0
		override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realLstat(root, name)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "seam failure")
	})
	t.Run("second manifest check", func(t *testing.T) {
		a, b, leftDir, rightDir := twoRoots(t)
		writeAt(t, leftDir, "node", "x", 0o644)
		writeAt(t, rightDir, "node", "x", 0o644)
		file, err := os.Open(filepath.Join(leftDir, "node"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		am := &fsx.PermissionManifest{}
		if err := am.Record("node", file, 0o644); err != nil {
			t.Fatal(err)
		}
		bm := &fsx.PermissionManifest{}
		if err := verifyPreparedSnapshots(a, "node", am, b, "node", bm); err == nil {
			t.Fatal("expected manifest failure")
		}
	})
	t.Run("left readlink", func(t *testing.T) {
		override(t, &rootReadlink, func(*os.Root, string) (string, error) { return "", errSeam })
		err := run(t, func(dir string) {
			if e := os.Symlink("t", filepath.Join(dir, "node")); e != nil {
				t.Fatal(e)
			}
		}, func(dir string) {
			if e := os.Symlink("t", filepath.Join(dir, "node")); e != nil {
				t.Fatal(e)
			}
		})
		mustContain(t, err, "seam failure")
	})
	t.Run("right readlink", func(t *testing.T) {
		realReadlink := rootReadlink
		calls := 0
		override(t, &rootReadlink, func(root *os.Root, name string) (string, error) {
			calls++
			if calls == 2 {
				return "", errSeam
			}
			return realReadlink(root, name)
		})
		err := run(t, func(dir string) {
			if e := os.Symlink("t", filepath.Join(dir, "node")); e != nil {
				t.Fatal(e)
			}
		}, func(dir string) {
			if e := os.Symlink("t", filepath.Join(dir, "node")); e != nil {
				t.Fatal(e)
			}
		})
		mustContain(t, err, "seam failure")
	})
	t.Run("link target", func(t *testing.T) {
		err := run(t, func(dir string) {
			if e := os.Symlink("a", filepath.Join(dir, "node")); e != nil {
				t.Fatal(e)
			}
		}, func(dir string) {
			if e := os.Symlink("b", filepath.Join(dir, "node")); e != nil {
				t.Fatal(e)
			}
		})
		mustContain(t, err, "snapshot mismatch")
	})
	dirCase := func(t *testing.T, leftChild, rightChild string) (func(string), func(string)) {
		mk := func(child string) func(string) {
			return func(dir string) {
				if err := os.Mkdir(filepath.Join(dir, "node"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeAt(t, filepath.Join(dir, "node"), child, "x", 0o644)
			}
		}
		return mk(leftChild), mk(rightChild)
	}
	t.Run("right dir open", func(t *testing.T) {
		realOpenFile := rootOpenFile
		calls := 0
		override(t, &rootOpenFile, func(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realOpenFile(root, name, flag, perm)
		})
		l, r := dirCase(t, "a", "a")
		mustContain(t, run(t, l, r), "seam failure")
	})
	t.Run("dir stat left", func(t *testing.T) {
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 1 {
				return nil, errSeam
			}
			return realStat(file)
		})
		l, r := dirCase(t, "a", "a")
		mustContain(t, run(t, l, r), "seam failure")
	})
	t.Run("dir stat right", func(t *testing.T) {
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realStat(file)
		})
		l, r := dirCase(t, "a", "a")
		mustContain(t, run(t, l, r), "seam failure")
	})
	t.Run("dir identity", func(t *testing.T) {
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 1 {
				return fakeInfoFor(t, 88), nil
			}
			return realStat(file)
		})
		l, r := dirCase(t, "a", "a")
		mustContain(t, run(t, l, r), "snapshot mismatch")
	})
	t.Run("dir read left", func(t *testing.T) {
		realRead := readDirFile
		calls := 0
		override(t, &readDirFile, func(file *os.File, n int) ([]os.DirEntry, error) {
			calls++
			if calls == 1 {
				return nil, errSeam
			}
			return realRead(file, n)
		})
		l, r := dirCase(t, "a", "a")
		mustContain(t, run(t, l, r), "seam failure")
	})
	t.Run("dir read right", func(t *testing.T) {
		realRead := readDirFile
		calls := 0
		override(t, &readDirFile, func(file *os.File, n int) ([]os.DirEntry, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realRead(file, n)
		})
		l, r := dirCase(t, "a", "a")
		mustContain(t, run(t, l, r), "seam failure")
	})
	t.Run("dir entry count", func(t *testing.T) {
		l := func(dir string) {
			if err := os.Mkdir(filepath.Join(dir, "node"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		r := func(dir string) {
			if err := os.Mkdir(filepath.Join(dir, "node"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeAt(t, filepath.Join(dir, "node"), "extra", "x", 0o644)
		}
		mustContain(t, run(t, l, r), "snapshot mismatch")
	})
	t.Run("dir entry name", func(t *testing.T) {
		l, r := dirCase(t, "a", "b")
		mustContain(t, run(t, l, r), "snapshot mismatch")
	})
	t.Run("file size", func(t *testing.T) {
		err := run(t, func(dir string) { writeAt(t, dir, "node", "longer", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "snapshot mismatch")
	})
	t.Run("file open right", func(t *testing.T) {
		realOpenFile := rootOpenFile
		calls := 0
		override(t, &rootOpenFile, func(root *os.Root, name string, flag int, perm os.FileMode) (*os.File, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realOpenFile(root, name, flag, perm)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "seam failure")
	})
	t.Run("file stat left", func(t *testing.T) {
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 1 {
				return nil, errSeam
			}
			return realStat(file)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "seam failure")
	})
	t.Run("file stat right", func(t *testing.T) {
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return nil, errSeam
			}
			return realStat(file)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "seam failure")
	})
	t.Run("file identity", func(t *testing.T) {
		realStat := statFile
		calls := 0
		override(t, &statFile, func(file *os.File) (os.FileInfo, error) {
			calls++
			if calls == 2 {
				return fakeInfoFor(t, 89), nil
			}
			return realStat(file)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "snapshot mismatch")
	})
	t.Run("file hash left", func(t *testing.T) {
		override(t, &copyHash, func(io.Writer, io.Reader) (int64, error) {
			return 0, errSeam
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "seam failure")
	})
	t.Run("file hash right", func(t *testing.T) {
		realCopy := copyHash
		calls := 0
		override(t, &copyHash, func(dst io.Writer, src io.Reader) (int64, error) {
			calls++
			if calls == 2 {
				return 0, errSeam
			}
			return realCopy(dst, src)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "seam failure")
	})
	t.Run("file content", func(t *testing.T) {
		err := run(t, func(dir string) { writeAt(t, dir, "node", "a", 0o644) }, func(dir string) { writeAt(t, dir, "node", "b", 0o644) })
		mustContain(t, err, "snapshot mismatch")
	})
	t.Run("final lstat right", func(t *testing.T) {
		realLstat := rootLstat
		calls := 0
		override(t, &rootLstat, func(root *os.Root, name string) (os.FileInfo, error) {
			calls++
			if calls == 4 {
				return nil, errSeam
			}
			return realLstat(root, name)
		})
		err := run(t, func(dir string) { writeAt(t, dir, "node", "x", 0o644) }, func(dir string) { writeAt(t, dir, "node", "x", 0o644) })
		mustContain(t, err, "snapshot mismatch")
	})
}
