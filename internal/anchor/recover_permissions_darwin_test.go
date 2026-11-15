//go:build darwin

package anchor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRecoverLowDescriptorLimitPreservesSource(t *testing.T) {
	if os.Getenv("IANCHOR_TEST_LOW_FD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRecoverLowDescriptorLimitPreservesSource$")
		cmd.Env = append(os.Environ(), "IANCHOR_TEST_LOW_FD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess: %v\n%s", err, out)
		}
		return
	}
	w := newWorld(t)
	original := filepath.Join(w.localDir, "tree")
	deep := original
	for i := 0; i < 24; i++ {
		deep = filepath.Join(deep, "d")
	}
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "value"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "tree"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(w.configDir, "tree")
	var old syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &old); err != nil {
		t.Fatal(err)
	}
	limited := old
	limited.Cur = 32
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
		t.Fatal(err)
	}
	_, recoverErr := Recover(w.db, w.configDir, "tree", false)
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &old); err != nil {
		t.Fatal(err)
	}
	if recoverErr == nil || !strings.Contains(recoverErr.Error(), "too many open files") {
		t.Fatalf("expected descriptor exhaustion: %v", recoverErr)
	}
	if strings.Contains(recoverErr.Error(), "rollback failed") {
		t.Fatalf("rollback failed: %v", recoverErr)
	}
	assertSymlink(t, original, source)
	if _, err := os.Lstat(filepath.Join(w.configDir, ".bak", "tree")); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(source, strings.Repeat("d/", 24), "value")); err != nil || string(got) != "data" {
		t.Fatalf("source: %q %v", got, err)
	}
}

func TestRecoverConflictRejectsUnreadableBackup(t *testing.T) {
	w := newWorld(t)
	original := w.write("acl-conflict", "payload")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "acl-conflict"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(w.configDir, "acl-conflict")
	if err := os.Chmod(source, 0044); err != nil {
		t.Fatal(err)
	}
	user, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", string(user[:len(user)-1])+" allow read", source).CombinedOutput(); err != nil {
		t.Fatalf("set ACL: %v %s", err, out)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(w.configDir, ".bak", "acl-conflict")
	t.Cleanup(func() { _ = os.Chmod(source, 0600); _ = os.Chmod(backup, 0600) })
	if _, err := Recover(w.db, w.configDir, "acl-conflict", false); err == nil || !strings.Contains(err.Error(), "inaccessible") {
		t.Fatalf("expected unreadable backup rejection: %v", err)
	}
	if got, err := os.ReadFile(original); err != nil || string(got) != "local" {
		t.Fatalf("local file: %q %v", got, err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup: %v", err)
	}
}

func TestRecoverRejectsOwnerUnreadableCopy(t *testing.T) {
	w := newWorld(t)
	original := w.write("acl-item", "payload")
	if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: original, Name: "acl-item"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(w.configDir, "acl-item")
	if err := os.Chmod(source, 0044); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(source, 0600)
		_ = os.Chmod(original, 0600)
		_ = os.Chmod(filepath.Join(w.configDir, ".bak", "acl-item"), 0600)
	})
	user, err := exec.Command("id", "-un").Output()
	if err != nil {
		t.Fatal(err)
	}
	acl := string(user[:len(user)-1]) + " allow read"
	if out, err := exec.Command("/bin/chmod", "+a", acl, source).CombinedOutput(); err != nil {
		t.Fatalf("set ACL: %v %s", err, out)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != "payload" {
		t.Fatalf("source must be readable: %q %v", got, err)
	}
	if _, err := Recover(w.db, w.configDir, "acl-item", false); err == nil || !strings.Contains(err.Error(), "inaccessible") {
		t.Fatalf("expected unreadable copy rejection: %v", err)
	}
	assertSymlink(t, original, source)
	if _, err := os.Lstat(filepath.Join(w.configDir, ".bak", "acl-item")); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup: %v", err)
	}
}
