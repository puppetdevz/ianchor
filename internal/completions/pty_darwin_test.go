//go:build darwin

package completions

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/puppetdevz/ianchor/internal/store"
)

const wantCompletePrompt = "SHELL-READY> "

type shellSession struct {
	t      *testing.T
	master *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	buffer string
	seen   int
}

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("no pty available: %v", err)
	}
	fd := int(ptmx.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		ptmx.Close()
		t.Fatalf("grantpt: %v", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		ptmx.Close()
		t.Fatalf("unlockpt: %v", err)
	}
	name := make([]byte, 128)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		ptmx.Close()
		t.Fatalf("ptsname: %v", errno)
	}
	end := strings.IndexByte(string(name), 0)
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR, 0)
	if err != nil {
		ptmx.Close()
		t.Fatalf("open slave: %v", err)
	}
	return ptmx, slave
}

func startShell(t *testing.T, argv []string) *shellSession {
	return startShellWithHome(t, argv, t.TempDir(), fakeBin(t))
}

func startShellWithHome(t *testing.T, argv []string, home, commandDir string) *shellSession {
	t.Helper()
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		t.Fatalf("%s not available", argv[0])
	}
	master, slave := openPTY(t)
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Dir = t.TempDir()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	cmd.Env = append(os.Environ(),
		"PS1="+wantCompletePrompt,
		"PS2=CONT> ",
		"TERM=xterm-256color",
		"HOME="+home,
		"PATH="+commandDir+":"+os.Getenv("PATH"),
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start shell: %v", err)
	}
	slave.Close()
	session := &shellSession{t: t, master: master, cmd: cmd}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				session.mu.Lock()
				session.buffer += string(buf[:n])
				session.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		master.Close()
	})
	session.expect(wantCompletePrompt)
	return session
}

// fakeBin installs a stand-in ianchor that answers __complete, so completion can
// be exercised without touching a real database.
func fakeBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"__complete\" ]; then\n" +
		"  if [ -n \"${IANCHOR_TEST_COMPLETION_DELAY:-}\" ]; then sleep \"$IANCHOR_TEST_COMPLETION_DELAY\"; fi\n" +
		"  printf 'zshrc\\0app config\\0colon:a:b\\0back\\\\slash\\0-n\\0--help\\0wild*card\\0quote'\\''s\"name\\0!bang\\0evil$(touch${IFS}PWNED)\\0'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	path := filepath.Join(dir, "ianchor")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (s *shellSession) snapshot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buffer
}

