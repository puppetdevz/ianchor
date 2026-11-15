package anchor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/puppetdevz/ianchor/internal/store"
)

// Test discovery and idempotency using the complete filter list from the Node
// baseline, with an existing row that must not be rewritten.
func TestInitDiscoveryFilteringAndRepeat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "cloud")
	dir := filepath.Join(base, "ianchor")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".zshrc", "unknown", ".DS_Store", "Thumbs.db", "ianchor.db.backup", "image.PNG", "a.jpg", "a.jpeg", "a.gif", "a.bmp", "a.svg", "a.ico", "a.webp", "a.tiff", "a.tif", "a.heic", "a.heif"} {
		writeFile(t, filepath.Join(dir, name), name)
	}
	if err := os.Mkdir(filepath.Join(dir, ".bak"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nvim"), 0755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := "/legacy/custom"
	if err := db.AddEntry("unknown", &original, "directory", "linked"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	first, err := Init(base, "ianchor")
	if err != nil {
		t.Fatal(err)
	}
	if first.Discovered != 2 || len(first.Entries) != 3 {
		t.Fatalf("first init: %+v", first)
	}
	wantNames := []string{".zshrc", "nvim", "unknown"}
	for i, name := range wantNames {
		if first.Entries[i].Name != name {
			t.Fatalf("ordering: %+v", first.Entries)
		}
	}
	if e := first.Entries[0]; e.OriginalPath == nil || *e.OriginalPath != filepath.Join(home, ".zshrc") || e.Type != "file" || e.Status != "unlinked" {
		t.Fatalf("known file: %+v", e)
	}
	if e := first.Entries[1]; e.OriginalPath == nil || *e.OriginalPath != filepath.Join(home, ".config", "nvim") || e.Type != "directory" || e.Status != "unlinked" {
		t.Fatalf("known directory: %+v", e)
	}
	if e := first.Entries[2]; e.OriginalPath == nil || *e.OriginalPath != original || e.Type != "directory" || e.Status != "linked" {
		t.Fatalf("existing row was rewritten: %+v", e)
	}
	second, err := Init(base, "ianchor")
	if err != nil {
		t.Fatal(err)
	}
	if second.Discovered != 0 || !reflect.DeepEqual(second.Entries, first.Entries) {
		t.Fatalf("repeat changed records: first %+v second %+v", first, second)
	}
}

// fixture is an isolated iCloud config directory plus a database.
type fixture struct {
	t         *testing.T
	configDir string
	db        *store.DB
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "iCloud", "ianchor")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	db, err := store.Open(configDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &fixture{t: t, configDir: configDir, db: db}
}

// local creates a file outside the config directory and returns its path.
func (f *fixture) local(name, content string) string {
	f.t.Helper()
	path := filepath.Join(f.t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func (f *fixture) add(source string, name string, force bool) (AddResult, error) {
	f.t.Helper()
	return Add(f.db, AddOptions{ConfigDir: f.configDir, Source: source, Name: name, Force: force})
}

func (f *fixture) mustEntry(name string) *store.Entry {
	f.t.Helper()
	entry, err := f.db.GetEntry(name)
	if err != nil {
		f.t.Fatalf("get entry %s: %v", name, err)
	}
	if entry == nil {
		f.t.Fatalf("entry %s not found", name)
	}
	return entry
}

func assertSymlink(t *testing.T, link, target string) {
	t.Helper()
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat %s: %v", link, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink", link)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink %s: %v", link, err)
	}
	if got != target {
		t.Fatalf("%s -> %s, want %s", link, got, target)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists (err=%v)", path, err)
	}
}

func TestAddFile(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "export A=1\n")
	result, err := f.add(src, "", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if result.Name != "zshrc" {
		t.Fatalf("name = %q, want zshrc", result.Name)
	}
	dest := filepath.Join(f.configDir, "zshrc")
	if got := readFile(t, dest); got != "export A=1\n" {
		t.Fatalf("iCloud content = %q", got)
	}
	assertSymlink(t, src, dest)
	entry := f.mustEntry("zshrc")
	if entry.Status != "linked" || entry.Type != "file" || entry.OriginalPath == nil || *entry.OriginalPath != src {
		t.Fatalf("entry = %+v", *entry)
	}
}

func TestAddDirectory(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	ssh := filepath.Join(root, ".ssh")
	writeFile(t, filepath.Join(ssh, "config"), "Host *\n")
	if err := os.MkdirAll(filepath.Join(ssh, "keys"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ssh, "keys", "id"), "key\n")

	result, err := f.add(ssh, "", false)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if result.Type != "directory" {
		t.Fatalf("type = %q", result.Type)
	}
	dest := filepath.Join(f.configDir, "ssh")
	if got := readFile(t, filepath.Join(dest, "keys", "id")); got != "key\n" {
		t.Fatalf("nested content = %q", got)
	}
	assertSymlink(t, ssh, dest)
	if got := readFile(t, filepath.Join(ssh, "config")); got != "Host *\n" {
		t.Fatalf("content through symlink = %q", got)
	}
}

func TestAddCustomName(t *testing.T) {
	f := newFixture(t)
	src := f.local("sample.txt", "x")
	if _, err := f.add(src, "custom", false); err != nil {
		t.Fatalf("add: %v", err)
	}
	f.mustEntry("custom")
	assertMissing(t, filepath.Join(f.configDir, "sample.txt"))
}

func TestAddRejectsMissingSource(t *testing.T) {
	f := newFixture(t)
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := f.add(missing, "", false)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddRejectsSymlinkSource(t *testing.T) {
	f := newFixture(t)
	target := f.local("real", "x")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := f.add(link, "", false)
	if err == nil || !strings.Contains(err.Error(), "is already a symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddConflictPrefix(t *testing.T) {
	f := newFixture(t)
	first := f.local(".zshrc", "one")
	if _, err := f.add(first, "", false); err != nil {
		t.Fatalf("first add: %v", err)
	}
	second := f.local(".zshrc", "two")
	var asked string
	result, err := Add(f.db, AddOptions{
		ConfigDir: f.configDir,
		Source:    second,
		OnConflict: func(destName string) (string, error) {
			asked = destName
			return "abc", nil
		},
	})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if asked != "zshrc" {
		t.Fatalf("asked for %q", asked)
	}
	if result.Name != "abc_zshrc" {
		t.Fatalf("name = %q", result.Name)
	}
	assertSymlink(t, second, filepath.Join(f.configDir, "abc_zshrc"))
}

func TestAddConflictWithoutPrefixFails(t *testing.T) {
	f := newFixture(t)
	if _, err := f.add(f.local(".zshrc", "one"), "", false); err != nil {
		t.Fatal(err)
	}
	_, err := Add(f.db, AddOptions{
		ConfigDir:  f.configDir,
		Source:     f.local(".zshrc", "two"),
		OnConflict: func(string) (string, error) { return "", nil },
	})
	if err == nil || !strings.Contains(err.Error(), "already exists in iCloud directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestAddForceReplacesDirectoryWithoutMerging(t *testing.T) {
	f := newFixture(t)
	first := filepath.Join(t.TempDir(), "conf")
	writeFile(t, filepath.Join(first, "old"), "stale")
	if _, err := f.add(first, "conf", false); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "conf")
	writeFile(t, filepath.Join(second, "new"), "fresh")
	if _, err := f.add(second, "conf", true); err != nil {
		t.Fatalf("force add: %v", err)
	}
	dest := filepath.Join(f.configDir, "conf")
	if _, err := os.Stat(filepath.Join(dest, "new")); err != nil {
		t.Fatalf("new content missing: %v", err)
	}
	assertMissing(t, filepath.Join(dest, "old"))
	assertMissing(t, filepath.Join(first, "old"))
	assertSymlink(t, second, dest)
}

func TestAddForceTakeoverMovesStaleLinkOnly(t *testing.T) {
	f := newFixture(t)
	oldSource := f.local("first.conf", "old")
	first, err := f.add(oldSource, "conf", false)
	if err != nil {
		t.Fatal(err)
	}
	// An unrelated symlink that happens to share the name must survive.
	otherDir := t.TempDir()
	otherTarget := filepath.Join(otherDir, "elsewhere")
	writeFile(t, otherTarget, "keep")
	otherLink := filepath.Join(t.TempDir(), "unrelated")
	if err := os.Symlink(otherTarget, otherLink); err != nil {
		t.Fatal(err)
	}
	before := f.mustEntry("conf")
	newSource := f.local("second.conf", "new")
	second, err := f.add(newSource, "conf", true)
	if err != nil {
		t.Fatalf("force add: %v", err)
	}
	after := f.mustEntry("conf")
	if after.ID != before.ID {
		t.Fatalf("id changed: %d -> %d", after.ID, before.ID)
	}
	if after.CreatedAt == nil || before.CreatedAt == nil || *after.CreatedAt != *before.CreatedAt {
		t.Fatalf("created_at changed: %v -> %v", before.CreatedAt, after.CreatedAt)
	}
	if after.OriginalPath == nil || *after.OriginalPath != second.OriginalPath {
		t.Fatalf("original_path = %v, want %s", after.OriginalPath, second.OriginalPath)
	}
	if after.Status != "linked" {
		t.Fatalf("status = %q", after.Status)
	}
	assertMissing(t, first.OriginalPath)
	assertSymlink(t, newSource, filepath.Join(f.configDir, "conf"))
	if got := readFile(t, otherLink); got != "keep" {
		t.Fatalf("unrelated link content = %q", got)
	}
}

func TestForceTakeoverPreservesUnrelatedOldOriginal(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			old := f.local("old", "old cloud")
			if _, err := f.add(old, "entry", false); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(old); err != nil {
				t.Fatal(err)
			}
			var target string
			if kind == "symlink" {
				target = f.local("unrelated", "untouched")
				if err := os.Symlink(target, old); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, old, "untouched")
			}
			before := f.mustEntry("entry")
			earlier := "2001-01-01 00:00:00"
			before.UpdatedAt = &earlier
			if err := f.db.RestoreEntry(*before); err != nil {
				t.Fatal(err)
			}
			newSource := filepath.Join(t.TempDir(), "dir")
			writeFile(t, filepath.Join(newSource, "config"), "new")
			if _, err := f.add(newSource, "entry", true); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, old); got != "untouched" {
				t.Fatalf("old path changed: %q", got)
			}
			if kind == "symlink" {
				assertSymlink(t, old, target)
			}
			after := f.mustEntry("entry")
			if after.ID != before.ID || after.CreatedAt == nil || before.CreatedAt == nil || *after.CreatedAt != *before.CreatedAt || after.UpdatedAt == nil || *after.UpdatedAt == earlier || after.Type != "directory" || after.Status != "linked" || after.OriginalPath == nil || *after.OriginalPath != newSource {
				t.Fatalf("unexpected row: before=%+v after=%+v", before, after)
			}
			assertSymlink(t, newSource, filepath.Join(f.configDir, "entry"))
		})
	}
}

