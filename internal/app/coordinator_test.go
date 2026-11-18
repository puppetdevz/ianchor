package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/puppetdevz/ianchor/internal/cli"
	"github.com/puppetdevz/ianchor/internal/coordinator"
	"github.com/puppetdevz/ianchor/internal/store"
)

type promptSignal struct{ ready chan struct{} }

func (w *promptSignal) Write(p []byte) (int, error) {
	select {
	case <-w.ready:
	default:
		close(w.ready)
	}
	return len(p), nil
}

func TestLockHeldDuringPrompt(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	base := filepath.Join(root, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "coordinator")
	acquire := func() (func() error, error) {
		lease, err := coordinator.AcquireIn(dir)
		if err != nil {
			return nil, err
		}
		return lease.Close, nil
	}
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	prompt := &promptSignal{ready: make(chan struct{})}
	finished := make(chan int, 1)
	go func() {
		finished <- cli.Run(NewWithCoordinator(acquire), "test", []string{"init"}, reader, prompt, io.Discard)
	}()
	select {
	case <-prompt.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt not reached")
	}
	attempted := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		close(attempted)
		lease, err := coordinator.AcquireIn(dir)
		if err == nil {
			err = lease.Close()
		}
		second <- err
	}()
	<-attempted
	select {
	case err := <-second:
		t.Fatalf("second command bypassed prompt lease: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := writer.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("init returned %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("init did not finish")
	}
	select {
	case err := <-second:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lease did not finish")
	}
}

func TestLocalLeaseFactory(t *testing.T) {
	old := localCoordinatorDirectory
	t.Cleanup(func() { localCoordinatorDirectory = old })
	root := t.TempDir()
	localCoordinatorDirectory = func() string { return filepath.Join(root, "missing", "lock") }
	if _, err := acquireLocalLease(); err == nil {
		t.Fatal("missing parent accepted")
	}
	localCoordinatorDirectory = func() string { return filepath.Join(root, "lock") }
	var stdout, stderr bytes.Buffer
	cli.Run(New(), "test", []string{"list"}, strings.NewReader(""), &stdout, &stderr)
	if _, err := os.Stat(filepath.Join(root, "lock", "coordinator.db")); err != nil {
		t.Fatal(err)
	}
}

func TestCoordinationErrors(t *testing.T) {
	failure := errors.New("lock failure")
	var stdout, stderr bytes.Buffer
	code := cli.Run(NewWithCoordinator(func() (func() error, error) { return nil, failure }), "test", []string{"list"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "cannot acquire local operation lock: lock failure") {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}
	root := t.TempDir()
	t.Setenv("HOME", root)
	base := filepath.Join(root, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = cli.Run(NewWithCoordinator(func() (func() error, error) { return func() error { return failure }, nil }), "test", []string{"init", "--dir", "example"}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 || !strings.Contains(stderr.String(), "operation may have completed but local lock release failed: lock failure") {
		t.Fatalf("code %d stderr %q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(base, "example", "ianchor.db")); err != nil {
		t.Fatalf("operation did not complete: %v", err)
	}
}

func TestCoordinatorValidationFailures(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	base := filepath.Join(root, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected validation failure")
	t.Run("config dir resolution", func(t *testing.T) {
		old := resolveConfigDir
		defer func() { resolveConfigDir = old }()
		resolveConfigDir = func(string, string) (string, error) { return "", failure }
		if _, err := configDir(); !errors.Is(err, failure) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("config dir guard", func(t *testing.T) {
		old := validateCoordinatorConfig
		defer func() { validateCoordinatorConfig = old }()
		validateCoordinatorConfig = func(string) error { return failure }
		if _, err := configDir(); !errors.Is(err, failure) {
			t.Fatalf("got %v", err)
		}
	})
	for _, point := range []string{"file resolution", "file guard"} {
		t.Run(point, func(t *testing.T) {
			oldFile, oldGuard := resolveConfigFile, validateCoordinatorConfig
			defer func() { resolveConfigFile, validateCoordinatorConfig = oldFile, oldGuard }()
			if point == "file resolution" {
				resolveConfigFile = func() (string, error) { return "", failure }
			} else {
				validateCoordinatorConfig = func(string) error { return failure }
			}
			var out, errOut bytes.Buffer
			code := cli.Run(NewWithCoordinator(func() (func() error, error) { return func() error { return nil }, nil }), "test", []string{"init", "--dir", "x"}, strings.NewReader(""), &out, &errOut)
			if code == 0 || !strings.Contains(errOut.String(), failure.Error()) {
				t.Fatalf("code %d error %q", code, errOut.String())
			}
		})
	}
}

func TestInitSaveFailureAfterGuard(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	base := filepath.Join(root, "Library", "Mobile Documents", "com~apple~CloudDocs")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".config", "ianchor.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := cli.Run(NewWithCoordinator(func() (func() error, error) { return func() error { return nil }, nil }), "test", []string{"init", "--dir", "example"}, strings.NewReader(""), &out, &errOut)
	if code == 0 {
		t.Fatal("directory config file accepted")
	}
	if _, err := os.Stat(filepath.Join(base, "example", "ianchor.db")); err != nil {
		t.Fatalf("init was not reached: %v", err)
	}
}

func TestCoordinationReleaseOnPanic(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	base := filepath.Join(root, "Library", "Mobile Documents", "com~apple~CloudDocs", "ianchor")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	old := openDBFunc
	openDBFunc = func(string) (*store.DB, error) { panic("test panic") }
	t.Cleanup(func() { openDBFunc = old })
	released := false
	defer func() {
		if recover() == nil || !released {
			t.Errorf("panic did not release lease")
		}
	}()
	var stdout, stderr bytes.Buffer
	cli.Run(NewWithCoordinator(func() (func() error, error) { return func() error { released = true; return nil }, nil }), "test", []string{"list"}, strings.NewReader(""), &stdout, &stderr)
}
