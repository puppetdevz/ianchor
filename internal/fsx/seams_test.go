package fsx

import (
	"crypto/rand"
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
		"workingDirectory":   {seamAddress(workingDirectory), seamAddress(os.Getwd)},
	}
	for name, pair := range direct {
		if pair[0] != pair[1] {
			t.Errorf("%s does not default to the real call", name)
		}
	}

	// randomReader is compared as an interface value: production reads
	// crypto/rand.Reader, and a stub would silently weaken every staging token.
	if randomReader != rand.Reader {
		t.Errorf("randomReader is not crypto/rand.Reader")
	}
	// The fault injection seams are exported for internal/anchor tests only and
	// must stay nil, or a shipped binary would fail its first copy.
	if TestCopyFault != nil {
		t.Error("TestCopyFault is not nil in production")
	}
	if TestPermissionFault != nil {
		t.Error("TestPermissionFault is not nil in production")
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

	// The root-relative openers must really open through the root, not return
	// a nil handle that the caller never notices.
	openSeams := map[string]func(*os.Root, string) (*os.File, error){
		"openChildDot":         openChildDot,
		"openSourceDot":        openSourceDot,
		"openSourceParentDot":  openSourceParentDot,
		"openRegularParentDot": openRegularParentDot,
	}
	for name, seam := range openSeams {
		handle, err := seam(root, "probe")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		opened, statErr := handle.Stat()
		handle.Close()
		if statErr != nil || !os.SameFile(opened, want) {
			t.Errorf("%s opened something else: %v", name, statErr)
		}
	}
	childRoots := map[string]func(*os.Root, string) (*os.Root, error){
		"openChildRoot":        openChildRoot,
		"openSourceRootNested": openSourceRootNested,
	}
	for name, seam := range childRoots {
		childRoot, err := seam(root, ".")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		probe, statErr := childRoot.Lstat("probe")
		childRoot.Close()
		if statErr != nil || !os.SameFile(probe, want) {
			t.Errorf("%s did not resolve through the root: %v", name, statErr)
		}
	}

	// manifestOpen creates inside the root; it must not silently drop the
	// flags or the permissions.
	created, err := manifestOpen(root, "manifest-probe", os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		t.Errorf("manifestOpen: %v", err)
	} else {
		created.Close()
		if info, statErr := os.Stat(filepath.Join(dir, "manifest-probe")); statErr != nil || info.Mode().Perm() != 0o640 {
			t.Errorf("manifestOpen created %v, %v", info, statErr)
		}
	}

	chmodSeams := map[string]func(*os.File, os.FileMode) error{
		"chmodPinnedTemporary":  chmodPinnedTemporary,
		"chmodPinnedFinal":      chmodPinnedFinal,
		"chmodCreatedFileFinal": chmodCreatedFileFinal,
		"chmodPrepareRestore":   chmodPrepareRestore,
	}
	for name, seam := range chmodSeams {
		// Each iteration starts from a mode the seam must actually change, so
		// an earlier real call cannot make a later stub look successful.
		if err := file.Chmod(0o644); err != nil {
			t.Fatalf("reset probe mode: %v", err)
		}
		if err := seam(file, 0o600); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		info, statErr := file.Stat()
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s did not change the handle's mode: %v", name, statErr)
		}
	}
}