func TestAddRejectsOverlap(t *testing.T) {
	f := newFixture(t)
	inside := filepath.Join(f.configDir, "nested")
	writeFile(t, inside, "x")
	for _, source := range []string{inside, f.configDir} {
		_, err := f.add(source, "", false)
		if err == nil || !strings.Contains(err.Error(), "overlaps the iCloud config directory") {
			t.Fatalf("%s: err = %v", source, err)
		}
	}
}

func TestAddRejectsUnsafeNames(t *testing.T) {
	f := newFixture(t)
	src := f.local("payload", "x")
	for _, name := range []string{"..", "../escape", "a/b", "/abs", ".bak", "ianchor.db", "ianchor.db-wal", "a\x00b"} {
		if _, err := f.add(src, name, false); err == nil {
			t.Fatalf("name %q accepted", name)
		}
	}
	// The source must be untouched by every rejection.
	if got := readFile(t, src); got != "x" {
		t.Fatalf("source content = %q", got)
	}
	assertMissing(t, filepath.Join(f.configDir, "..", "escape"))
}

func TestAddAcceptsSpecialNames(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"中文 名称", "quote's\"name", "!bang", "-n", "--help", "a:b", "back\\slash", "line\nbreak"} {
		src := f.local("payload", name)
		if _, err := f.add(src, name, false); err != nil {
			t.Fatalf("name %q rejected: %v", name, err)
		}
		f.mustEntry(name)
	}
}

