package anchor

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// override replaces a package seam with value for the duration of the test.
func override[T any](t *testing.T, target *T, value T) {
	t.Helper()
	prev := *target
	*target = value
	t.Cleanup(func() { *target = prev })
}

// sequence returns values in call order, repeating the last one.
func sequence[T any](values ...T) func(int) T {
	return func(call int) T {
		if call > len(values) {
			call = len(values)
		}
		return values[call-1]
	}
}

var errSeam = errors.New("seam failure")

// fakeInfoFor builds a FileInfo that never compares equal to a real inode.
func fakeInfoFor(t *testing.T, ino uint64) os.FileInfo {
	t.Helper()
	return &fakeFileInfo{name: "fake", mode: 0o644, ino: ino, mod: time.Unix(0, 0)}
}

type fakeFileInfo struct {
	name string
	mode os.FileMode
	ino  uint64
	size int64
	mod  time.Time
	dir  bool
}

func (f *fakeFileInfo) Name() string { return f.name }
func (f *fakeFileInfo) Size() int64  { return f.size }
func (f *fakeFileInfo) Mode() os.FileMode {
	mode := f.mode
	if f.dir {
		mode |= os.ModeDir
	}
	return mode
}
func (f *fakeFileInfo) ModTime() time.Time { return f.mod }
func (f *fakeFileInfo) IsDir() bool        { return f.dir }
func (f *fakeFileInfo) Sys() any           { return &syscall.Stat_t{Ino: f.ino} }

// renameAtReal is the unmodified exclusive rename used by tests that replace
// the renameAt seam for a subset of names.
func renameAtReal(fromFD int, from string, toFD int, to string) error {
	return renameAtExclusive(fromFD, from, toFD, to)
}

// failingAt returns a statPath replacement that only fails for calls made from
// the given source line, leaving every other call untouched.
func failingAt(t *testing.T, file string, line int, real func(string) (os.FileInfo, error)) func(string) (os.FileInfo, error) {
	t.Helper()
	return func(path string) (os.FileInfo, error) {
		_, caller, at, _ := runtime.Caller(1)
		if strings.HasSuffix(caller, "/"+file) && at == line {
			return nil, errSeam
		}
		return real(path)
	}
}

// failingInFunc fails the nth call made from inside the named function in the
// anchor package for the given argument, so a gofmt-driven line shift cannot
// silently disable it while sibling call sites stay untouched.
func failingInFunc(t *testing.T, function, argument string, n int, real func(string) (string, error)) func(string) (string, error) {
	t.Helper()
	seen := 0
	return func(path string) (string, error) {
		if path != argument {
			return real(path)
		}
		if pc, _, _, ok := runtime.Caller(1); ok {
			if frame := runtime.FuncForPC(pc); frame != nil && strings.HasSuffix(frame.Name(), "."+function) {
				seen++
				if seen == n {
					return "", errSeam
				}
			}
		}
		return real(path)
	}
}

// failingAtN fails only the nth call made from the given source line.
func failingAtN(t *testing.T, file string, line, n int, real func(string) (os.FileInfo, error)) func(string) (os.FileInfo, error) {
	t.Helper()
	seen := 0
	return func(path string) (os.FileInfo, error) {
		_, caller, at, _ := runtime.Caller(1)
		if strings.HasSuffix(caller, "/"+file) && at == line {
			seen++
			if seen == n {
				return nil, errSeam
			}
		}
		return real(path)
	}
}
