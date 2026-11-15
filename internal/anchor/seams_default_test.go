package anchor

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"golang.org/x/sys/unix"
)

func seamAddress(fn any) uintptr { return reflect.ValueOf(fn).Pointer() }

// TestSeamsDefaultToRealCalls guards the base assumption of the coverage
// strategy: with no test replacing a seam, every seam is the real call. A seam
// that silently became a stub would make the tested behaviour differ from the
// shipped behaviour.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	direct := map[string][2]uintptr{
		"evalSymlinksPath": {seamAddress(evalSymlinksPath), seamAddress(filepath.EvalSymlinks)},
		"lstatPath":        {seamAddress(lstatPath), seamAddress(os.Lstat)},
		"statPath":         {seamAddress(statPath), seamAddress(os.Stat)},
		"readlinkPath":     {seamAddress(readlinkPath), seamAddress(os.Readlink)},
		"openPath":         {seamAddress(openPath), seamAddress(os.Open)},
		"readDirPath":      {seamAddress(readDirPath), seamAddress(os.ReadDir)},
		"mkdirPath":        {seamAddress(mkdirPath), seamAddress(os.Mkdir)},
		"stagingPath":      {seamAddress(stagingPath), seamAddress(fsx.StagingPath)},
		"openRootPath":     {seamAddress(openRootPath), seamAddress(os.OpenRoot)},
		"copyHash":         {seamAddress(copyHash), seamAddress(io.Copy)},
		"absPath":          {seamAddress(absPath), seamAddress(filepath.Abs)},
	}
	for name, pair := range direct {
		if pair[0] != pair[1] {
			t.Errorf("%s does not default to the real call", name)
		}
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "probe")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	want, err := handle.Stat()
	if err != nil {
		t.Fatal(err)
	}
	statSeams := map[string]func(*os.File) (os.FileInfo, error){"statFile": statFile}
	for name, seam := range statSeams {
		got, err := seam(handle)
		if err != nil || !os.SameFile(got, want) {
			t.Errorf("%s does not return the handle's FileInfo: %v", name, err)
		}
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rootSeams := map[string]func(*os.Root, string) (os.FileInfo, error){"rootLstat": rootLstat}
	for name, seam := range rootSeams {
		got, err := seam(root, "probe")
		if err != nil || !os.SameFile(got, want) {
			t.Errorf("%s does not lstat through the root: %v", name, err)
		}
	}
	openSeams := map[string]func(*os.Root, string) (*os.File, error){"rootOpen": rootOpen}
	for name, seam := range openSeams {
		file, err := seam(root, "probe")
		if err != nil {
			t.Errorf("%s open: %v", name, err)
			continue
		}
		file.Close()
	}
	child, err := rootOpenRoot(root, ".")
	if err != nil {
		t.Errorf("rootOpenRoot: %v", err)
	} else {
		child.Close()
	}

	closable, err := os.OpenFile(filepath.Join(dir, "closable"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := closeHash(closable); err != nil {
		t.Fatalf("closeHash: %v", err)
	}
	if _, err := closable.Write([]byte("x")); err == nil {
		t.Error("closeHash did not close the file")
	}

	handleDir, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer handleDir.Close()
	var st unix.Stat_t
	if err := unix.Fstatat(int(handleDir.Fd()), "probe", &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		t.Errorf("direct fstatat: %v", err)
	}
	if err := unix.Unlinkat(int(handleDir.Fd()), "missing", 0); err == nil {
		t.Error("direct unlinkat unexpectedly succeeded")
	}
}
