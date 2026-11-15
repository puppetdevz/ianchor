package cli

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func terminalColumns(file *os.File) int {
	if file != nil {
		if size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ); err == nil {
			return int(size.Col)
		}
	}
	return 0
}

// Resize signals are independent of stdin redirection. Join before the prompt
// returns so a late resize cannot redraw over the command's result.
func watchTerminalResize(file *os.File, resized func(int)) func() {
	if !isTerminal(file) {
		return func() {}
	}
	changes := make(chan os.Signal, 1)
	done, exited := make(chan struct{}), make(chan struct{})
	signal.Notify(changes, syscall.SIGWINCH)
	go func() {
		defer close(exited)
		for {
			select {
			case <-changes:
				resized(terminalColumns(file))
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(changes); close(done); <-exited }
}
