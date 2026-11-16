package anchor

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
	"golang.org/x/sys/unix"
)

func seamAddress(fn any) uintptr { return reflect.ValueOf(fn).Pointer() }

// TestSeamsDefaultToRealCalls guards the base assumption of the coverage
// strategy: with no test replacing a seam, every seam is the real call. A seam
// that silently became a stub would make the tested behaviour differ from the
// shipped behaviour.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	direct := map[string][2]uintptr{
		"evalSymlinksPath":           {seamAddress(evalSymlinksPath), seamAddress(filepath.EvalSymlinks)},
		"lstatPath":                  {seamAddress(lstatPath), seamAddress(os.Lstat)},
		"statPath":                   {seamAddress(statPath), seamAddress(os.Stat)},
		"readlinkPath":               {seamAddress(readlinkPath), seamAddress(os.Readlink)},
		"openPath":                   {seamAddress(openPath), seamAddress(os.Open)},
		"readDirPath":                {seamAddress(readDirPath), seamAddress(os.ReadDir)},
		"mkdirPath":                  {seamAddress(mkdirPath), seamAddress(os.Mkdir)},
		"stagingPath":                {seamAddress(stagingPath), seamAddress(fsx.StagingPath)},
		"openRootPath":               {seamAddress(openRootPath), seamAddress(os.OpenRoot)},
		"copyHash":                   {seamAddress(copyHash), seamAddress(io.Copy)},
		"absPath":                    {seamAddress(absPath), seamAddress(filepath.Abs)},
		"copyIntoBackup":             {seamAddress(copyIntoBackup), seamAddress(fsx.CopyIntoRoot)},
		"copyNodeModesIntoBackup":    {seamAddress(copyNodeModesIntoBackup), seamAddress(fsx.CopyNodePreserveModesIntoRoot)},
		"copyNodeImpl":               {seamAddress(copyNodeImpl), seamAddress(copyNodeWithIdentity)},
		"makeCopiedTreeWritableHook": {seamAddress(makeCopiedTreeWritableHook), seamAddress(makeCopiedTreeWritable)},
		"renameAt":                   {seamAddress(renameAt), seamAddress(renameAtExclusive)},
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

	// Remaining root-relative seams must act on the root, not be no-ops.
	if err := rootMkdir(root, "made-dir", 0o750); err != nil {
		t.Errorf("rootMkdir: %v", err)
	} else if info, statErr := os.Stat(filepath.Join(dir, "made-dir")); statErr != nil || info.Mode().Perm() != 0o750 {
		t.Errorf("rootMkdir created %v, %v", info, statErr)
	}
	createdFile, err := rootOpenFile(root, "opened-file", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Errorf("rootOpenFile: %v", err)
	} else {
		createdFile.Close()
	}
	if err := os.Symlink("probe", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if target, err := rootReadlink(root, "link"); err != nil || target != "probe" {
		t.Errorf("rootReadlink = %q, %v", target, err)
	}
	if err := rootRemove(root, "opened-file"); err != nil {
		t.Errorf("rootRemove: %v", err)
	} else if _, statErr := os.Stat(filepath.Join(dir, "opened-file")); statErr == nil {
		t.Error("rootRemove did not remove the entry")
	}

	if entries, err := readDirFile(handleDir, -1); err != nil || len(entries) == 0 {
		t.Errorf("readDirFile = %d entries, %v", len(entries), err)
	}
	if err := chmodFile(handle, 0o640); err != nil {
		t.Errorf("chmodFile: %v", err)
	} else if info, statErr := handle.Stat(); statErr != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("chmodFile left mode %v, %v", info.Mode().Perm(), statErr)
	}

	// opHook is an injection point only: production must leave it nil so no
	// hook can divert a destructive operation.
	if opHook != nil {
		t.Error("opHook is not nil in production")
	}
	if err := checkOp("rename", "/probe"); err != nil {
		t.Errorf("checkOp with no hook = %v, want nil", err)
	}

	// renameDirect and dbAllEntries are closures, so only behaviour proves
	// they still do the real work.
	from := filepath.Join(dir, "rename-from")
	if err := os.WriteFile(from, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(dir, "rename-to")
	if err := renameDirect(from, to); err != nil {
		t.Errorf("renameDirect: %v", err)
	} else if _, statErr := os.Stat(to); statErr != nil {
		t.Errorf("renameDirect did not move the entry: %v", statErr)
	}

	// dbAllEntries must still be the real listing, not a stub returning nil.
	dbDir := t.TempDir()
	db, err := store.Open(dbDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	original := "/probe/path"
	if err := db.AddEntry("probe", &original, "file", "linked"); err != nil {
		t.Fatalf("AddEntry: %v", err)
	}
	listed, err := dbAllEntries(db)
	if err != nil {
		t.Fatalf("dbAllEntries: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "probe" || listed[0].OriginalPath == nil || *listed[0].OriginalPath != original {
		t.Fatalf("dbAllEntries = %+v, want the stored probe row", listed)
	}

	// manifestRestore / manifestVerify must really drive the manifest.
	manifest := &fsx.PermissionManifest{}
	manifestDir := filepath.Join(dir, "manifest")
	if err := os.Mkdir(manifestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mRoot, err := os.OpenRoot(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	defer mRoot.Close()
	manifestFile, err := mRoot.OpenFile("node", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := manifest.Record("node", manifestFile, 0o600); err != nil {
		t.Fatalf("manifest.Record: %v", err)
	}
	manifestFile.Close()
	if err := manifestRestore(manifest, mRoot); err != nil {
		t.Errorf("manifestRestore: %v", err)
	}
	if err := manifestVerify(manifest, mRoot); err != nil {
		t.Errorf("manifestVerify: %v", err)
	}
}
