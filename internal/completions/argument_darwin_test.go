//go:build darwin

package completions

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Migrated from the Node completion matrix: real Tab through Readline/ZLE,
// including flag/value handling and the three supported zsh load mechanisms.
func TestInteractiveArgumentBoundaries(t *testing.T) {
	for _, mode := range []string{"bash", "zsh-source", "zsh-eval", "zsh-autoload"} {
		t.Run(mode, func(t *testing.T) {
			argv := []string{"/bin/bash", "--norc", "--noprofile", "-i"}
			shell := "bash"
			if strings.HasPrefix(mode, "zsh") {
				shell = "zsh"
				argv = []string{"zsh", "-f", "-i"}
			}
			s := startShell(t, argv)
			asset := scriptPath(t, shell)
			if shell == "zsh" {
				if mode == "zsh-autoload" {
					site := t.TempDir()
					data, _ := Script("zsh")
					if err := os.WriteFile(filepath.Join(site, "_ianchor"), data, 0600); err != nil {
						t.Fatal(err)
					}
					s.write("fpath=(" + quoteShellPath(site) + " $fpath)\n")
					s.expect(wantCompletePrompt)
				}
				s.write("autoload -Uz compinit; compinit -D -u >/dev/null 2>&1; bindkey -e\n")
				s.expect(wantCompletePrompt)
			}
			switch mode {
			case "zsh-eval":
				s.write("eval \"$(< " + quoteShellPath(asset) + ")\"\n")
			case "zsh-autoload":
				s.write("true\n")
			default:
				s.write("source " + quoteShellPath(asset) + "\n")
			}
			s.expect(wantCompletePrompt)
			if err := os.WriteFile(filepath.Join(s.cmd.Dir, "sample.txt"), []byte("sample"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(s.cmd.Dir, "folder name"), 0700); err != nil {
				t.Fatal(err)
			}
			// A tracked name matching a directory must remain a name without '/'.
			if err := os.Mkdir(filepath.Join(s.cmd.Dir, "zshrc"), 0700); err != nil {
				t.Fatal(err)
			}
			cases := []struct{ input, want string }{
				{"ianchor ini", "ianchor|init"},
				{"ianchor recover --force zs", "ianchor|recover|--force|zshrc"},
				{"ianchor remove -y zs", "ianchor|remove|-y|zshrc"},
				{"ianchor recover -- --h", "ianchor|recover|--|--help"},
				{"ianchor recover -- -n", "ianchor|recover|--|-n"},
				{"ianchor recover zshrc app", "ianchor|recover|zshrc|app"},
				{"ianchor add --name zs", "ianchor|add|--name|zs"},
				{"ianchor add --name chosen sam", "ianchor|add|--name|chosen|sample.txt"},
				{"ianchor list --j", "ianchor|list|--json"},
				{"ianchor add sam", "ianchor|add|sample.txt"},
				{"ianchor add fol", "ianchor|add|folder name/"},
				{"ianchor recover colon:a", "ianchor|recover|colon:a:b"},
				{`ianchor recover colon\:a:`, "ianchor|recover|colon:a:b"},
				{"ianchor recover line", "ianchor|recover|line"},
			}
			for _, tc := range cases {
				want := tc.want
				if shell == "zsh" && tc.input == "ianchor add fol" {
					// ZLE removes its automatic slash when the following Ctrl-A
					// edits the buffer. Check the raw Tab buffer separately below.
					want = "ianchor|add|folder name"
				}
				if got := strings.Join(s.lineAfterCompletion(tc.input), "|"); got != want {
					t.Fatalf("%s: got %q want %q", tc.input, got, want)
				}
			}
			if shell == "zsh" {
				capture := filepath.Join(t.TempDir(), "raw-buffer")
				s.write("_capture_directory() { zle complete-word; print -rn -- \"$BUFFER\" > " + quoteShellPath(capture) + "; zle -M DIRECTORY-CAPTURED; }; zle -N _capture_directory; bindkey '^I' _capture_directory\n")
				s.expect(wantCompletePrompt)
				s.write("ianchor add fol\t")
				s.expect("DIRECTORY-CAPTURED")
				data, err := os.ReadFile(capture)
				if err != nil || string(data) != `ianchor add folder\ name/` {
					t.Fatalf("raw directory Tab buffer %q: %v", data, err)
				}
			}
		})
	}
}

func TestBashWordBreakAndPositionalProtocol(t *testing.T) {
	for _, shell := range []string{"/bin/bash", "bash"} {
		t.Run(shell, func(t *testing.T) {
			asset := scriptPath(t, "bash")
			cases := []struct {
				words []string
				want  []string
			}{
				{[]string{"ianchor", "recover", "zs"}, []string{"zshrc "}},
				{[]string{"ianchor", "recover", "--force", "zs"}, []string{"zshrc "}},
				{[]string{"ianchor", "remove", "-y", "zs"}, []string{"zshrc "}},
				{[]string{"ianchor", "recover", "--", "--h"}, []string{"--help "}},
				{[]string{"ianchor", "recover", "--", "-n"}, []string{"-n "}},
				{[]string{"ianchor", "recover", "zshrc", ""}, nil},
				{[]string{"ianchor", "add", "--name", "zs"}, nil},
				{[]string{"ianchor", "recover", "colon", ":", "a"}, []string{"a:b "}},
				{[]string{"ianchor", "recover", "colon", ":", "a", ":", ""}, []string{"b "}},
				{[]string{"ianchor", "recover", `colon\:a`}, []string{`colon:a:b `}},
			}
			for _, tc := range cases {
				var quoted []string
				for _, word := range tc.words {
					quoted = append(quoted, quoteShellPath(word))
				}
				script := "before=$COMP_WORDBREAKS\nsource " + quoteShellPath(asset) + "\nsource " + quoteShellPath(asset) + "\n[[ \"$before\" == \"$COMP_WORDBREAKS\" ]] || exit 91\n" +
					"COMP_WORDS=(" + strings.Join(quoted, " ") + ")\n" + fmt.Sprintf("COMP_CWORD=%d\n", len(tc.words)-1) +
					"_ianchor_complete\n[[ \"$before\" == \"$COMP_WORDBREAKS\" ]] || exit 92\nfor reply in \"${COMPREPLY[@]}\"; do printf '%s\\0' \"$reply\"; done\n"
				cmd := exec.Command(shell, "--noprofile", "--norc", "-c", script)
				cmd.Dir = t.TempDir()
				cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "PATH="+fakeBin(t)+":"+os.Getenv("PATH"))
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %v %q", tc.words, err, out)
				}
				got := parseCompletionWords(string(out))
				if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
					t.Fatalf("%q: %q want %q", tc.words, got, tc.want)
				}
			}
		})
	}
}