func TestRecoverRestoresLocalCopyAndKeepsCloud(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "cloud\n")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	before := *f.mustEntry("zshrc")
	if _, err := Recover(f.db, f.configDir, "zshrc", false); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if got := readFile(t, src); got != "cloud\n" {
		t.Fatalf("local = %q", got)
	}
	if got := readFile(t, filepath.Join(f.configDir, "zshrc")); got != "cloud\n" {
		t.Fatalf("cloud = %q", got)
	}
	if got := readFile(t, filepath.Join(f.configDir, bakDirName, "zshrc")); got != "cloud\n" {
		t.Fatalf("backup = %q", got)
	}
	after := *f.mustEntry("zshrc")
	if after.ID != before.ID || after.Name != before.Name || after.Status != before.Status ||
		after.Type != before.Type || *after.CreatedAt != *before.CreatedAt ||
		*after.UpdatedAt != *before.UpdatedAt || *after.OriginalPath != *before.OriginalPath {
		t.Fatalf("entry changed: %+v -> %+v", before, after)
	}
	entries, err := List(f.db, f.configDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Status != "unlinked" {
		t.Fatalf("status = %q", entries[0].Status)
	}
}

func TestRecoverDirectory(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	dir := filepath.Join(root, ".ssh")
	writeFile(t, filepath.Join(dir, "config"), "Host *\n")
	if _, err := f.add(dir, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(f.db, f.configDir, "ssh", false); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "config")); got != "Host *\n" {
		t.Fatalf("local = %q", got)
	}
	if got := readFile(t, filepath.Join(f.configDir, bakDirName, "ssh", "config")); got != "Host *\n" {
		t.Fatalf("backup = %q", got)
	}
}

