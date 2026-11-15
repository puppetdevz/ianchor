package completions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The shell scripts are shipped byte-for-byte as they were in the Node
// implementation; these hashes lock the bytes so an accidental edit is caught.
const (
	bashSHA256 = "093b7db8d0d42bd94ef8bcdb42282e3761fd01c2e9b38e1d19d054ede7c3aaf4"
	zshSHA256  = "8a9bd5856677e279d0d2d0689fd97e49ac8d7d34007cc782261ca2d0a9d0db50"
)

func TestScriptBytes(t *testing.T) {
	for shell, want := range map[string]string{"bash": bashSHA256, "zsh": zshSHA256} {
		data, err := Script(shell)
		if err != nil {
			t.Fatalf("missing asset for %s: %v", shell, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s script changed: %s", shell, got)
		}
	}
}

func TestSupported(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		if !Supported(shell) {
			t.Fatalf("%s should be supported", shell)
		}
	}
	for _, shell := range []string{"fish", "", "ZSH", "sh"} {
		if Supported(shell) {
			t.Fatalf("%q should not be supported", shell)
		}
	}
	if UnsupportedError("fish").Error() != "Unsupported shell: fish. Expected zsh or bash." {
		t.Fatalf("message = %q", UnsupportedError("fish"))
	}
	if _, err := Script("fish"); err == nil {
		t.Fatal("unsupported shell must not return a script")
	}
}

func TestShellSyntax(t *testing.T) {
	dir := t.TempDir()
	for _, shell := range []string{"bash", "zsh"} {
		path := filepath.Join(dir, "ianchor."+shell)
		data, _ := Script(shell)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		binary, err := exec.LookPath(shell)
		if err != nil {
			continue
		}
		if out, err := exec.Command(binary, "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("%s syntax: %v\n%s", shell, err, out)
		}
	}
}

// TestZshRegistration checks that sourcing the script registers a completion
// for ianchor without requiring a terminal.
func TestZshRegistration(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh not available")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "ianchor.zsh")
	data, _ := Script("zsh")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	script := "autoload -Uz compinit && compinit -u -d " + filepath.Join(dir, "zcompdump") + " >/dev/null 2>&1\n" +
		"source " + path + "\n" +
		"print -r -- ${_comps[ianchor]}\n"
	cmd := exec.Command(zsh, "-f", "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("zsh: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "_ianchor" {
		t.Fatalf("registration = %q", out)
	}
}

// TestZshFunctionNames guards the helper names the zsh script relies on.
func TestZshFunctionNames(t *testing.T) {
	data, _ := Script("zsh")
	text := string(data)
	for _, needle := range []string{"_ianchor()", "#compdef ianchor", "__complete", "compadd", "compdef _ianchor ianchor"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("zsh script lost %q", needle)
		}
	}
}

// TestBashFunctionNames guards the helper names the bash script relies on.
func TestBashFunctionNames(t *testing.T) {
	data, _ := Script("bash")
	text := string(data)
	for _, needle := range []string{"_ianchor_complete()", "_ianchor_quote_replies", "COMP_WORDBREAKS", "complete -o nospace -F _ianchor_complete ianchor"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("bash script lost %q", needle)
		}
	}
}
