//go:build darwin

package anchor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSnapshotFIFOReplacementDoesNotBlock(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			base := t.TempDir()
			for _, name := range []string{"a", "b"} {
				var err error
				if directory {
					err = os.Mkdir(filepath.Join(base, name), 0700)
				} else {
					err = os.WriteFile(filepath.Join(base, name), nil, 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			root, err := os.OpenRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			victim := filepath.Join(base, "a")
			opHook = func(op, path string) error {
				if op != "snapshot-open" || path != "a" {
					return nil
				}
				if err := os.Rename(victim, victim+"-saved"); err != nil {
					return err
				}
				return unix.Mkfifo(victim, 0600)
			}
			defer func() { opHook = nil }()
			done := make(chan error, 1)
			go func() { done <- verifyRootSnapshot(root, "a", root, "b") }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("accepted replacement")
				}
			case <-time.After(time.Second):
				// Release a buggy blocking open so the test exits without leaking workers.
				fd, err := unix.Open(victim, unix.O_RDWR|unix.O_NONBLOCK, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer unix.Close(fd)
				<-done
				t.Fatal("snapshot comparison blocked on a replacement FIFO")
			}
		})
	}
}
