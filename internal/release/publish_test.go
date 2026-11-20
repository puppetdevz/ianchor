package release_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	assets     string
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
	w.assets = filepath.Join(root, "assets")
	if err := os.MkdirAll(w.assets, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "amd64"} {
		writeAsset(t, filepath.Join(w.assets, assetName(arch)), arch)
	}
	w.writeMock("curl", `#!/bin/bash
set -euo pipefail
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift 2 ;;
    --retry|--proto|--proto-redir) shift 2 ;;
    --fail|--location) shift ;;
    https://*) url="$1"; shift ;;
    *) exit 1 ;;
  esac
done
name="${url##*/}"
[ "$url" = "https://github.com/puppetdevz/ianchor/releases/download/v1.1.1/$name" ] || exit 1
if [ "${MOCK_BAD_DOWNLOAD:-}" = "$name" ]; then printf bad > "$out"; else cp "$MOCK_DIR/remote/$name" "$out"; fi
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
        status="${MOCK_GH_STATUS:-404}"
        [ ! -f "$MOCK_DIR/created" ] || status=200
        body='{"tag_name":"v1.1.1","draft":false,"prerelease":false,"id":42}'
        [ -z "${MOCK_GH_BODY:-}" ] || body="$MOCK_GH_BODY"
        printf 'HTTP/2.0 %s\r\n\r\n%s\n' "$status" "$body"
        printf '%s' "${MOCK_GH_STDERR:-}" >&2
        [ "$status" = 200 ] && exit 0
        exit 1
        ;;
      *"--paginate"*)
        ruby -rjson -e 'puts [Dir.children(ARGV[0]).sort.map { |n| {name: n, state: "uploaded"} }].to_json' "$MOCK_DIR/remote"
        ;;
      *) exit "${MOCK_GH_REPO_EXIT:-0}" ;;
    esac
    ;;
  release)
    case "$2" in
      create) touch "$MOCK_DIR/created" ;;
      upload)
        name="${4##*/}"
        if [ "${MOCK_UPLOAD_FAIL:-}" != "missing" ]; then cp "$4" "$MOCK_DIR/remote/$name"; fi
        if [ -n "${MOCK_UPLOAD_FAIL:-}" ]; then exit 1; fi
        ;;
    esac ;;
esac
exit 0
`)
	if err := os.MkdirAll(filepath.Join(w.mockDir, "remote"), 0o755); err != nil {
		t.Fatal(err)
	}
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
		"ASSET_DIR="+w.assets,
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

func assetName(arch string) string { return "ianchor_1.1.1_darwin_" + arch + ".tar.gz" }

