package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The helper runs the real CLI error reporter and spinner in a child process;
// signal tests can therefore observe the process exit without killing tests.
func TestSpinnerProcessHelper(t *testing.T) {
	mode := os.Getenv("IANCHOR_TEST_SPINNER_PROCESS")
	if mode == "" {
		return
	}
	command := &Command{Name: "ianchor", Action: func(ctx *Context) error {
		spinner := ctx.Spinner()
		if mode == "error" {
			time.Sleep(300 * time.Millisecond)
			spinner.Stop()
			return errors.New("delayed list failure")
		}
		// A normal process signal must terminate even while the spinner is live.
		select {}
	}}
	os.Exit(Run(command, "1.1.1", nil, strings.NewReader(""), os.Stdout, os.Stderr))
}

type spinnerCapture struct {
	mu    sync.Mutex
	data  bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (c *spinnerCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n, err := c.data.Write(p)
	if bytes.Contains(c.data.Bytes(), []byte(spinnerFrames[1]+" Loading entries…")) {
		c.once.Do(func() { close(c.ready) })
	}
	return n, err
}
func (c *spinnerCapture) String() string { c.mu.Lock(); defer c.mu.Unlock(); return c.data.String() }

func TestSpinnerProcessFailureAndSignals(t *testing.T) {
	for _, mode := range []string{"error", "SIGINT", "SIGTERM"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestSpinnerProcessHelper$")
			cmd.Env = append(os.Environ(), "IANCHOR_TEST_SPINNER_PROCESS="+mode, "FORCE_COLOR=0")
			var stdout bytes.Buffer
			capture := &spinnerCapture{ready: make(chan struct{})}
			cmd.Stdout = &stdout
			cmd.Stderr = capture
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-capture.ready:
			case err := <-done:
				t.Fatalf("exited before drawing frames: %v %q", err, capture.String())
			case <-time.After(10 * time.Second):
				t.Fatal("spinner did not draw frames")
			}
			if mode != "error" {
				signal := syscall.SIGINT
				if mode == "SIGTERM" {
					signal = syscall.SIGTERM
				}
				if err := cmd.Process.Signal(signal); err != nil {
					t.Fatal(err)
				}
			}
			var result error
			select {
			case result = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("spinner process did not terminate")
			}
			if stdout.Len() != 0 {
				t.Fatalf("spinner polluted stdout: %q", stdout.String())
			}
			stderr := capture.String()
			if !strings.HasPrefix(stderr, spinnerFrames[0]+" Loading entries…\r"+spinnerFrames[1]+" Loading entries…") {
				t.Fatalf("frame order: %q", stderr)
			}
			var exited *exec.ExitError
			if !errors.As(result, &exited) {
				t.Fatalf("expected failed exit, got %v", result)
			}
			if mode == "error" {
				if exited.ExitCode() != 1 || !strings.HasSuffix(stderr, "\r\x1b[KError: delayed list failure\n") || strings.Count(stderr, "\x1b[K") != 1 {
					t.Fatalf("failure cleanup: code=%d stderr=%q", exited.ExitCode(), stderr)
				}
			} else {
				expected := syscall.SIGINT
				if mode == "SIGTERM" {
					expected = syscall.SIGTERM
				}
				status := exited.Sys().(syscall.WaitStatus)
				if !status.Signaled() || status.Signal() != expected {
					t.Fatal(fmt.Sprintf("signal exit: %v", status))
				}
			}
		})
	}
}
