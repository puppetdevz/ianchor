package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTagResumeRejectsNonBumpCommit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join("..", "..", "scripts", "tag.sh"), filepath.Join(root, "scripts", "tag.sh"))
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "config", "user.name", "Test")
	run(t, root, "git", "config", "user.email", "test@example.com")
	run(t, root, "git", "add", "VERSION", "scripts/tag.sh")
	run(t, root, "git", "commit", "-qm", "unrelated")
	command := exec.Command("bash", "scripts/tag.sh", "resume")
	command.Dir = root
	if out, err := command.CombinedOutput(); err == nil || !strings.Contains(string(out), "HEAD is not the version bump commit") {
		t.Fatalf("expected refusal: %s, %v", out, err)
	}
}

func TestTagResumeAfterPushFailure(t *testing.T) {
	for _, rejectedRef := range []string{"refs/heads/main", "refs/tags/v1.0.1"} {
		t.Run(rejectedRef, func(t *testing.T) {
			root := t.TempDir()
			repo := filepath.Join(root, "repo")
			remote := filepath.Join(root, "remote.git")
			if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
				t.Fatal(err)
			}
			copyFile(t, filepath.Join("..", "..", "scripts", "tag.sh"), filepath.Join(repo, "scripts", "tag.sh"))
			if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run(t, root, "git", "init", "--bare", "-q", remote)
			run(t, repo, "git", "init", "-q", "-b", "main")
			run(t, repo, "git", "config", "user.name", "Test")
			run(t, repo, "git", "config", "user.email", "test@example.com")
			run(t, repo, "git", "add", "VERSION", "scripts/tag.sh")
			run(t, repo, "git", "commit", "-qm", "initial")
			run(t, repo, "git", "remote", "add", "origin", remote)
			run(t, repo, "git", "push", "-q", "origin", "main")
			hook := filepath.Join(remote, "hooks", "pre-receive")
			script := "#!/bin/sh\nwhile read old new ref; do [ \"$ref\" = " + rejectedRef + " ] && exit 1; done\nexit 0\n"
			if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", "scripts/tag.sh", "patch")
			command.Dir = repo
			if out, err := command.CombinedOutput(); err == nil || !strings.Contains(string(out), "./scripts/tag.sh resume") {
				t.Fatalf("expected failed push and recovery instructions: %s, %v", out, err)
			}
			if err := os.Remove(hook); err != nil {
				t.Fatal(err)
			}
			run(t, repo, "bash", "scripts/tag.sh", "resume")
			if got := strings.TrimSpace(run(t, repo, "git", "show", "origin/main:VERSION")); got != "1.0.1" {
				t.Fatalf("version = %q", got)
			}
			if got, want := strings.TrimSpace(run(t, root, "git", "--git-dir", remote, "rev-parse", "refs/tags/v1.0.1")), strings.TrimSpace(run(t, repo, "git", "rev-parse", "HEAD")); got != want {
				t.Fatalf("remote tag=%s HEAD=%s", got, want)
			}
		})
	}
}