// waitFor polls the terminal until every needle has appeared. Listing output
// has no reliable completion marker, so the assertions poll instead of
// sleeping for a fixed time and hoping the shell was fast enough.
func (s *shellSession) waitFor(want ...string) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		buffer := s.snapshot()
		missing := ""
		for _, needle := range want {
			if !strings.Contains(buffer, needle) {
				missing = needle
				break
			}
		}
		if missing == "" {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("terminal output never showed %q in:\n%s", missing, buffer)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *shellSession) expect(needle string) {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		buffer := s.snapshot()
		if pos := strings.Index(buffer[s.seen:], needle); pos >= 0 {
			s.seen += pos + len(needle)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("timed out waiting for %q in:\n%s", needle, s.snapshot())
}

func (s *shellSession) write(text string) {
	s.t.Helper()
	if _, err := s.master.Write([]byte(text)); err != nil {
		s.t.Fatalf("write: %v", err)
	}
}

// lineAfterCompletion types text, presses Tab and then rewrites the resulting
// buffer as a printf command so the completed words can be asserted verbatim.
// The returned slice holds one entry per word the shell finally saw.
func TestParseCompletionWordsPreservesCarriageReturn(t *testing.T) {
	got := parseCompletionWords("ianchor\r\x00remove\x00word\nmarker|\x00\x00")
	if len(got) != 4 || got[0] != "ianchor\r" || got[1] != "remove" || got[2] != "word\nmarker|" || got[3] != "" {
		t.Fatalf("got %#v", got)
	}
}

func parseCompletionWords(output string) []string {
	if !strings.HasSuffix(output, "\x00") {
		return nil
	}
	return strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
}

func (s *shellSession) lineAfterCompletion(typed string) []string {
	s.t.Helper()
	// Structured data bypasses the PTY: its ONLCR processing would otherwise
	// alter newlines embedded in arguments. Each call gets a fresh path and marker.
	dir := s.t.TempDir()
	result := filepath.Join(dir, "words")
	done := "AFTER-TAB-DONE-" + filepath.Base(dir)
	s.write(typed + "\t")
	// Readline processes input serially: editing keys queued behind Tab are
	// consumed only after the completion function returns, including slow ones.
	s.write("\x01") // ctrl-a: move to start of the line
	s.write("printf '%s\\0' ")
	s.write("\x05") // ctrl-e: append redirection and completion sentinel
	s.write(" > " + quoteShellPath(result) + "; printf '\\n" + done + "\\n'\n")
	s.expect("\n" + done)
	s.expect(wantCompletePrompt)
	data, err := os.ReadFile(result)
	if err != nil {
		s.t.Fatalf("read completed words: %v\n%s", err, s.snapshot())
	}
	words := parseCompletionWords(string(data))
	if len(words) == 0 {
		s.t.Fatalf("invalid or empty completed words %q in:\n%s", data, s.snapshot())
	}
	return words
}

func scriptPath(t *testing.T, shell string) string {
	t.Helper()
	data, err := Script(shell)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ianchor."+shell)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func quoteShellPath(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'" }

func TestSlowCompletionProcessesQueuedEditingInOrder(t *testing.T) {
	t.Setenv("IANCHOR_TEST_COMPLETION_DELAY", "2")
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			argv := []string{"/bin/bash", "--norc", "--noprofile", "-i"}
			if shell == "zsh" {
				argv = []string{"zsh", "-f", "-i"}
			}
			s := startShell(t, argv)
			if shell == "zsh" {
				s.write("autoload -Uz compinit; compinit -u >/dev/null 2>&1\n")
				s.expect(wantCompletePrompt)
			}
			s.write("source " + quoteShellPath(scriptPath(t, shell)) + "\n")
			s.expect(wantCompletePrompt)
			words := s.lineAfterCompletion("ianchor remove quote")
			if len(words) != 3 || words[0] != "ianchor" || words[1] != "remove" || words[2] != `quote's"name` {
				t.Fatalf("unexpected words %#v; terminal %q", words, s.snapshot())
			}
		})
	}
}

func TestInteractiveSpecialNames(t *testing.T) {
	cases := []struct{ prefix, want string }{
		{"app", "app config"}, {"colon", "colon:a:b"}, {"back", `back\slash`},
		{"wild", "wild*card"}, {"quote", `quote's"name`}, {"!ba", "!bang"},
	}
	for _, shell := range []string{"bash", "zsh"} {
		for _, tc := range cases {
			t.Run(shell+"/"+tc.prefix, func(t *testing.T) {
				argv := []string{"/bin/bash", "--norc", "--noprofile", "-i"}
				if shell == "zsh" {
					argv = []string{"zsh", "-f", "-i"}
				}
				session := startShell(t, argv)
				if shell == "zsh" {
					session.write("autoload -Uz compinit; compinit -u >/dev/null 2>&1\n")
					session.expect(wantCompletePrompt)
				}
				session.write("source " + quoteShellPath(scriptPath(t, shell)) + "\n")
				session.expect(wantCompletePrompt)
				words := session.lineAfterCompletion("ianchor remove " + tc.prefix)
				if len(words) != 3 || words[0] != "ianchor" || words[1] != "remove" || words[2] != tc.want {
					t.Fatalf("completed words = %#v, want %q; terminal output = %q", words, tc.want, session.snapshot())
				}
			})
		}
	}
}

