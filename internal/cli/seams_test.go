package cli

import (
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func seamAddress(fn any) uintptr { return reflect.ValueOf(fn).Pointer() }

// TestSeamsDefaultToRealCalls guards the base assumption of the coverage
// strategy: with no test replacing a seam, every seam is the real call.
func TestSeamsDefaultToRealCalls(t *testing.T) {
	direct := map[string][2]uintptr{
		"setTerminalMode": {seamAddress(setTerminalMode), seamAddress(unix.IoctlSetTermios)},
		"signalProcess":   {seamAddress(signalProcess), seamAddress(syscall.Kill)},
		"watchResize":     {seamAddress(watchResize), seamAddress(watchTerminalResize)},
		"pollEscapeInput": {seamAddress(pollEscapeInput), seamAddress(unix.Poll)},
	}
	for name, pair := range direct {
		if pair[0] != pair[1] {
			t.Errorf("%s is not the real call", name)
		}
	}

	// watchSignals is a wrapper around signal.Notify, so check that it really
	// subscribes: deliver SIGCONT to this process and expect it on the channel.
	received := make(chan os.Signal, 1)
	watchSignals(received)
	defer signal.Stop(received)
	if err := signalProcess(os.Getpid(), syscall.SIGCONT); err != nil {
		t.Fatalf("signalProcess: %v", err)
	}
	select {
	case got := <-received:
		if got != syscall.SIGCONT {
			t.Fatalf("watchSignals delivered %v, want SIGCONT", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchSignals did not deliver SIGCONT")
	}
}
