//go:build darwin

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// A shell that starts the command with SIGINT ignored, as `ianchor init &` does
// in a script, must not be able to make it uninterruptible: the guard installs
// the handler Node has and raises the signal with its default action.
func TestInheritedInterruptGuardRaisesSIGINT(t *testing.T) {
	previousIgnored, previousProcess := signalIgnored, signalProcess
	previousAction := installDefaultAction
	t.Cleanup(func() {
		signalIgnored, signalProcess, installDefaultAction = previousIgnored, previousProcess, previousAction
	})
	signalIgnored = func(os.Signal) bool { return true }
	// The real installation changes this process's disposition for good, so the
	// child process below is what proves it works.
	installDefaultAction = func(syscall.Signal) error { return nil }
	raised := make(chan syscall.Signal, 1)
	signalProcess = func(_ int, sig syscall.Signal) error {
		raised <- sig
		return nil
	}
	stop := guardInheritedInterrupt()
	t.Cleanup(stop)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	select {
	case sig := <-raised:
		if sig != syscall.SIGINT {
			t.Fatalf("guard raised %v, want SIGINT", sig)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the guard never raised the inherited-ignored SIGINT")
	}
}

// Without an inherited ignore the guard installs nothing, so an interactive run
// keeps the terminal handler as the only SIGINT listener.
func TestInterruptGuardIsIdleWithoutAnInheritedIgnore(t *testing.T) {
	if signal.Ignored(syscall.SIGINT) {
		t.Skip("the test process itself inherited an ignored SIGINT")
	}
	stop := guardInheritedInterrupt()
	stop()
}

// A failed disposition change must still end the process, with the exit code a
// shell reports for an interrupted command.
func TestInheritedInterruptGuardFallsBackToTheSignalExitCode(t *testing.T) {
	previousIgnored, previousProcess := signalIgnored, signalProcess
	previousAction, previousExit := installDefaultAction, exitProcess
	t.Cleanup(func() {
		signalIgnored, signalProcess = previousIgnored, previousProcess
		installDefaultAction, exitProcess = previousAction, previousExit
	})
	signalIgnored = func(os.Signal) bool { return true }
	installDefaultAction = func(syscall.Signal) error { return errors.New("sigaction refused") }
	exited := make(chan int, 1)
	exitProcess = func(code int) { exited <- code }
	stop := guardInheritedInterrupt()
	t.Cleanup(stop)
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}
	select {
	case code := <-exited:
		if code != 130 {
			t.Fatalf("guard exited %d, want 130", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the guard neither exited nor raised the signal")
	}
}

// TestInstallDefaultActionRestoresTheKernelDisposition runs in a child process:
// the real installation makes the next SIGINT fatal, which would take the test
// process with it.
func TestInstallDefaultActionRestoresTheKernelDisposition(t *testing.T) {
	if os.Getenv("IANCHOR_TEST_DEFAULT_ACTION") == "1" {
		signal.Ignore(syscall.SIGINT)
		if err := installDefaultAction(syscall.SIGINT); err != nil {
			fmt.Fprintln(os.Stderr, "installDefaultAction:", err)
			os.Exit(9)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		os.Exit(7)
	}
	command := exec.Command(os.Args[0], "-test.run=TestInstallDefaultActionRestoresTheKernelDisposition")
	command.Env = append(os.Environ(), "IANCHOR_TEST_DEFAULT_ACTION=1")
	err := command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("the child did not die by SIGINT: %v", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Fatalf("the child ended with %v, want killed by SIGINT", exitErr)
	}
}

// TestInstallDefaultActionCoversTheKernelCall exercises the seam body in the
// test process. SIGINFO's default action is to ignore, and neither the runtime
// nor this package uses it, so installing it cannot disturb the suite (Go does
// not repair a disposition it believes it owns, so a signal the suite sends
// would stay broken for the rest of the run). An out-of-range signal must be
// reported instead of looking installed.
func TestInstallDefaultActionCoversTheKernelCall(t *testing.T) {
	if err := installDefaultAction(syscall.SIGINFO); err != nil {
		t.Fatalf("install SIGINFO default action: %v", err)
	}
	if err := installDefaultAction(syscall.Signal(9999)); err == nil {
		t.Fatal("an out-of-range signal was accepted")
	}
}
