package release_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests drive scripts/release/publish.sh against an isolated bare tap
// repository and mocked GitHub/network tooling, so the release contract is
// verified without touching any real remote.

type workspace struct {
	t          *testing.T
	root       string // copy of the repository files publish.sh needs
	bin        string // mocked tooling
	tapBare    string // bare repository used as the tap origin
	tapWork    string // working clone used by tests to inspect the tap
	mockDir    string // directory holding mock state files
	sourceBare string
	commit     string
}

func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	root := t.TempDir()
	repoSource, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(repoSource, "Formula/ianchor.rb"), filepath.Join(root, "Formula/ianchor.rb"))
	// This isolated release fixture uses a fixed version and tag, independent
	// of the product version in the source checkout.
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scripts, err := os.ReadDir(filepath.Join(repoSource, "scripts", "release"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range scripts {
		rel := filepath.Join("scripts", "release", entry.Name())
		copyFile(t, filepath.Join(repoSource, rel), filepath.Join(root, rel))
	}

	w := &workspace{t: t, root: root, bin: filepath.Join(root, "bin"), mockDir: filepath.Join(root, "mock")}
	if err := os.MkdirAll(w.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.mockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.writeMock("curl", `#!/bin/bash
set -euo pipefail
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift 2 ;;
    --retry) shift 2 ;;
    --fail|--location) shift ;;
    https://*) url="$1"; shift ;;
    *) exit 1 ;;
  esac
done
[ "$url" = "https://github.com/puppetdevz/ianchor/archive/$EXPECTED_COMMIT.tar.gz" ] || exit 1
if [ -n "${MOCK_SOURCE_ARCHIVE:-}" ]; then
  cp "$MOCK_SOURCE_ARCHIVE" "$out"
else
  printf 'fake source archive' > "$out"
fi
`)
	w.writeMock("gh", `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$MOCK_DIR/gh.log"
if [ "${MOCK_GH_ADVANCE_TAP:-}" = "1" ]; then
  git -C "$MOCK_TAP_WORK" -c user.name=t -c user.email=t@example.com commit --allow-empty -q -m advance
  git -C "$MOCK_TAP_WORK" push -q origin main
fi
case "$1" in
  api)
    case "$*" in
      *"--include"*)
        printf 'HTTP/2.0 %s\r\n\r\n%s\n' "${MOCK_GH_STATUS:-404}" "${MOCK_GH_BODY:-{\}}"
        printf '%s' "${MOCK_GH_STDERR:-}" >&2
        [ "${MOCK_GH_STATUS:-404}" = "200" ] && exit 0
        exit 1
        ;;
      *) exit "${MOCK_GH_REPO_EXIT:-0}" ;;
    esac
    ;;
  release) exit 0 ;;
esac
exit 0
`)
	run(t, root, "git", "init", "-q", "-b", "main")
	run(t, root, "git", "add", "Formula/ianchor.rb", "VERSION", "scripts/release")
	run(t, root, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "source")
	w.commit = strings.TrimSpace(run(t, root, "git", "rev-parse", "HEAD"))
	run(t, root, "git", "tag", "v1.1.1")
	w.sourceBare = filepath.Join(root, "source.git")
	run(t, root, "git", "init", "-q", "--bare", w.sourceBare)
	run(t, root, "git", "push", "-q", w.sourceBare, "main", "refs/tags/v1.1.1")
	w.tapBare = filepath.Join(root, "tap.git")
	w.tapWork = filepath.Join(root, "tap-work")
	run(t, root, "git", "init", "-q", "--bare", "-b", "main", w.tapBare)
	run(t, root, "git", "clone", "-q", w.tapBare, w.tapWork)
	return w
}

func (w *workspace) writeMock(name, body string) {
	w.t.Helper()
	path := filepath.Join(w.bin, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

// commitTap publishes Formula content (empty means "no Formula directory") to
// the tap main branch.
func (w *workspace) commitTap(formula string) {
	w.t.Helper()
	if formula != "" {
		if err := os.MkdirAll(filepath.Join(w.tapWork, "Formula"), 0o755); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(w.tapWork, "Formula", "ianchor.rb"), []byte(formula), 0o644); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(w.tapWork, "README.md"), []byte("tap\n"), 0o644); err != nil {
		w.t.Fatal(err)
	}
	run(w.t, w.tapWork, "git", "add", "-A")
	if !status(w.t, w.tapWork, "git", "diff", "--cached", "--quiet") {
		run(w.t, w.tapWork, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "seed")
	}
	run(w.t, w.tapWork, "git", "push", "-q", "origin", "main")
}

func (w *workspace) tapFormula() string {
	w.t.Helper()
	cmd := exec.Command("git", "-C", w.tapBare, "show", "main:Formula/ianchor.rb")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func (w *workspace) tapCommits() int {
	w.t.Helper()
	out := run(w.t, w.tapBare, "git", "rev-list", "--count", "main")
	var count int
	if _, err := fmt.Sscan(strings.TrimSpace(out), &count); err != nil {
		w.t.Fatal(err)
	}
	return count
}

// Require the entire tap commit (and therefore its complete tree and modes)
// to remain identical after a rejected publish. Call only after seeding main.
func (w *workspace) assertTapUnchanged() func() {
	w.t.Helper()
	before := strings.TrimSpace(run(w.t, w.tapBare, "git", "rev-parse", "refs/heads/main"))
	return func() {
		w.t.Helper()
		after := strings.TrimSpace(run(w.t, w.tapBare, "git", "rev-parse", "refs/heads/main"))
		if after != before {
			w.t.Errorf("rejected publish changed tap HEAD: %s -> %s", before, after)
		}
	}
}

type publishResult struct {
	output string
	code   int
}

func (w *workspace) publish(env map[string]string) publishResult {
	w.t.Helper()
	cmd := exec.Command("bash", filepath.Join(w.root, "scripts", "release", "publish.sh"))
	cmd.Dir = w.root
	cmd.Env = append(os.Environ(),
		"PATH="+w.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+filepath.Join(w.root, "home"),
		"MOCK_DIR="+w.mockDir,
		"MOCK_TAP_WORK="+w.tapWork,
		"IANCHOR_TAP_URL=file://"+w.tapBare,
		"IANCHOR_SOURCE_URL=file://"+w.sourceBare,
		"EXPECTED_COMMIT="+w.commit,
		"IANCHOR_REPO=puppetdevz/ianchor",
		"TAG=v1.1.1",
		"SOURCE_TOKEN=source-token",
		"TAP_TOKEN=tap-token",
	)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			w.t.Fatalf("run publish: %v", err)
		}
	}
	return publishResult{output: string(out), code: code}
}

func TestFormulaInstallAndTestInIsolatedHarness(t *testing.T) {
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkspace(t)
	// Put the actual Go source in the isolated tagged commit. The Formula
	// must build its immutable source archive, not the live working directory.
	for _, rel := range []string{"go.mod", "go.sum", "version.go"} {
		copyFile(t, filepath.Join(source, rel), filepath.Join(w.root, rel))
	}
	for _, rel := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(source, rel), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			copyFile(t, path, filepath.Join(w.root, relative))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	run(t, w.root, "git", "add", "go.mod", "go.sum", "version.go", "cmd", "internal")
	run(t, w.root, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "build source")
	w.commit = strings.TrimSpace(run(t, w.root, "git", "rev-parse", "HEAD"))
	run(t, w.root, "git", "tag", "-f", "v1.1.1")
	run(t, w.root, "git", "push", "-q", "--force", w.sourceBare, "main", "refs/tags/v1.1.1")
	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	run(t, w.root, "git", "archive", "--format=tar.gz", "--prefix=ianchor/", "--output="+archive, w.commit)
	w.commitTap("")
	if result := w.publish(map[string]string{"MOCK_SOURCE_ARCHIVE": archive}); result.code != 0 {
		t.Fatalf("render formula: %s", result.output)
	}
	formula := w.tapFormula()
	archiveBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(formula, fmt.Sprintf("%x", sha256.Sum256(archiveBytes))) || !strings.Contains(formula, w.commit) {
		t.Fatal("Formula does not identify the exact immutable archive")
	}
	root := t.TempDir()
	run(t, root, "tar", "-xzf", archive)
	buildSource := filepath.Join(root, "ianchor")
	formulaPath := filepath.Join(root, "ianchor.rb")
	if err := os.WriteFile(formulaPath, []byte(formula), 0600); err != nil {
		t.Fatal(err)
	}
	// The harness changes HOME, so preserve the caller's module cache instead
	// of defaulting to an empty cache under the temporary home.
	moduleCache := strings.TrimSpace(run(t, source, "go", "env", "GOMODCACHE"))
	if moduleCache == "" {
		t.Fatal("Go module cache path is empty")
	}
	harnessEnv := append(os.Environ(), "HOME="+root, "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOMODCACHE="+moduleCache)
	// First exercise the actual Formula install from the unmodified archive;
	// its installed binary's version and completion generation are checked.
	exact := exec.Command("ruby", filepath.Join(source, "internal/release/testdata/formula_harness.rb"), formulaPath, filepath.Join(root, "exact"), "install-only")
	exact.Dir = buildSource
	exact.Env = harnessEnv
	if out, err := exact.CombinedOutput(); err != nil {
		t.Fatalf("exact archive Formula install: %v\n%s", err, out)
	}
	// Relocate the coordinator only in this extracted test copy so the
	// stateful roundtrip cannot touch the user's shared lock. The archive and
	// rendered checksum above remain unchanged.
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	coordinatorSource := filepath.Join(buildSource, "internal", "coordinator", "coordinator.go")
	content, err := os.ReadFile(coordinatorSource)
	if err != nil {
		t.Fatal(err)
	}
	const originalParent = `"/Users/Shared"`
	if strings.Count(string(content), originalParent) != 1 {
		t.Fatal("unexpected coordinator source")
	}
	isolated := strings.Replace(string(content), originalParent, strconv.Quote(shared), 1)
	if err := os.WriteFile(coordinatorSource, []byte(isolated), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ruby", filepath.Join(source, "internal/release/testdata/formula_harness.rb"), formulaPath, root)
	cmd.Dir = buildSource
	cmd.Env = harnessEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated Formula harness: %v\n%s", err, out)
	}
	wrong := strings.Replace(formula, `version "1.1.1"`, `version "9.9.9"`, 1)
	if wrong == formula {
		t.Fatal("rendered Formula has unexpected version")
	}
	badPath := filepath.Join(root, "bad.rb")
	if err := os.WriteFile(badPath, []byte(wrong), 0600); err != nil {
		t.Fatal(err)
	}
	bad := exec.Command("ruby", filepath.Join(source, "internal/release/testdata/formula_harness.rb"), badPath, filepath.Join(root, "bad"))
	bad.Dir = buildSource
	bad.Env = harnessEnv
	if output, err := bad.CombinedOutput(); err == nil || !strings.Contains(string(output), "9.9.9") {
		t.Fatalf("wrong Formula version not rejected: %v\n%s", err, output)
	}
	missingFetch := exec.Command("ruby", filepath.Join(source, "internal/release/testdata/formula_harness.rb"), formulaPath, filepath.Join(root, "missing-fetch"), "skip-fetch")
	missingFetch.Dir = buildSource
	missingFetch.Env = harnessEnv
	if output, err := missingFetch.CombinedOutput(); err == nil || !strings.Contains(string(output), "requires Homebrew 6.0.20 or newer") {
		t.Fatalf("missing fetch phase not rejected: %v\n%s", err, output)
	}
	for _, shell := range []string{"bash", "zsh"} {
		want, err := os.ReadFile(filepath.Join(source, "internal/completions/assets/ianchor."+shell))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, "completions", shell))
		if err != nil || string(got) != string(want) {
			t.Fatalf("%s completion mismatch: %v", shell, err)
		}
	}
}