// Construct a minimal structurally valid Mach-O fixture, not a runnable program.
func writeAsset(t *testing.T, path, arch string) {
	t.Helper()
	cpu := uint32(0x0100000c)
	if arch == "amd64" {
		cpu = 0x01000007
	}
	mach := make([]byte, 104)
	copy(mach, []byte{0xcf, 0xfa, 0xed, 0xfe})
	binary.LittleEndian.PutUint32(mach[4:], cpu)
	binary.LittleEndian.PutUint32(mach[12:], 2)
	binary.LittleEndian.PutUint32(mach[16:], 1)
	binary.LittleEndian.PutUint32(mach[20:], 72)
	binary.LittleEndian.PutUint32(mach[32:], 0x19)
	binary.LittleEndian.PutUint32(mach[36:], 72)
	binary.LittleEndian.PutUint64(mach[32+48:], uint64(len(mach)))
	binary.LittleEndian.PutUint32(mach[32+60:], 4)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "ianchor", Mode: 0755, Size: int64(len(mach)), Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(mach); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestFormulaInstallAndTestInIsolatedHarness(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	if r := w.publish(nil); r.code != 0 {
		t.Fatal(r.output)
	}
	root := t.TempDir()
	formula := filepath.Join(root, "formula.rb")
	if err := os.WriteFile(formula, []byte(w.tapFormula()), 0644); err != nil {
		t.Fatal(err)
	}
	// Stand-in is deliberately not the synthetic Mach-O archive fixture.
	if err := os.WriteFile(filepath.Join(root, "ianchor"), []byte("#!/bin/sh\ncase \"$*\" in *--version*) echo 1.1.1 ;; *zsh*) echo '#compdef ianchor' ;; *bash*) echo complete ;; esac\n"), 0755); err != nil {
		t.Fatal(err)
	}
	harness, err := filepath.Abs("testdata/formula_harness.rb")
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"arm64", "amd64"} {
		data, err := os.ReadFile(filepath.Join(w.assets, assetName(arch)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		url := "https://github.com/puppetdevz/ianchor/releases/download/v1.1.1/" + assetName(arch)
		cmd := exec.Command("ruby", harness, formula, filepath.Join(root, arch), url, fmt.Sprintf("%x", sum))
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "ARCH="+arch)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s harness: %v\n%s", arch, err, out)
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
		`version "1.1.1"`, `on_arm do`, `on_intel do`,
		`url "https://github.com/puppetdevz/ianchor/releases/download/v1.1.1/` + assetName("arm64") + `"`,
		`url "https://github.com/puppetdevz/ianchor/releases/download/v1.1.1/` + assetName("amd64") + `"`,
	} {
		if !strings.Contains(formula, want) {
			t.Fatalf("formula missing %q:\n%s", want, formula)
		}
	}
	for _, arch := range []string{"arm64", "amd64"} {
		data, err := os.ReadFile(filepath.Join(w.assets, assetName(arch)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if !strings.Contains(formula, fmt.Sprintf(`sha256 "%x"`, sum)) {
			t.Fatalf("missing %s checksum: %s", arch, formula)
		}
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
	if r := w.publish(map[string]string{"MOCK_GH_STATUS": "200", "MOCK_GH_BODY": `{"tag_name":"v1.1.1","draft":false,"prerelease":false,"id":42}`}); r.code != 0 {
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
		"MOCK_GH_BODY":   `{"tag_name":"v1.1.1","draft":false,"prerelease":false,"id":42}`,
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
	if r.code == 0 || !strings.Contains(r.output, "Invalid target checksum") {
		t.Fatalf("checksum = %+v", r)
	}
}

func TestPublishAssetSafety(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(*workspace)
		env        map[string]string
	}{
		{"missing", "Missing or invalid asset", func(w *workspace) {
			if err := os.Remove(filepath.Join(w.assets, assetName("arm64"))); err != nil {
				t.Fatal(err)
			}
		}, nil},
		{"wrong architecture", "Wrong executable architecture", func(w *workspace) { writeAsset(t, filepath.Join(w.assets, assetName("arm64")), "amd64") }, nil},
		{"existing different bytes", "Public release asset differs", func(w *workspace) {
			if err := os.WriteFile(filepath.Join(w.mockDir, "remote", assetName("amd64")), []byte("other"), 0644); err != nil {
				t.Fatal(err)
			}
		}, nil},
		{"download mismatch", "Public release asset differs", func(w *workspace) {}, map[string]string{"MOCK_BAD_DOWNLOAD": assetName("arm64")}},
		{"upload failure missing", "Release asset is still missing", func(w *workspace) {}, map[string]string{"MOCK_UPLOAD_FAIL": "missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkspace(t)
			w.commitTap("")
			defer w.assertTapUnchanged()()
			tc.setup(w)
			r := w.publish(tc.env)
			if r.code == 0 || !strings.Contains(r.output, tc.want) {
				t.Fatalf("result: %+v", r)
			}
			if tc.name == "existing different bytes" && strings.Contains(readFile(t, filepath.Join(w.mockDir, "gh.log")), "release upload") {
				t.Fatal("uploaded before checking all existing assets")
			}
		})
	}
}

func TestPublishUploadFailureAfterSuccess(t *testing.T) {
	w := newWorkspace(t)
	w.commitTap("")
	if r := w.publish(map[string]string{"MOCK_UPLOAD_FAIL": "after"}); r.code != 0 || !strings.Contains(r.output, "Upload failed") {
		t.Fatalf("result: %+v", r)
	}
	if w.tapFormula() == "" {
		t.Fatal("matching uploaded bytes did not publish formula")
	}
}

func TestPublishRejectsChangedBinaryIdentity(t *testing.T) {
	for _, arch := range []string{"arm64", "amd64"} {
		t.Run(arch, func(t *testing.T) {
			w := newWorkspace(t)
			w.commitTap("")
			if r := w.publish(nil); r.code != 0 {
				t.Fatal(r.output)
			}
			defer w.assertTapUnchanged()()
			path := filepath.Join(w.assets, assetName(arch))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// gzip header timestamp changes the archive identity without changing its contents.
			data[4] = 1
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			r := w.publish(nil)
			if r.code == 0 || !strings.Contains(r.output, "Refusing to change binary identity") {
				t.Fatalf("result: %+v", r)
			}
		})
	}
}

func TestPublishRejectsMaliciousStaticRuby(t *testing.T) {
	for _, payload := range []string{`system "touch", ENV["MOCK_DIR"] + "/pwned"`, "`touch $MOCK_DIR/pwned`", `x = File.write(ENV["MOCK_DIR"] + "/pwned", "x")`} {
		t.Run(payload, func(t *testing.T) {
			w := newWorkspace(t)
			w.commitTap("class Ianchor < Formula\n  " + payload + "\nend\n")
			defer w.assertTapUnchanged()()
			r := w.publish(nil)
			if r.code == 0 {
				t.Fatalf("malicious Ruby accepted: %+v", r)
			}
			if _, err := os.Stat(filepath.Join(w.mockDir, "pwned")); !os.IsNotExist(err) {
				t.Fatal("tap Ruby was executed")
			}
		})
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
