//go:build darwin

package cli

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

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
	stop := watchTerminalResize(slave, func(int) {})
	stop()
	resumed := make(chan struct{}, 1)
	cleanup, suspend, err := protectTerminal(int(slave.Fd()), func() { resumed <- struct{}{} })
	if err != nil || cleanup == nil || suspend == nil {
		t.Fatalf("protect=%v", err)
	}
	done := make(chan struct{})
	go func() { suspend(); close(done) }()
	time.Sleep(20 * time.Millisecond)
	syscall.Kill(os.Getpid(), syscall.SIGCONT)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("terminal suspend did not resume")
	}
	cleanup()
	stop = watchTerminalResize(slave, func(int) {})
	syscall.Kill(os.Getpid(), syscall.SIGWINCH)
	time.Sleep(20 * time.Millisecond)
	stop()
	if !isTerminal(slave) {
		t.Fatal("pty slave not recognized as terminal")
	}
	if isTerminal(nil) {
		t.Fatal("nil file recognized as terminal")
	}
	reader := bufio.NewReader(slave)
	promptSession := &session{in: reader, inputFile: slave, outputFile: slave, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	promptDone := make(chan string)
	go func() {
		answer, ok := promptSession.terminalPrompt("> ")
		if !ok {
			promptDone <- "not accepted"
			return
		}
		promptDone <- answer
	}()
	time.Sleep(50 * time.Millisecond)
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
	time.Sleep(50 * time.Millisecond)
	if _, err := master.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
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
	time.Sleep(50 * time.Millisecond)
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
