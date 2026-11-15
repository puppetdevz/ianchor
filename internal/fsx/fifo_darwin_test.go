//go:build darwin

package fsx

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPreparedCopyDirectoryFIFOReplacementDoesNotBlock(t *testing.T) {
	base := t.TempDir()
	source, dest := filepath.Join(base, "source"), filepath.Join(base, "copy")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var manifest PermissionManifest
	defer manifest.Close()
	done := make(chan error, 1)
	go func() {
		done <- CopyNodeIntoRootPrepared(source, root, "copy", nil, &manifest, func(os.FileInfo) {
			if err := os.Rename(dest, dest+"-saved"); err != nil {
				panic(err)
			}
			if err := unix.Mkfifo(dest, 0600); err != nil {
				panic(err)
			}
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted replacement")
		}
	case <-time.After(time.Second):
		fd, err := unix.Open(dest, unix.O_RDWR|unix.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		<-done
		t.Fatal("directory copy blocked on a replacement FIFO")
	}
}
