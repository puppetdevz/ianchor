//go:build darwin

package cli

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// mutexBuffer is a goroutine-safe sink, so the test goroutine can inspect the
// prompt while a session goroutine is still writing to it.
type mutexBuffer struct {
	sync.Mutex
	buf bytes.Buffer
}

func newMutexBuffer() *mutexBuffer { return &mutexBuffer{} }

func (m *mutexBuffer) Write(p []byte) (int, error) {
	m.Lock()
	defer m.Unlock()
	return m.buf.Write(p)
}

// waitForOutput blocks until the PTY has displayed needle under the caller's
// output mutex, so a test never races the prompt it is about to type into.
func waitForOutput(t *testing.T, out *mutexBuffer, needle string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		out.Lock()
		buffer := out.buf.String()
		out.Unlock()
		if strings.Contains(buffer, needle) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q in prompt output %q", needle, buffer)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTerminalDetectsRealPTY(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
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
	defer slave.Close()
	if _, _, err := protectTerminal(-1, func() {}); err == nil {
		t.Fatal("invalid terminal accepted")
	}
	if !isTerminal(slave) {
		t.Fatal("pty not detected")
	}
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 111}); err != nil {
		t.Fatal(err)
	}
	if got := terminalColumns(slave); got != 111 {
		t.Fatalf("terminalColumns=%d, want 111 from the pty window size", got)
	}
	if got := outputColumns(slave); got != 111 {
		t.Fatalf("outputColumns=%d, want 111 from the pty window size", got)
	}
	// Resize callbacks are compared with what the loop actually observed, so a
	// lost SIGWINCH can no longer pass on a bare time.Sleep.
	var resized sync.Mutex
	var widths []int
	watching := watchTerminalResize(slave, func(columns int) {
		resized.Lock()
		widths = append(widths, columns)
		resized.Unlock()
	})
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 123}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		resized.Lock()
		got := append([]int(nil), widths...)
		resized.Unlock()
		if len(got) > 0 {
			if got[len(got)-1] != 123 {
				t.Fatalf("resize widths = %v, want the new 123 columns", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resize callback never observed SIGWINCH; widths = %v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	watching()
	resumed := make(chan struct{}, 1)
	cleanup, suspend, err := protectTerminal(int(slave.Fd()), func() { resumed <- struct{}{} })
	if err != nil || cleanup == nil || suspend == nil {
		t.Fatalf("protect=%v", err)
	}
	// SIGTSTP is observed instead of delivered: a stopped test process could
	// never send the SIGCONT that resumes it, which would hang the suite
	// instead of reporting a failure.
	realSignalProcess := signalProcess
	tstpSeen := make(chan struct{}, 1)
	signalProcess = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGTSTP {
			tstpSeen <- struct{}{}
			return nil
		}
		return realSignalProcess(pid, sig)
	}
	t.Cleanup(func() { signalProcess = realSignalProcess })
	done := make(chan struct{})
	go func() { suspend(); close(done) }()
	select {
	case <-tstpSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("suspend never asked the process to stop")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGCONT never reached the continue handler")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal suspend did not return after SIGCONT")
	}
	cleanup()
	reader := bufio.NewReader(slave)
	promptOut := newMutexBuffer()
	promptSession := &session{in: reader, inputFile: slave, outputFile: slave, out: promptOut, errOut: &bytes.Buffer{}}
	promptDone := make(chan string)
	go func() {
		answer, ok := promptSession.terminalPrompt("> ")
		if !ok {
			promptDone <- "not accepted"
			return
		}
		promptDone <- answer
	}()
	waitForOutput(t, promptOut, "> ")
	if _, err := master.Write([]byte("pty-answer\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-promptDone:
		if answer != "pty-answer" {
			t.Fatalf("pty prompt=%q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pty prompt timed out")
	}
	go func() {
		answer, ok := promptSession.terminalPrompt("> ")
		if !ok {
			promptDone <- "not accepted"
			return
		}
		promptDone <- answer
	}()
	// The editor prints the prompt before it reads, so waiting for it removes
	// the fixed delay that used to hide a slow start on a loaded machine.
	waitForOutput(t, promptOut, "> ")
	if _, err := master.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	// A lone escape is only committed after the 500 ms disambiguation window;
	// the trailing text proves the sequence was consumed as "not a key".
	time.Sleep(600 * time.Millisecond)
	if _, err := master.Write([]byte("ctx\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-promptDone:
		if answer != "ctx" {
			t.Fatalf("escape prompt=%q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("escape prompt timed out")
	}
	ctx := &Context{s: promptSession}
	go func() {
		answer, ok := ctx.Prompt("> ")
		if !ok {
			promptDone <- "not accepted"
			return
		}
		promptDone <- answer
	}()
	waitForOutput(t, promptOut, "> ")
	if _, err := master.Write([]byte("context\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case answer := <-promptDone:
		if answer != "context" {
			t.Fatalf("context prompt=%q", answer)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context prompt timed out")
	}
	size := &unix.Winsize{Row: 24, Col: 100}
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, size); err != nil {
		t.Fatal(err)
	}
	if width := (&session{}).helpWidth(slave); width != 100 {
		t.Fatalf("pty help width=%d", width)
	}
	closedFile, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if outputColumns(closedFile) != 0 {
		t.Fatal("closed columns accepted")
	}
	if _, _, err := protectTerminal(int(closedFile.Fd()), func() {}); err == nil {
		t.Fatal("closed terminal accepted")
	}
	if !isTerminal(slave) {
		t.Fatal("slave lost terminal state")
	}
	slave.Close()
	null, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	nullSession := &session{in: bufio.NewReader(strings.NewReader("\x1b")), inputFile: null, outputFile: null, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	if _, ok := nullSession.terminalPrompt("> "); ok {
		t.Fatal("non-terminal prompt accepted")
	}
	if _, err := nullSession.escapeKey(); err == nil {
		t.Fatal("non-terminal escape accepted")
	}
	closed := &session{in: bufio.NewReader(strings.NewReader("")), inputFile: slave}
	slave.Close()
	if _, err := closed.readEscapeRune(); err == nil {
		t.Fatal("closed escape accepted")
	}
	slave.Close()
	if _, _, err := protectTerminal(int(slave.Fd()), func() {}); err == nil {
		t.Fatal("closed terminal accepted")
	}
}