func TestRecoverNullOriginalDoesNotCreateBackup(t *testing.T) {
	f := newFixture(t)
	cloud := filepath.Join(f.configDir, "mystery")
	writeFile(t, cloud, "cloud data")
	if err := f.db.AddEntry("mystery", nil, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	before := f.mustEntry("mystery")
	_, err := Recover(f.db, f.configDir, "mystery", false)
	if err == nil || err.Error() != `Entry "mystery" has no original path. Cannot recover locally.` {
		t.Fatalf("unexpected error: %v", err)
	}
	if readFile(t, cloud) != "cloud data" || !reflect.DeepEqual(before, f.mustEntry("mystery")) {
		t.Fatal("recover mutated cloud or record")
	}
	if _, err := os.Lstat(filepath.Join(f.configDir, ".bak", "mystery")); !os.IsNotExist(err) {
		t.Fatalf("backup unexpectedly exists: %v", err)
	}
}

func TestRecoverErrors(t *testing.T) {
	f := newFixture(t)
	if _, err := Recover(f.db, f.configDir, "ghost", false); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown entry err = %v", err)
	}
	if _, err := f.add(f.local(".zshrc", "x"), "", false); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(f.configDir, "zshrc")
	if err := os.Remove(cloud); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(f.db, f.configDir, "zshrc", false); err == nil || !strings.Contains(err.Error(), "not found in iCloud directory") {
		t.Fatalf("missing cloud err = %v", err)
	}
}