func TestRealBinaryInteractiveCompletion(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	binDir := filepath.Join(root, "bin")
	cloud := filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "ianchor")
	if err := os.MkdirAll(cloud, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(binDir, "ianchor"), "./cmd/ianchor")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real binary: %v\n%s", err, out)
	}
	db, err := store.Open(cloud)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app config", "zshrc", "evil$(touch${IFS}PWNED)"} {
		if err := db.AddEntry(name, nil, "file", "linked"); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			args := []string{"/bin/bash", "--norc", "--noprofile", "-i"}
			if shell == "zsh" {
				args = []string{"zsh", "-f", "-i"}
			}
			session := startShellWithHome(t, args, home, binDir)
			if shell == "zsh" {
				session.write("autoload -Uz compinit; compinit -u >/dev/null 2>&1\n")
				session.expect(wantCompletePrompt)
			}
			session.write("source " + scriptPath(t, shell) + "\n")
			session.expect(wantCompletePrompt)
			words := session.lineAfterCompletion("ianchor recover app")
			if strings.Join(words, "|") != "ianchor|recover|app config" {
				t.Fatalf("real completion words: %q", words)
			}
			words = session.lineAfterCompletion("ianchor remove evil")
			if strings.Join(words, "|") != "ianchor|remove|evil$(touch${IFS}PWNED)" {
				t.Fatalf("real malicious completion words: %q", words)
			}
			if _, err := os.Stat(filepath.Join(session.cmd.Dir, "PWNED")); !os.IsNotExist(err) {
				t.Fatalf("completion executed candidate: %v", err)
			}
		})
	}
}

func TestBashInteractiveCompletion(t *testing.T) {
	session := startShell(t, []string{"/bin/bash", "--norc", "--noprofile", "-i"})
	session.write("source " + scriptPath(t, "bash") + "\n")
	session.expect(wantCompletePrompt)

	words := session.lineAfterCompletion("ianchor recover zs")
	if strings.Join(words, " ") != "ianchor recover zshrc" {
		t.Fatalf("line = %v", words)
	}
}

func TestBashInteractiveListsCommands(t *testing.T) {
	session := startShell(t, []string{"/bin/bash", "--norc", "--noprofile", "-i"})
	session.write("source " + scriptPath(t, "bash") + "\n")
	session.expect(wantCompletePrompt)
	// bash 3.2 lists candidates on the second Tab.
	session.write("ianchor re\t\t")
	session.waitFor("recover", "relink", "remove")
}

func TestZshInteractiveCompletion(t *testing.T) {
	session := startShell(t, []string{"zsh", "-f", "-i"})
	session.write("autoload -Uz compinit; compinit -u >/dev/null 2>&1\n")
	session.expect(wantCompletePrompt)
	session.write("source " + scriptPath(t, "zsh") + "\n")
	session.expect(wantCompletePrompt)

	words := session.lineAfterCompletion("ianchor recover zs")
	if strings.Join(words, " ") != "ianchor recover zshrc" {
		t.Fatalf("line = %v", words)
	}
	session.write("clear\n")
	session.expect(wantCompletePrompt)

	// A name with a space must be escaped so it stays one shell word.
	words = session.lineAfterCompletion("ianchor remove app")
	if strings.Join(words, " ") != "ianchor remove app config" {
		t.Fatalf("escaped completion = %v", words)
	}
}

func TestInteractiveCompletionDoesNotExecuteNames(t *testing.T) {
	for _, shell := range []string{"bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			argv := []string{"/bin/bash", "--norc", "--noprofile", "-i"}
			if shell == "zsh" {
				argv = []string{"zsh", "-f", "-i"}
			}
			s := startShell(t, argv)
			if shell == "zsh" {
				s.write("autoload -Uz compinit; compinit -u >/dev/null 2>&1\n")
				s.expect(wantCompletePrompt)
			}
			s.write("source " + quoteShellPath(scriptPath(t, shell)) + "\n")
			s.expect(wantCompletePrompt)
			words := s.lineAfterCompletion("ianchor remove evil")
			if strings.Join(words, " ") != "ianchor remove evil$(touch${IFS}PWNED)" {
				t.Fatalf("completion changed name: %q", words)
			}
			if _, err := os.Lstat(filepath.Join(s.cmd.Dir, "PWNED")); !os.IsNotExist(err) {
				t.Fatalf("completion executed candidate, stat error: %v", err)
			}
		})
	}
}

func TestZshInteractiveListsCommands(t *testing.T) {
	session := startShell(t, []string{"zsh", "-f", "-i"})
	session.write("autoload -Uz compinit; compinit -u >/dev/null 2>&1\n")
	session.expect(wantCompletePrompt)
	session.write("source " + scriptPath(t, "zsh") + "\n")
	session.expect(wantCompletePrompt)
	session.write("ianchor re\t")
	session.waitFor("recover", "relink", "remove")
}
