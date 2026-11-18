package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/puppetdevz/ianchor/internal/app"
	"github.com/puppetdevz/ianchor/internal/cli"
	"github.com/puppetdevz/ianchor/internal/config"
)

func runCLI(args []string, stdin string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := cli.Run(app.NewWithCoordinator(testCoordinator), "1.1.1", args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestCommandsFailWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	cases := [][]string{
		{"list"},
		{"add", "missing"},
		{"recover", "missing"},
		{"relink", "missing"},
		{"remove", "missing"},
		{"init", "--dir", "ianchor"},
	}
	for _, args := range cases {
		code, stdout, stderr := runCLI(args, "")
		if code == 0 || !strings.Contains(stderr, "Error:") || stdout != "" {
			t.Fatalf("%v code=%d out=%q err=%q", args, code, stdout, stderr)
		}
	}
	code, stdout, stderr := runCLI([]string{"__complete"}, "")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("complete code=%d out=%q err=%q", code, stdout, stderr)
	}
}

func TestInitRejectsEscapingNameAndMissingDrive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, _, stderr := runCLI([]string{"init", "--dir", ".."}, "")
	if code == 0 || !strings.Contains(stderr, `Config directory name ".." must be a single path component`) {
		t.Fatalf("escaping name code=%d err=%q", code, stderr)
	}

	code, _, stderr = runCLI([]string{"init", "--dir", "ianchor"}, "")
	if code == 0 || !strings.Contains(stderr, config.ErrICloudMissing) {
		t.Fatalf("missing drive code=%d err=%q", code, stderr)
	}
}

func TestInitSaveFailsWhenConfigFileIsBlocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config"), []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI([]string{"init", "--dir", "ianchor"}, "")
	if code == 0 || !strings.Contains(stderr, "Error:") || strings.Contains(stdout, "iAnchor initialized!") {
		t.Fatalf("blocked save code=%d out=%q err=%q", code, stdout, stderr)
	}
}

func TestDatabaseOpenFailures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := [][]string{
		{"list"},
		{"add", filepath.Join(home, "missing")},
		{"recover", "missing"},
		{"relink", "missing"},
		{"remove", "--yes", "missing"},
	}
	for _, args := range cases {
		code, _, stderr := runCLI(args, "")
		if code == 0 || !strings.Contains(stderr, "unable to open database file") {
			t.Fatalf("%v code=%d err=%q", args, code, stderr)
		}
	}
}

func TestClosedPromptsExitQuietly(t *testing.T) {
	h := newHarness(t)
	r := h.run("", "init")
	if r.code != 0 || !strings.Contains(r.stdout, "iCloud config directory name") || r.stderr != "" {
		t.Fatalf("init prompt = %+v", r)
	}

	r = h.run("", "remove", "ghost")
	if r.code != 0 || !strings.Contains(r.stdout, `Remove "ghost"?`) || r.stderr != "" {
		t.Fatalf("remove prompt = %+v", r)
	}

	first := h.write("one/file", "one")
	second := h.write("two/file", "two")
	h.mustRun("", "add", first)
	r = h.run("", "add", second)
	if r.code != 0 || !strings.Contains(r.stdout, "already exists") || r.stderr != "" {
		t.Fatalf("add prompt = %+v", r)
	}
	if _, err := os.Lstat(second); err != nil || h.run("", "list").code != 0 {
		t.Fatal("closed prompt changed the source or listing")
	}
}
