package app_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/puppetdevz/ianchor/internal/app"
	"github.com/puppetdevz/ianchor/internal/cli"
	"github.com/puppetdevz/ianchor/internal/completions"
	"github.com/puppetdevz/ianchor/internal/store"
)

func testCoordinator() (func() error, error) { return func() error { return nil }, nil }

type harness struct {
	t         *testing.T
	home      string
	configDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TERM", "xterm-256color")
	// Keep Azure Pipelines detection from forcing color in plain-output tests.
	for _, name := range []string{"TF_BUILD", "AGENT_NAME"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	base := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	configDir := filepath.Join(base, "ianchor")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, home: home, configDir: configDir}
}

type result struct {
	stdout string
	stderr string
	code   int
}

func (h *harness) run(stdin string, args ...string) result {
	h.t.Helper()
	var out, errOut bytes.Buffer
	code := cli.Run(app.NewWithCoordinator(testCoordinator), "1.1.1", args, strings.NewReader(stdin), &out, &errOut)
	return result{stdout: out.String(), stderr: errOut.String(), code: code}
}

func (h *harness) mustRun(stdin string, args ...string) result {
	h.t.Helper()
	r := h.run(stdin, args...)
	if r.code != 0 {
		h.t.Fatalf("%v failed (%d): %s%s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

func (h *harness) write(rel, content string) string {
	h.t.Helper()
	path := filepath.Join(h.home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestListJSONPreservesUnicodeSeparatorsAndLiteralEscapes(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	path := h.write("separator\u2028and\u2029and\\u2028", "content")
	h.mustRun("", "add", path)
	r := h.mustRun("", "list", "--json")
	if !strings.Contains(r.stdout, "separator\u2028and\u2029and\\\\u2028") {
		t.Fatalf("JSON differs from JSON.stringify: %s", r.stdout)
	}
}

func TestListJSONPreservesHTMLCharacters(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	path := h.write("a&b<test>", "content")
	h.mustRun("", "add", path)
	result := h.mustRun("", "list", "--json")
	if !strings.Contains(result.stdout, `"name": "a&b<test>"`) {
		t.Fatalf("JSON escaped ordinary characters: %s", result.stdout)
	}
}

func TestCompleteProtocol(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	db, err := store.Open(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"zshrc", "app config", "colon:a:b", `back\slash`, "wild*card", "-n", "--help",
		`quote's"name`, "!bang", "line\nbreak", "line\rbreak", "dollar$(touch X)",
	}
	for _, name := range names {
		if err := db.AddEntry(name, nil, "file", "unlinked"); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}
	db.Close()
	dbPath := filepath.Join(h.configDir, "ianchor.db")
	before := hashFile(t, dbPath)

	r := h.mustRun("", "__complete")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("stderr = %q code = %d", r.stderr, r.code)
	}
	if !strings.HasSuffix(r.stdout, "\x00") {
		t.Fatalf("missing trailing NUL: %q", r.stdout)
	}
	fields := strings.Split(strings.TrimSuffix(r.stdout, "\x00"), "\x00")
	var want []string
	for _, name := range names {
		if strings.ContainsAny(name, "\n\r") {
			continue
		}
		want = append(want, name)
	}
	// SQLite orders by binary collation.
	want = []string{"!bang", "--help", "-n", "app config", `back\slash`, "colon:a:b", `dollar$(touch X)`, `quote's"name`, "wild*card", "zshrc"}
	if strings.Join(fields, "|") != strings.Join(want, "|") {
		t.Fatalf("candidates = %q, want %q", fields, want)
	}
	if got := hashFile(t, dbPath); got != before {
		t.Fatal("__complete modified the database")
	}
}

func TestCompleteSilentWhenUnavailable(t *testing.T) {
	h := newHarness(t)
	// No database yet: the completion must stay silent and must not create one.
	r := h.mustRun("", "__complete")
	if r.stdout != "" || r.stderr != "" {
		t.Fatalf("output = %q %q", r.stdout, r.stderr)
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "ianchor.db")); !os.IsNotExist(err) {
		t.Fatalf("database was created: %v", err)
	}

	// Corrupt database: still silent, still exit 0.
	dir := filepath.Join(h.home, "Library", "Mobile Documents", "com~apple~CloudDocs", "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ianchor.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, ".config", "ianchor.json"), []byte("{\"dirName\":\"broken\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = h.mustRun("", "__complete")
	if r.stdout != "" || r.stderr != "" {
		t.Fatalf("corrupt output = %q %q", r.stdout, r.stderr)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "ianchor.db")); err != nil || string(data) != "not a database" {
		t.Fatalf("corrupt database was touched: %q %v", data, err)
	}
}

func TestCompletionCommand(t *testing.T) {
	h := newHarness(t)
	bash, _ := completions.Script("bash")
	zsh, _ := completions.Script("zsh")
	cases := map[string]string{
		"bash": string(bash),
		"zsh":  string(zsh),
		"":     string(zsh),
	}
	for shell, want := range cases {
		args := []string{"completion"}
		if shell != "" {
			args = append(args, shell)
		}
		r := h.mustRun("", args...)
		if r.stdout != want {
			t.Fatalf("%s output mismatch", shell)
		}
		if r.stderr != "" {
			t.Fatalf("%s stderr = %q", shell, r.stderr)
		}
	}
	r := h.run("", "completion", "fish")
	if r.code != 1 || r.stdout != "" || r.stderr != "Unsupported shell: fish. Expected zsh or bash.\n" {
		t.Fatalf("fish = %+v", r)
	}
}

func TestInitPromptAndDiscovery(t *testing.T) {
	h := newHarness(t)
	h.write(".zshrc", "zsh\n")
	if err := os.WriteFile(filepath.Join(h.configDir, ".zshrc"), []byte("zsh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.configDir, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.configDir, "logo.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := h.mustRun("\n", "init")
	want := "iCloud config directory name (default: ianchor): iAnchor initialized!\n" +
		"Config directory: " + h.configDir + "\n" +
		"Discovered 1 existing file(s).\n" +
		"\nTracked entries:\n  .zshrc -> " + filepath.Join(h.home, ".zshrc") + "\n"
	if r.stdout != want {
		t.Fatalf("stdout = %q, want %q", r.stdout, want)
	}
	if got := h.run("", "init", "--dir", "../outside"); got.code == 0 {
		t.Fatalf("escaping init accepted: %+v", got)
	}
}

func TestCommandFailuresStayLocal(t *testing.T) {
	h := newHarness(t)
	if got := h.run("", "add", filepath.Join(h.home, "missing")); got.code == 0 {
		t.Fatalf("missing add accepted: %+v", got)
	}
	if got := h.run("", "recover", "missing"); got.code == 0 {
		t.Fatalf("missing recover accepted: %+v", got)
	}
	if got := h.run("", "relink", "missing"); got.code == 0 {
		t.Fatalf("missing relink accepted: %+v", got)
	}
	if got := h.run("", "remove", "missing", "--yes"); got.code == 0 {
		t.Fatalf("missing remove accepted: %+v", got)
	}
	h.mustRun("", "init", "--dir", "ianchor")
	if got := h.run("", "add", h.write(".zshrc", "one\n"), "--name", "../outside"); got.code == 0 {
		t.Fatalf("escaping add accepted: %+v", got)
	}
}

func TestInitCustomDirectoryPersists(t *testing.T) {
	h := newHarness(t)
	r := h.mustRun("work\n", "init")
	custom := filepath.Join(filepath.Dir(h.configDir), "work")
	if !strings.Contains(r.stdout, "Config directory: "+custom) {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if info, err := os.Stat(custom); err != nil || !info.IsDir() {
		t.Fatalf("custom directory missing: %v", err)
	}
	config, err := os.ReadFile(filepath.Join(h.home, ".config", "ianchor.json"))
	if err != nil || !strings.Contains(string(config), `"dirName": "work"`) {
		t.Fatalf("config = %q %v", config, err)
	}
	if r := h.mustRun("", "list"); !strings.Contains(r.stdout, "No entries found.") {
		t.Fatalf("list = %q", r.stdout)
	}
}

func TestAddConflictPrompt(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	first := h.write(".zshrc", "one\n")
	h.mustRun("", "add", first)
	second := h.write("second/.zshrc", "two\n")
	r := h.mustRun("abc\n", "add", second)
	if !strings.HasPrefix(r.stdout, `"zshrc" already exists. Enter a prefix (e.g. abc -> abc_zshrc): `) {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "abc_zshrc")); err != nil {
		t.Fatalf("prefixed file missing: %v", err)
	}
	if entry, _ := storeLookup(h, "abc_zshrc"); entry != "linked" {
		t.Fatalf("status = %q", entry)
	}
}

func storeLookup(h *harness, name string) (string, error) {
	db, err := store.OpenReadOnly(h.configDir)
	if err != nil {
		return "", err
	}
	defer db.Close()
	entry, err := db.GetEntry(name)
	if err != nil || entry == nil {
		return "", err
	}
	return entry.Status, nil
}

func TestAddConflictPrefixCollision(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	h.mustRun("", "add", h.write("a/.zshrc", "one\n"))
	h.mustRun("", "add", h.write("b/.zshrc", "two\n"), "--name", "abc_zshrc")
	r := h.run("abc\n", "add", h.write("c/.zshrc", "three\n"))
	if r.code != 1 {
		t.Fatalf("expected failure, got %+v", r)
	}
	want := `"abc_zshrc" already exists in iCloud directory. Use --force to overwrite.`
	if !strings.Contains(r.stdout+r.stderr, want) {
		t.Fatalf("output = %q %q", r.stdout, r.stderr)
	}
}

func TestRemoveUnknownPromptsBeforeLookup(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	prompt := `Remove "ghost"? This will delete the file from iCloud and the database record. [y/N] `
	for _, tc := range []struct {
		input string
		want  result
	}{
		{"n\n", result{stdout: prompt + "Cancelled.\n", code: 0}},
		{"y\n", result{stdout: prompt, stderr: "Error: Entry \"ghost\" not found.\n", code: 1}},
	} {
		if got := h.run(tc.input, "remove", "ghost"); got != tc.want {
			t.Fatalf("input %q: got %+v want %+v", tc.input, got, tc.want)
		}
	}
	db, err := store.Open(h.configDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := db.AllEntries()
	if err != nil || len(entries) != 0 {
		t.Fatalf("unexpected records: %+v %v", entries, err)
	}
}

func TestRemoveConfirmation(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	h.mustRun("", "add", h.write(".zshrc", "zsh\n"))

	r := h.mustRun("n\n", "remove", "zshrc")
	want := `Remove "zshrc"? This will delete the file from iCloud and the database record. [y/N] Cancelled.` + "\n"
	if r.stdout != want {
		t.Fatalf("cancel stdout = %q", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "zshrc")); err != nil {
		t.Fatal("cancelled removal deleted the cloud file")
	}

	r = h.mustRun("y\n", "remove", "zshrc")
	if !strings.Contains(r.stdout, `Removed "zshrc".`) {
		t.Fatalf("stdout = %q", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(h.configDir, "zshrc")); !os.IsNotExist(err) {
		t.Fatal("cloud file survived removal")
	}
}

func TestListStatusesAndJSON(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	linked := h.write(".zshrc", "zsh\n")
	h.mustRun("", "add", linked)
	unlinked := h.write(".vimrc", "vim\n")
	h.mustRun("", "add", unlinked)
	if err := os.Remove(unlinked); err != nil {
		t.Fatal(err)
	}
	r := h.mustRun("", "list", "--json")
	if !strings.Contains(r.stdout, `"status": "linked"`) || !strings.Contains(r.stdout, `"status": "unlinked"`) {
		t.Fatalf("json = %q", r.stdout)
	}
	if !strings.HasPrefix(r.stdout, "[\n") || !strings.HasSuffix(r.stdout, "]\n") {
		t.Fatalf("json framing = %q", r.stdout)
	}
	if err := os.Remove(filepath.Join(h.configDir, "zshrc")); err != nil {
		t.Fatal(err)
	}
	r = h.mustRun("", "list")
	if !strings.Contains(r.stdout, "deleted") {
		t.Fatalf("table = %q", r.stdout)
	}
	if !strings.Contains(r.stdout, "\x1b[90m┌") {
		t.Fatalf("table borders missing: %q", r.stdout)
	}
}

func TestRecoverAndRelinkEndToEnd(t *testing.T) {
	h := newHarness(t)
	h.mustRun("", "init", "--dir", "ianchor")
	source := h.write(".zshrc", "cloud\n")
	h.mustRun("", "add", source)
	r := h.mustRun("", "recover", "zshrc")
	want := "Recovered \"zshrc\" locally.\n" +
		"iCloud file and tracking entry preserved; other devices can keep using them.\n" +
		"  Restored to: " + source + "\n"
	if r.stdout != want {
		t.Fatalf("recover stdout = %q", r.stdout)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "cloud\n" {
		t.Fatalf("local = %q %v", data, err)
	}
	if err := os.WriteFile(source, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = h.mustRun("", "relink", "zshrc")
	if !strings.Contains(r.stdout, "Relinked \"zshrc\".") {
		t.Fatalf("relink stdout = %q", r.stdout)
	}
	if data, err := os.ReadFile(source + ".bak"); err != nil || string(data) != "edited\n" {
		t.Fatalf("backup = %q %v", data, err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "cloud\n" {
		t.Fatalf("restored link content = %q %v", data, err)
	}
}
