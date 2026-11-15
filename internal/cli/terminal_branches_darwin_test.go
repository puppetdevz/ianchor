//go:build darwin

package cli

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openTestPTY returns the master and slave ends of a fresh pseudo terminal.
func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatal(err)
	}
	name := make([]byte, 128)
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		t.Fatal(errno)
	}
	end := strings.IndexByte(string(name), 0)
	if end < 0 {
		t.Fatal("unterminated pty name")
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

// stubTerminalSignals captures the guard channel so a test delivers SIGCONT or
// SIGTERM without signalling the real process. SIGTSTP loops back as SIGCONT.
func stubTerminalSignals(t *testing.T) func() chan os.Signal {
	t.Helper()
	var captured chan os.Signal
	originalWatch, originalKill := watchSignals, signalProcess
	watchSignals = func(c chan os.Signal) { captured = c }
	signalProcess = func(pid int, signal syscall.Signal) error {
		if signal == syscall.SIGTSTP && captured != nil {
			captured <- syscall.SIGCONT
		}
		return nil
	}
	t.Cleanup(func() { watchSignals, signalProcess = originalWatch, originalKill })
	return func() chan os.Signal { return captured }
}

// A resize during an open prompt redraws with the new width. The watcher is
// stubbed so delivery is synchronous: waiting on the real SIGWINCH would let a
// lost signal pass silently, which is exactly what this test must not allow.
func TestTerminalPromptRedrawsOnResize(t *testing.T) {
	input, writer := io.Pipe()
	out := &bytes.Buffer{}
	installed := make(chan func(int), 1)
	originalWatchResize := watchResize
	watchResize = func(_ *os.File, handler func(int)) func() {
		select {
		case installed <- handler:
		default:
		}
		return func() {}
	}
	t.Cleanup(func() { watchResize = originalWatchResize })
	answering := &session{in: bufio.NewReader(input), out: out, errOut: &bytes.Buffer{}}
	done := make(chan string, 1)
	go func() {
		answer, ok := answering.terminalPrompt("> ")
		if !ok {
			done <- "<closed>"
			return
		}
		done <- answer
	}()
	var resize func(int)
	select {
	case resize = <-installed:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not install a resize watcher")
	}
	resize(120)
	if _, err := writer.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-done:
		if answer != "" {
			t.Fatalf("resized prompt=%q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resized prompt timed out")
	}
	if got := strings.Count(out.String(), "\x1b[0J> "); got != 2 {
		t.Fatalf("refresh count=%d, want one at open and one on resize", got)
	}
	writer.Close()
}

// The pinned promises question closes on Ctrl+Z and redraws on SIGCONT.
func TestTerminalPromptSuspendCallback(t *testing.T) {
	master, slave := openTestPTY(t)
	stubTerminalSignals(t)
	pausing := &session{in: bufio.NewReader(slave), inputFile: slave, outputFile: slave, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	done := make(chan bool, 1)
	go func() {
		_, ok := pausing.terminalPrompt("> ")
		done <- ok
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := master.Write([]byte("\x1a")); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-done:
		if ok {
			t.Fatal("Ctrl+Z prompt submitted an answer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ctrl+Z prompt did not close")
	}
}

// Guard signals forward termination, and a rejected raw install is reported by
// both protectTerminal and the prompt wrapper.
func TestProtectTerminalSignalForwardingAndInstallFailure(t *testing.T) {
	_, slave := openTestPTY(t)
	signals := stubTerminalSignals(t)
	forwarded := make(chan syscall.Signal, 4)
	originalKill := signalProcess
	signalProcess = func(pid int, signal syscall.Signal) error {
		if signal == syscall.SIGTERM {
			forwarded <- signal
			return nil
		}
		if signal == syscall.SIGTSTP {
			signals() <- syscall.SIGCONT
		}
		return nil
	}
	t.Cleanup(func() { signalProcess = originalKill })
	cleanup, suspend, err := protectTerminal(int(slave.Fd()), func() {})
	if err != nil {
		t.Fatal(err)
	}
	resumed := make(chan struct{})
	go func() { suspend(); close(resumed) }()
	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("suspend did not resume")
	}
	signals() <- syscall.SIGTERM
	select {
	case signal := <-forwarded:
		if signal != syscall.SIGTERM {
			t.Fatalf("forwarded %v", signal)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("termination signal was not forwarded")
	}
	cleanup()

	failing := errors.New("termios rejected")
	originalSet := setTerminalMode
	setTerminalMode = func(fd int, request uint, state *unix.Termios) error { return failing }
	t.Cleanup(func() { setTerminalMode = originalSet; signalProcess = originalKill })
	if _, _, err := protectTerminal(int(slave.Fd()), func() {}); !errors.Is(err, failing) {
		t.Fatalf("raw install err=%v", err)
	}
	prompting := &session{in: bufio.NewReader(strings.NewReader("")), inputFile: slave, outputFile: slave, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	if answer, ok := prompting.terminalPrompt("> "); ok || answer != "" {
		t.Fatalf("prompt with failed terminal=%q ok=%v", answer, ok)
	}
	if !errors.Is(prompting.promptErr, failing) {
		t.Fatalf("prompt error=%v", prompting.promptErr)
	}
}
