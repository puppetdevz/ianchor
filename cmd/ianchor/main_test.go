package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	dir := t.TempDir()
	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := os.Create(filepath.Join(dir, "err"))
	if err != nil {
		t.Fatal(err)
	}
	code := run([]string{"--version"}, nil, out, errOut)
	out.Close()
	errOut.Close()
	got, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(string(got), "1.1.1") {
		t.Fatalf("code=%d out=%q", code, got)
	}
}

func TestMainExit(t *testing.T) {
	if os.Getenv("IANCHOR_TEST_MAIN") == "1" {
		os.Args = []string{"ianchor", "--version"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainExit")
	cmd.Env = append(os.Environ(), "IANCHOR_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main exit: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "1.1.1") {
		t.Fatalf("output=%q", out)
	}
}
