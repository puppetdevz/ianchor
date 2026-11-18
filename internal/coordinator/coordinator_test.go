package coordinator

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestConcurrentLeases(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "coord")
	lease, err := AcquireIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		other, err := AcquireIn(dir)
		if err == nil {
			err = other.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("second lease returned too early: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lease did not acquire")
	}
}

func TestContendedDirectoryDoesNotBlockAnother(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireIn(filepath.Join(root, "one"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		lease, err := AcquireIn(filepath.Join(root, "one"))
		if err == nil {
			err = lease.Close()
		}
		done <- err
	}()
	other, err := AcquireIn(filepath.Join(root, "two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("contender did not complete")
	}
}

func TestLeaseProcess(t *testing.T) {
	if dir := os.Getenv("IANCHOR_COORD_TEST_DIR"); dir != "" {
		fmt.Fprintln(os.Stdout, "ready")
		lease, err := AcquireIn(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "coord")
	lease, err := AcquireIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	otherStarted := make(chan struct{})
	otherDone := make(chan error, 1)
	go func() {
		close(otherStarted)
		other, err := AcquireIn(dir)
		if err == nil {
			err = other.Close()
		}
		otherDone <- err
	}()
	<-otherStarted
	command := exec.Command(os.Args[0], "-test.run=^TestLeaseProcess$")
	command.Env = append(os.Environ(), "IANCHOR_COORD_TEST_DIR="+dir)
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child barrier %q: %v", line, err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("child acquired while locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("child did not acquire")
	}
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-process contender did not acquire")
	}
}

func TestFlockSurvivesIndependentDescriptorClose(t *testing.T) {
	if path := os.Getenv("IANCHOR_FLOCK_PROBE"); path != "" {
		fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer unix.Close(fd)
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if os.Getenv("IANCHOR_FLOCK_BUSY") == "1" {
			if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
				t.Fatalf("expected contention, got %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "coord")
	lease, err := AcquireIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"coordinator.lock", "coordinator.db"} {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	probe := func(busy string) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFlockSurvivesIndependentDescriptorClose$")
		cmd.Env = append(os.Environ(), "IANCHOR_FLOCK_PROBE="+filepath.Join(dir, "coordinator.lock"), "IANCHOR_FLOCK_BUSY="+busy)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("probe: %v\n%s", err, out)
		}
	}
	probe("1")
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	probe("0")
}

func TestRestrictiveUmask(t *testing.T) {
	if dir := os.Getenv("IANCHOR_UMASK_DIR"); dir != "" {
		oldMask := unix.Umask(0o777)
		defer unix.Umask(oldMask)
		lease, err := AcquireIn(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := lease.Close(); err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]os.FileMode{"": 0o700, "coordinator.lock": 0o600, "coordinator.db": 0o600} {
			info, err := os.Stat(filepath.Join(dir, name))
			if err != nil || info.Mode().Perm() != want {
				t.Fatalf("%s: mode %v, error %v", name, info, err)
			}
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestrictiveUmask$")
	cmd.Env = append(os.Environ(), "IANCHOR_UMASK_DIR="+filepath.Join(t.TempDir(), "coord"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("umask subprocess: %v\n%s", err, out)
	}
}

func TestFirstUseDuringPermissionInitializationFailsClosed(t *testing.T) {
	for _, point := range []string{"directory", "lock"} {
		t.Run(point, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "coord")
			ready, resume := make(chan struct{}), make(chan struct{})
			oldDir, oldFile := chmodDirectory, chmodFile
			defer func() { chmodDirectory, chmodFile = oldDir, oldFile }()
			if point == "directory" {
				chmodDirectory = func(path string) error {
					if err := unix.Fchmodat(unix.AT_FDCWD, path, 0, unix.AT_SYMLINK_NOFOLLOW); err != nil {
						return err
					}
					close(ready)
					<-resume
					return oldDir(path)
				}
			} else {
				chmodFile = func(fd int, mode uint32) error {
					if err := unix.Fchmod(fd, 0); err != nil {
						return err
					}
					close(ready)
					<-resume
					chmodFile = oldFile
					return oldFile(fd, mode)
				}
			}
			done := make(chan error, 1)
			go func() {
				lease, err := AcquireIn(dir)
				if err == nil {
					err = lease.Close()
				}
				done <- err
			}()
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				close(resume)
				t.Fatal("creator did not reach barrier")
			}
			_, err := AcquireIn(dir)
			close(resume)
			if err == nil {
				t.Fatal("accepted incomplete permissions")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			lease, err := AcquireIn(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInterruptedInitializationDoesNotRepairExistingFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "coord")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, "coordinator.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lock, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireIn(dir); err == nil {
		t.Fatal("accepted unfinished lock")
	}
	info, err := os.Stat(lock)
	if err != nil || info.Mode().Perm() != 0 {
		t.Fatalf("existing lock altered: %v %v", info, err)
	}
}

func TestDirectoryChmodDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_ = chmodNewDirectory(link)
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target changed: %v %v", info, err)
	}
}

func TestDirectory(t *testing.T) {
	if !strings.HasPrefix(Directory(), "/Users/Shared/.ianchor-") {
		t.Fatalf("unexpected directory %q", Directory())
	}
}

func TestUnsafeLocations(t *testing.T) {
	root := t.TempDir()
	if _, err := AcquireIn(filepath.Join(root, "absent", "child")); err == nil {
		t.Fatal("missing parent accepted")
	}
	insecure := filepath.Join(root, "insecure")
	if err := os.Mkdir(insecure, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(insecure, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireIn(insecure); err == nil {
		t.Fatal("insecure directory accepted")
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(private, "coordinator.db")
	if err := os.Symlink(filepath.Join(root, "target"), db); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireIn(private); err == nil {
		t.Fatal("database symlink accepted")
	}
	if err := os.Remove(db); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-journal", []byte("unsafe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(db+"-journal", 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireIn(private); err == nil {
		t.Fatal("insecure sidecar accepted")
	}
	if err := os.Remove(db + "-journal"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(db, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireIn(private); err == nil {
		t.Fatal("insecure database accepted")
	}
	if err := os.Chmod(db, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireIn(private); err == nil {
		t.Fatal("invalid database accepted")
	}
}
