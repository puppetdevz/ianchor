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

func TestTagRejectsInvalidVersionBeforeMutation(t *testing.T) {
	for _, version := range []string{"1. 2.3\n", "01.2.3\n", "1.2.3\nextra\n", "1.2.3-rc1\n", "1.2\n"} {
		t.Run(strings.TrimSpace(version), func(t *testing.T) {
			root := t.TempDir()
			copyFile(t, filepath.Join("..", "..", "scripts", "tag.sh"), filepath.Join(root, "scripts", "tag.sh"))
			if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte(version), 0o644); err != nil {
				t.Fatal(err)
			}
			run(t, root, "git", "init", "-q", "-b", "main")
			run(t, root, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-qm", "initial")
			before := run(t, root, "git", "rev-parse", "HEAD")
			cmd := exec.Command("bash", "scripts/tag.sh", "patch")
			cmd.Dir = root
			if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "invalid VERSION") {
				t.Fatalf("expected invalid version: %s, %v", out, err)
			}
			if got := run(t, root, "git", "rev-parse", "HEAD"); got != before {
				t.Fatal("invalid version changed HEAD")
			}
		})
	}
}

func TestTagAtomicPushRejectsConcurrentRewind(t *testing.T) {
	root := t.TempDir()
	repo, remote := filepath.Join(root, "repo"), filepath.Join(root, "remote.git")
	copyFile(t, filepath.Join("..", "..", "scripts", "tag.sh"), filepath.Join(repo, "scripts", "tag.sh"))
	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "git", "init", "--bare", "-q", remote)
	run(t, repo, "git", "init", "-q", "-b", "main")
	run(t, repo, "git", "config", "user.name", "Test")
	run(t, repo, "git", "config", "user.email", "test@example.com")
	run(t, repo, "git", "commit", "--allow-empty", "-qm", "ancestor")
	ancestor := strings.TrimSpace(run(t, repo, "git", "rev-parse", "HEAD"))
	run(t, repo, "git", "add", "VERSION", "scripts/tag.sh")
	run(t, repo, "git", "commit", "-qm", "initial")
	run(t, repo, "git", "remote", "add", "origin", remote)
	run(t, repo, "git", "push", "-q", "origin", "main")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/bash\nif [[ \" $* \" == *\" push --atomic \"* ]]; then\n  " + realGit + " --git-dir=\"" + remote + "\" update-ref refs/heads/main " + ancestor + "\nfi\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "scripts/tag.sh", "patch")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("stale atomic push succeeded: %s", out)
	}
	if got := strings.TrimSpace(run(t, root, "git", "--git-dir", remote, "rev-parse", "main")); got != ancestor {
		t.Fatalf("rewind overwritten: %s", got)
	}
	if status(t, root, "git", "--git-dir", remote, "show-ref", "--verify", "--quiet", "refs/tags/v1.0.1") {
		t.Fatal("tag pushed despite lease failure")
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
			original := strings.TrimSpace(run(t, repo, "git", "rev-parse", "HEAD"))
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
			if got := strings.TrimSpace(run(t, root, "git", "--git-dir", remote, "rev-parse", "refs/heads/main")); got != original {
				t.Fatalf("failed atomic push changed main: %s", got)
			}
			if status(t, root, "git", "--git-dir", remote, "show-ref", "--verify", "--quiet", "refs/tags/v1.0.1") {
				t.Fatal("failed atomic push created tag")
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