func TestRecoverConflictKeepsBackupContract(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "cloud\n")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "local edit\n")
	_, err := Recover(f.db, f.configDir, "zshrc", false)
	if err == nil || !strings.Contains(err.Error(), "already exists at original path") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, src); got != "local edit\n" {
		t.Fatalf("local = %q", got)
	}
	if got := readFile(t, filepath.Join(f.configDir, bakDirName, "zshrc")); got != "cloud\n" {
		t.Fatalf("backup = %q (conflict must still refresh the backup)", got)
	}
	if _, err := Recover(f.db, f.configDir, "zshrc", true); err != nil {
		t.Fatalf("force recover: %v", err)
	}
	if got := readFile(t, src); got != "cloud\n" {
		t.Fatalf("local after force = %q", got)
	}
}

func TestRelinkRepairsDanglingSymlink(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "cloud\n")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(f.configDir, "zshrc")

	// Sanity check: a correct link is reported, not silently accepted.
	if err := Relink(f.db, f.configDir, "zshrc"); err == nil || !strings.Contains(err.Error(), "already linked") {
		t.Fatalf("err = %v", err)
	}

	// DELTA-001: the link exists but its target does not (the baseline treated
	// this as "no local file" and then failed with EEXIST).
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(f.configDir, "gone"), src); err != nil {
		t.Fatal(err)
	}
	if err := Relink(f.db, f.configDir, "zshrc"); err != nil {
		t.Fatalf("relink dangling: %v", err)
	}
	assertSymlink(t, src, cloud)
	if got := readFile(t, src); got != "cloud\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestRelinkReplacesUnrelatedLiveSymlink(t *testing.T) {
	f := newFixture(t)
	source := f.local("source", "cloud")
	if _, err := f.add(source, "item", false); err != nil {
		t.Fatal(err)
	}
	other := f.local("other", "other data")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, source); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetStatus("item", "unlinked"); err != nil {
		t.Fatal(err)
	}
	if err := Relink(f.db, f.configDir, "item"); err != nil {
		t.Fatal(err)
	}
	assertSymlink(t, source, filepath.Join(f.configDir, "item"))
	if readFile(t, other) != "other data" || f.mustEntry("item").Status != "linked" {
		t.Fatal("relink changed unrelated file or failed to update status")
	}
	if _, err := os.Lstat(source + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unrelated symlink was backed up: %v", err)
	}
}

func TestRelinkMovesEntityToBackup(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "cloud\n")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil { // replace the symlink with a real file
		t.Fatal(err)
	}
	writeFile(t, src, "local edit\n")
	if err := Relink(f.db, f.configDir, "zshrc"); err != nil {
		t.Fatalf("relink: %v", err)
	}
	assertSymlink(t, src, filepath.Join(f.configDir, "zshrc"))
	if got := readFile(t, src+".bak"); got != "local edit\n" {
		t.Fatalf("backup = %q", got)
	}
	if got := f.mustEntry("zshrc").Status; got != "linked" {
		t.Fatalf("status = %q", got)
	}
}