func TestPublishSupportsAnnotatedTag(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	run(t, w.root, "git", "tag", "-d", "v1.1.1")
	run(t, w.root, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "tag", "-a", "v1.1.1", "-m", "release")
	run(t, w.root, "git", "push", "-q", "--force", w.sourceBare, "refs/tags/v1.1.1")
	if r := w.publish(nil); r.code != 0 {
		t.Fatalf("annotated tag failed: %s", r.output)
	}
}

func TestPublishRejectsSameVersionDifferentSource(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap(`class Ianchor < Formula
  url "https://github.com/puppetdevz/ianchor/archive/refs/tags/v1.1.1.tar.gz"
  version "1.1.1"
  sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
end
`)
	defer w.assertTapUnchanged()()
	r := w.publish(nil)
	if r.code == 0 || !strings.Contains(r.output, "Refusing to change source identity") {
		t.Fatalf("same version accepted: %+v", r)
	}
}

func TestPublishRejectsMovedRemoteTag(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	defer w.assertTapUnchanged()()
	run(t, w.root, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-q", "-m", "different")
	run(t, w.root, "git", "tag", "-f", "v1.1.1")
	run(t, w.root, "git", "push", "-q", "--force", w.sourceBare, "refs/tags/v1.1.1")
	run(t, w.root, "git", "checkout", "-q", w.commit)
	run(t, w.root, "git", "tag", "-f", "v1.1.1", w.commit)
	r := w.publish(nil)
	if r.code == 0 {
		t.Fatal("published a moved remote tag")
	}
	if w.tapFormula() != "" {
		t.Fatal("published despite remote tag mismatch")
	}
}

func TestPublishRejectsSourceTagPointingElsewhere(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	defer w.assertTapUnchanged()()
	run(t, w.root, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-q", "-m", "different")
	r := w.publish(nil)
	if r.code == 0 {
		t.Fatal("published a tag that does not match the verified checkout")
	}
	if w.tapFormula() != "" {
		t.Fatal("published despite source mismatch")
	}
}

func TestWorkflowInstallsGoAndDoesNotExecuteCrossBuilds(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if !strings.Contains(workflow, "actions/setup-go@") || !strings.Contains(workflow, "go-version-file: go.mod") {
		t.Fatal("release workflow must install the required Go toolchain")
	}
	if strings.Contains(workflow, `"/tmp/ianchor-$arch" --version`) {
		t.Fatal("release workflow executes potentially non-native binaries")
	}
}

func TestWorkflowTestsCleanCheckoutAndPublishesFreshCheckout(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	verify := strings.Index(workflow, "- name: Verify sources")
	publish := strings.Index(workflow, "- name: Publish release and formula")
	fresh := strings.LastIndex(workflow, "uses: actions/checkout@")
	if verify < 0 || publish < 0 || fresh < verify || fresh > publish {
		t.Fatal("publishing requires a fresh checkout after verification")
	}
	if strings.Count(workflow, "git status --porcelain --untracked-files=all --ignored") < 2 {
		t.Fatal("verification must reject both untracked and ignored files before and after tests")
	}
	if !strings.Contains(workflow[fresh:publish], "ref: ${{ github.sha }}") {
		t.Fatal("publish checkout is not pinned to the event commit")
	}
}

func TestPublishCreatesFormula(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	before := w.tapCommits()
	r := w.publish(nil)
	if r.code != 0 {
		t.Fatalf("publish failed: %s", r.output)
	}
	formula := w.tapFormula()
	for _, want := range []string{
		`version "1.1.1"`,
		`url "https://github.com/puppetdevz/ianchor/archive/` + w.commit + `.tar.gz"`,
		`depends_on "go" => :build`,
		`sha256 "`,
	} {
		if !strings.Contains(formula, want) {
			t.Fatalf("formula missing %q:\n%s", want, formula)
		}
	}
	sum := sha256.Sum256([]byte("fake source archive"))
	if !strings.Contains(formula, fmt.Sprintf(`sha256 "%x"`, sum)) {
		t.Fatalf("formula checksum does not match downloaded bytes: %s", formula)
	}
	if strings.Contains(formula, "__IANCHOR_") {
		t.Fatalf("unresolved placeholders:\n%s", formula)
	}
	if got := w.tapCommits(); got != before+1 {
		t.Fatalf("commits = %d, want %d", got, before+1)
	}
	if got := run(t, w.tapBare, "git", "log", "-1", "--format=%s", "main"); strings.TrimSpace(got) != "chore: update formula for v1.1.1" {
		t.Fatalf("commit subject = %q", got)
	}
}

func TestPublishLeaseRejectsConcurrentRewind(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	ancestor := strings.TrimSpace(run(t, w.tapBare, "git", "rev-parse", "main"))
	run(t, w.tapWork, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-qm", "advance")
	run(t, w.tapWork, "git", "push", "-q", "origin", "main")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	w.writeMock("git", "#!/bin/bash\nif [[ \" $* \" == *\" --force-with-lease=\"* ]]; then\n  "+realGit+" --git-dir=\""+w.tapBare+"\" update-ref refs/heads/main "+ancestor+"\nfi\nexec "+realGit+" \"$@\"\n")
	if r := w.publish(nil); r.code == 0 {
		t.Fatalf("stale update succeeded: %s", r.output)
	}
	if got := strings.TrimSpace(run(t, w.tapBare, "git", "rev-parse", "main")); got != ancestor {
		t.Fatalf("concurrent rewind overwritten: %s", got)
	}
}

func TestPublishRetryAfterTapPushFailure(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	w.writeMock("git", "#!/bin/bash\nif [[ \" $* \" == *\" push \"* ]] && [ ! -e \"$MOCK_DIR/push-failed\" ]; then touch \"$MOCK_DIR/push-failed\"; exit 1; fi\nexec "+realGit+" \"$@\"\n")
	if r := w.publish(nil); r.code == 0 {
		t.Fatal("first push unexpectedly succeeded")
	}
	if w.tapFormula() != "" {
		t.Fatal("failed push updated tap")
	}
	if r := w.publish(map[string]string{"MOCK_GH_STATUS": "200", "MOCK_GH_BODY": `{"tag_name":"v1.1.1","draft":false,"prerelease":false}`}); r.code != 0 {
		t.Fatalf("retry failed: %s", r.output)
	}
	if w.tapFormula() == "" {
		t.Fatal("retry did not update tap")
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	if r := w.publish(nil); r.code != 0 {
		t.Fatalf("first publish failed: %s", r.output)
	}
	commits := w.tapCommits()
	r := w.publish(nil)
	if r.code != 0 {
		t.Fatalf("second publish failed: %s", r.output)
	}
	if !strings.Contains(r.output, "Formula is unchanged") {
		t.Fatalf("output = %q", r.output)
	}
	if got := w.tapCommits(); got != commits {
		t.Fatalf("commits = %d, want %d", got, commits)
	}
}

func TestPublishRejectsUnstableAndMismatchedTags(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	defer w.assertTapUnchanged()()
	for _, tag := range []string{"v1.1", "v1.1.1-rc1", "1.1.1", "v01.1.1", "v1.1.1.1", "v"} {
		r := w.publish(map[string]string{"TAG": tag})
		if r.code == 0 {
			t.Fatalf("tag %q was accepted", tag)
		}
		if !strings.Contains(r.output, "Only stable vMAJOR.MINOR.PATCH tags are supported") {
			t.Fatalf("tag %q output = %q", tag, r.output)
		}
	}
	r := w.publish(map[string]string{"TAG": "v1.1.2"})
	if r.code == 0 || !strings.Contains(r.output, "Tag/version mismatch") {
		t.Fatalf("mismatched tag = %+v", r)
	}
	if w.tapFormula() != "" {
		t.Fatal("a rejected tag must not publish")
	}
}

func TestPublishHistoricalReleaseSkipsDowngrade(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap(`class Ianchor < Formula
  desc "x"
  homepage "https://github.com/puppetdevz/ianchor"
  url "https://github.com/puppetdevz/ianchor/archive/refs/tags/v2.0.0.tar.gz"
  version "2.0.0"
  sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  license "MIT"

  depends_on "go" => :build

  def install
    bin.install "ianchor"
  end
end
`)
	defer w.assertTapUnchanged()()
	r := w.publish(nil)
	if r.code != 0 || !strings.Contains(r.output, "Skipping tap downgrade from 2.0.0 to 1.1.1") {
		t.Fatalf("historical release = %+v", r)
	}
	log, err := os.ReadFile(filepath.Join(w.mockDir, "gh.log"))
	if err != nil || !strings.Contains(string(log), "release create v1.1.1 --repo puppetdevz/ianchor --verify-tag --title v1.1.1 --notes Release v1.1.1 --latest=false") {
		t.Fatalf("historical release must not become latest: %s, %v", log, err)
	}
}

func TestFormulaMetadataRejectsClassLevelCalls(t *testing.T) {
	metadata, err := filepath.Abs("../../scripts/release/metadata.rb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		call string
		want bool
	}{
		{`deny_network_access!`, true},
		{`deny_network_access!()`, false},
		{`deny_network_access!(:build)`, false},
		{`arbitrary_call()`, false},
		{`deny_network_access! { system "false" }`, false},
	} {
		t.Run(tc.call, func(t *testing.T) {
			cmd := exec.Command("ruby", metadata)
			cmd.Stdin = strings.NewReader("class Ianchor < Formula\n  " + tc.call + "\nend\n")
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.want {
				t.Fatalf("metadata validation: %v, output: %s", err, out)
			}
		})
	}
}

func TestPublishRejectsDynamicTapFormula(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap(`class Ianchor < Formula
  desc "x"
  homepage "https://github.com/puppetdevz/ianchor"
  url "https://github.com/puppetdevz/ianchor/archive/refs/tags/v1.0.0.tar.gz"
  sha256 "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  license "MIT"

  def version
    "1.0.0"
  end
end
`)
	defer w.assertTapUnchanged()()
	r := w.publish(nil)
	if r.code == 0 {
		t.Fatalf("dynamic formula accepted: %s", r.output)
	}
	if !strings.Contains(r.output, "Cannot override Formula metadata methods") {
		t.Fatalf("output = %q", r.output)
	}
}

func TestPublishRejectsSymlinkedFormula(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	target := filepath.Join(w.tapWork, "elsewhere.rb")
	if err := os.WriteFile(target, []byte("class Ianchor < Formula\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(w.tapWork, "Formula"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(w.tapWork, "Formula", "ianchor.rb")); err != nil {
		t.Fatal(err)
	}
	run(t, w.tapWork, "git", "add", "-A")
	run(t, w.tapWork, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "symlink")
	run(t, w.tapWork, "git", "push", "-q", "origin", "main")

	defer w.assertTapUnchanged()()
	r := w.publish(nil)
	if r.code == 0 || !strings.Contains(r.output, "must not be symbolic links") {
		t.Fatalf("symlink = %+v", r)
	}
}

func TestPublishRejectsSymlinkedFormulaDirectory(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	if err := os.Symlink("../outside", filepath.Join(w.tapWork, "Formula")); err != nil {
		t.Fatal(err)
	}
	run(t, w.tapWork, "git", "add", "Formula")
	run(t, w.tapWork, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "symlink directory")
	run(t, w.tapWork, "git", "push", "-q", "origin", "main")
	defer w.assertTapUnchanged()()
	r := w.publish(nil)
	if r.code == 0 || !strings.Contains(r.output, "must not be symbolic links") {
		t.Fatalf("directory symlink = %+v", r)
	}
}

func TestPublishRefusesMovedTapMain(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	r := w.publish(map[string]string{"MOCK_GH_ADVANCE_TAP": "1"})
	if r.code == 0 || !strings.Contains(r.output, "Tap main changed during preparation") {
		t.Fatalf("moved tap = %+v", r)
	}
}

func TestPublishSurvivesExistingRelease(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	r := w.publish(map[string]string{
		"MOCK_GH_STATUS": "200",
		"MOCK_GH_BODY":   `{"tag_name":"v1.1.1","draft":false,"prerelease":false}`,
	})
	if r.code != 0 {
		t.Fatalf("publish failed: %s", r.output)
	}
	if !strings.Contains(r.output, "Release v1.1.1 already exists") {
		t.Fatalf("output = %q", r.output)
	}
	if strings.Contains(strings.TrimSpace(readFile(t, filepath.Join(w.mockDir, "gh.log"))), "release create") {
		t.Fatal("existing Release must not be recreated")
	}
	if w.tapFormula() == "" {
		t.Fatal("formula was not published")
	}
}

func TestPublishRejectsNonFinalExistingReleases(t *testing.T) {
	for _, body := range []string{`{"tag_name":"v1.1.1","draft":true,"prerelease":false}`, `{"tag_name":"v1.1.1","draft":false,"prerelease":true}`, `{"tag_name":"v1.1.1"}`} {
		t.Run(body, func(t *testing.T) {
			w := newWorkspace(t)
			w.commitTap("")
			defer w.assertTapUnchanged()()
			r := w.publish(map[string]string{"MOCK_GH_STATUS": "200", "MOCK_GH_BODY": body})
			if r.code == 0 || w.tapFormula() != "" {
				t.Fatalf("non-final release accepted: %+v", r)
			}
		})
	}
}

func TestPublishClassifiesApiFailureWithoutLeaking(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	defer w.assertTapUnchanged()()
	r := w.publish(map[string]string{
		"MOCK_GH_STATUS": "401",
		"MOCK_GH_STDERR": "SECRET-TOKEN-LEAK",
	})
	if r.code == 0 {
		t.Fatal("API failure was ignored")
	}
	if !strings.Contains(r.output, "::error::Release query failed (authentication/permission") {
		t.Fatalf("output = %q", r.output)
	}
	if strings.Contains(r.output, "SECRET-TOKEN-LEAK") {
		t.Fatalf("stderr leaked: %q", r.output)
	}
	if w.tapFormula() != "" {
		t.Fatal("API failure must not publish")
	}
}

func TestPublishClassifiesAdditionalApiErrorsWithoutLeaks(t *testing.T) {
	for _, statusCode := range []string{"403", "429", "500"} {
		t.Run(statusCode, func(t *testing.T) {
			w := newWorkspace(t)
			w.commitTap("")
			before := w.tapCommits()
			r := w.publish(map[string]string{"MOCK_GH_STATUS": statusCode, "MOCK_GH_STDERR": "SECRET-TOKEN-LEAK", "MOCK_GH_BODY": `{"message":"SECRET-TOKEN-LEAK"}`})
			if r.code == 0 || !strings.Contains(r.output, "Release query failed") || strings.Contains(r.output, "SECRET-TOKEN-LEAK") {
				t.Fatalf("unsafe API result: %+v", r)
			}
			if w.tapCommits() != before || w.tapFormula() != "" {
				t.Fatal("API failure modified tap")
			}
		})
	}
}

func TestPublishRequiresCredentials(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	defer w.assertTapUnchanged()()
	r := w.publish(map[string]string{"TAP_TOKEN": ""})
	if r.code == 0 || !strings.Contains(r.output, "Missing HOMEBREW_TAP_TOKEN") {
		t.Fatalf("missing token = %+v", r)
	}
}

func TestRenderRejectsInvalidChecksum(t *testing.T) {
	w := newWorkspace(t)
	w.writeMock("shasum", "#!/bin/bash\necho not-a-checksum\n")
	w.commitTap("")
	defer w.assertTapUnchanged()()
	r := w.publish(nil)
	if r.code == 0 || !strings.Contains(r.output, "Invalid archive SHA256") {
		t.Fatalf("checksum = %+v", r)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func copyFile(t *testing.T, source, target string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func status(t *testing.T, dir, name string, args ...string) bool {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Run() == nil
}
