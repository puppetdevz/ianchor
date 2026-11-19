package main

import (
	"io"
	"os"
	"strings"
	"testing"

	ianchor "github.com/puppetdevz/ianchor"
)

func TestMainUsesExitWrapper(t *testing.T) {
	originalExit := exit
	originalArgs := os.Args
	originalOut := os.Stdout
	originalErr := os.Stderr
	t.Cleanup(func() {
		exit = originalExit
		os.Args = originalArgs
		os.Stdout = originalOut
		os.Stderr = originalErr
	})

	var code int
	called := false
	exit = func(status int) {
		called = true
		code = status
	}
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	os.Args = []string{"ianchor", "--version"}
	main()
	stdoutW.Close()
	stderrW.Close()
	out, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if !called || code != 0 || !strings.Contains(string(out), ianchor.Version) {
		t.Fatalf("called=%v code=%d out=%q", called, code, out)
	}

	called = false
	os.Args = []string{"ianchor", "not-a-command"}
	stderr2, stderr2W, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = stderr2W
	main()
	stderr2W.Close()
	errOut, err := io.ReadAll(stderr2)
	if err != nil {
		t.Fatal(err)
	}
	if !called || code == 0 || !strings.Contains(string(errOut), "error:") {
		t.Fatalf("called=%v code=%d err=%q", called, code, errOut)
	}
}