func TestRelinkErrors(t *testing.T) {
	f := newFixture(t)
	if err := Relink(f.db, f.configDir, "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	if err := f.db.AddEntry("orphan", nil, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	noPath := f.local("cloud-only", "x")
	if _, err := f.add(noPath, "cloud-only", false); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetOriginalPath("cloud-only", nil); err != nil {
		t.Fatal(err)
	}
	if err := Relink(f.db, f.configDir, "orphan"); err == nil || !strings.Contains(err.Error(), "no original path") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveDeletesCloudAndRecord(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "x")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	result, err := Remove(f.db, f.configDir, "zshrc")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if result.Name != "zshrc" || *result.OriginalPath != src {
		t.Fatalf("result = %+v", result)
	}
	assertMissing(t, filepath.Join(f.configDir, "zshrc"))
	assertMissing(t, src)
	if entry, _ := f.db.GetEntry("zshrc"); entry != nil {
		t.Fatalf("record still present")
	}
}

func TestRemovePreservesUnrelatedSymlinkAndBackup(t *testing.T) {
	f := newFixture(t)
	source := f.local("source", "cloud")
	if _, err := f.add(source, "item", false); err != nil {
		t.Fatal(err)
	}
	other := f.local("other", "other data")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, source); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(f.configDir, ".bak", "item")
	writeFile(t, backup, "backup data")
	if _, err := Remove(f.db, f.configDir, "item"); err != nil {
		t.Fatal(err)
	}
	assertSymlink(t, source, other)
	if readFile(t, other) != "other data" || readFile(t, backup) != "backup data" {
		t.Fatal("unrelated target or backup changed")
	}
	if _, err := os.Lstat(filepath.Join(f.configDir, "item")); !os.IsNotExist(err) {
		t.Fatalf("cloud remains: %v", err)
	}
	if entry, err := f.db.GetEntry("item"); err != nil || entry != nil {
		t.Fatalf("record remains: %+v %v", entry, err)
	}
}

func TestRemoveKeepsUnrelatedTargets(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "x")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	// A device with a real local file (no link) must not be touched.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "local\n")
	if _, err := Remove(f.db, f.configDir, "zshrc"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := readFile(t, src); got != "local\n" {
		t.Fatalf("local = %q", got)
	}
}

func TestRemoveToleratesMissingCloud(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "x")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.configDir, "zshrc")); err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(f.db, f.configDir, "zshrc"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if entry, _ := f.db.GetEntry("zshrc"); entry != nil {
		t.Fatalf("record still present")
	}
}

func TestRemoveUnknownEntry(t *testing.T) {
	f := newFixture(t)
	if _, err := Remove(f.db, f.configDir, "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestListTreatsEquivalentRelativeLinkAsUnlinkedWithoutDBWrite(t *testing.T) {
	f := newFixture(t)
	source := f.local("source", "cloud")
	if _, err := f.add(source, "item", false); err != nil {
		t.Fatal(err)
	}
	cloud := filepath.Join(f.configDir, "item")
	relative, err := filepath.Rel(filepath.Dir(source), cloud)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(relative, source); err != nil {
		t.Fatal(err)
	}
	before := f.mustEntry("item")
	rows, err := List(f.db, f.configDir)
	if err != nil || len(rows) != 1 || rows[0].Status != "unlinked" {
		t.Fatalf("live state: %+v %v", rows, err)
	}
	if !reflect.DeepEqual(before, f.mustEntry("item")) {
		t.Fatal("list changed stored row")
	}
}

func TestListStatuses(t *testing.T) {
	f := newFixture(t)
	src := f.local(".zshrc", "x")
	if _, err := f.add(src, "", false); err != nil {
		t.Fatal(err)
	}
	if err := f.db.AddEntry("orphan", nil, "file", "unlinked"); err != nil {
		t.Fatal(err)
	}
	noPath := f.local("cloud-only", "x")
	if _, err := f.add(noPath, "cloud-only", false); err != nil {
		t.Fatal(err)
	}
	if err := f.db.SetOriginalPath("cloud-only", nil); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), ".gitconfig")
	if err := f.db.AddEntry("gitconfig", &missing, "file", "linked"); err != nil {
		t.Fatal(err)
	}
	entries, err := List(f.db, f.configDir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Name] = e.Status
	}
	want := map[string]string{"zshrc": "linked", "orphan": "deleted", "cloud-only": "unlinked", "gitconfig": "deleted"}
	for name, status := range want {
		if got[name] != status {
			t.Fatalf("%s status = %q, want %q", name, got[name], status)
		}
	}
	// List only reports live status; it must not rewrite the rows.
	if entry := f.mustEntry("gitconfig"); entry.Status != "linked" {
		t.Fatalf("stored status changed to %q", entry.Status)
	}
}
