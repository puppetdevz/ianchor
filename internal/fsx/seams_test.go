package fsx

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

// seamAddress extracts the code pointer of a function value so a seam can be
// compared with the call it is supposed to stand for.
func seamAddress(fn any) uintptr {
	return reflect.ValueOf(fn).Pointer()
}

// TestSeamsDefaultToRealCalls guards the property the whole coverage strategy
// relies on: while no test replaces a seam, every one of them is the real call.
// A seam that quietly became a stub would make production behaviour differ from
// what the tests execute.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	direct := map[string][2]uintptr{
		"lstatPath":          {seamAddress(lstatPath), seamAddress(os.Lstat)},
		"statResolvedTarget": {seamAddress(statResolvedTarget), seamAddress(os.Stat)},
		"readlinkPath":       {seamAddress(readlinkPath), seamAddress(os.Readlink)},
		"openRootPath":       {seamAddress(openRootPath), seamAddress(os.OpenRoot)},
		"openatPath":         {seamAddress(openatPath), seamAddress(unix.Openat)},
		"fstatatChild":       {seamAddress(fstatatChild), seamAddress(unix.Fstatat)},
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
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	statSeams := map[string]func(*os.File) (os.FileInfo, error){
		"statSourceDirIdentity": statSourceDirIdentity,
		"statPinnedDestination": statPinnedDestination,
		"statPinnedMode":        statPinnedMode,
		"statPinnedSource":      statPinnedSource,
		"statRegularSource":     statRegularSource,
		"statCreatedFile":       statCreatedFile,
		"statFinalMode":         statFinalMode,
		"statSearchDir":         statSearchDir,
	}
	for name, seam := range statSeams {
		got, err := seam(file)
		if err != nil || !os.SameFile(got, want) {
			t.Errorf("%s does not return the handle's own FileInfo: %v", name, err)
		}
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rootSeams := map[string]func(*os.Root, string) (os.FileInfo, error){
		"rootLstatCreatedSymlink": rootLstatCreatedSymlink,
		"rootLstatCreatedDir":     rootLstatCreatedDir,
	}
	for name, seam := range rootSeams {
		got, err := seam(root, "probe")
		if err != nil || !os.SameFile(got, want) {
			t.Errorf("%s does not lstat through the root: %v", name, err)
		}
	}

	dirHandle, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer dirHandle.Close()
	entries, err := readSearchDir(dirHandle, 1)
	if err != nil || len(entries) != 1 || entries[0].Name() != "probe" {
		t.Errorf("readSearchDir = %v, %v", entries, err)
	}
}
