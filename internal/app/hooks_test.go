package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/puppetdevz/ianchor/internal/cli"
	"github.com/puppetdevz/ianchor/internal/store"
)

func runApp(args []string) (int, string, string) {
	var out, errOut strings.Builder
	code := cli.Run(NewWithCoordinator(func() (func() error, error) { return func() error { return nil }, nil }), "1.1.1", args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUnreachableCommandBranches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runApp([]string{"init", "--dir", "ianchor"})
	if code != 0 {
		t.Fatalf("init code=%d out=%q err=%q", code, stdout, stderr)
	}

	originalList := listEntries
	listEntries = func(*store.DB, string) ([]store.Entry, error) { return nil, nil }
	t.Cleanup(func() { listEntries = originalList })
	code, stdout, stderr = runApp([]string{"list", "--json"})
	if code != 0 || !strings.Contains(stdout, "[]") || stderr != "" {
		t.Fatalf("nil list code=%d out=%q err=%q", code, stdout, stderr)
	}

	originalEncode := encodeListPayload
	encodeListPayload = func(any) (string, error) { return "", errors.New("encode failed") }
	t.Cleanup(func() { encodeListPayload = originalEncode })
	code, stdout, stderr = runApp([]string{"list", "--json"})
	if code == 0 || !strings.Contains(stderr, "encode failed") || strings.Contains(stdout, "[") {
		t.Fatalf("encode code=%d out=%q err=%q", code, stdout, stderr)
	}
	encodeListPayload = originalEncode
	listEntries = originalList

	originalLookup := entryAfterRelink
	entryAfterRelink = func(*store.DB, string) (*store.Entry, error) {
		return nil, errors.New("lookup failed")
	}
	t.Cleanup(func() { entryAfterRelink = originalLookup })
	source := filepath.Join(home, "tracked")
	if err := os.WriteFile(source, []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = runApp([]string{"add", source})
	if code != 0 {
		t.Fatalf("add code=%d err=%q", code, stderr)
	}
	code, stdout, stderr = runApp([]string{"recover", "tracked"})
	if code != 0 {
		t.Fatalf("recover before relink code=%d err=%q", code, stderr)
	}
	code, stdout, stderr = runApp([]string{"relink", "tracked"})
	if code == 0 || !strings.Contains(stderr, "lookup failed") || strings.Contains(stdout, "Relinked") {
		t.Fatalf("relink code=%d out=%q err=%q", code, stdout, stderr)
	}
	entryAfterRelink = originalLookup

	originalScript := completionScript
	completionScript = func(string) ([]byte, error) { return nil, errors.New("script missing") }
	t.Cleanup(func() { completionScript = originalScript })
	code, stdout, stderr = runApp([]string{"completion", "zsh"})
	if code == 0 || !strings.Contains(stderr, "script missing") || stdout != "" {
		t.Fatalf("completion code=%d out=%q err=%q", code, stdout, stderr)
	}
}
