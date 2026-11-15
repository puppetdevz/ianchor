package cli

import (
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Test seams: production values are the real signal and ioctl calls.
var (
	setTerminalMode = unix.IoctlSetTermios
	watchSignals    = func(c chan os.Signal) {
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM, syscall.SIGCONT)
	}
	signalProcess = syscall.Kill
	watchResize   = watchTerminalResize
)

// Register before exposing raw mode. The mutex orders raw installation,
// suspend/continue, and signal-driven restoration. Shutdown joins the worker.
func protectTerminal(fd int, onContinue func()) (cleanup func(), suspend func(), err error) {
	original, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, nil, err
	}
	raw := *original
	raw.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	raw.Oflag |= unix.ONLCR
	raw.Cflag |= unix.CS8
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	var state sync.Mutex
	state.Lock()
	closing, suspended := false, false
	signals := make(chan os.Signal, 4)
	done, exited := make(chan struct{}), make(chan struct{})
	// resumed is buffered rather than closed on each resume: closing it twice
	// (two suspend/continue cycles, or a spurious SIGCONT) would panic, and the
	// signal handler must never take the whole process down.
	resumed := make(chan struct{}, 1)
	watchSignals(signals)
	go func() {
		defer close(exited)
		for {
			select {
			case received := <-signals:
				state.Lock()
				if received == syscall.SIGCONT {
					resume := suspended && !closing
					if resume {
						_ = setTerminalMode(fd, unix.TIOCSETA, &raw)
						suspended = false
					}
					state.Unlock()
					if resume {
						onContinue()
						select {
						case resumed <- struct{}{}:
						default:
						}
					}
					continue
				}
				_ = setTerminalMode(fd, unix.TIOCSETA, original)
				state.Unlock()
				signal.Reset(received)
				_ = signalProcess(os.Getpid(), received.(syscall.Signal))
			case <-done:
				return
			}
		}
	}()
	cleanup = func() {
		state.Lock()
		closing = true
		_ = setTerminalMode(fd, unix.TIOCSETA, original)
		state.Unlock()
		signal.Stop(signals)
		close(done)
		<-exited
	}
	suspend = func() {
		state.Lock()
		// A suspend that arrives after cleanup has no handler left to send
		// SIGCONT, so waiting for resume would block forever.
		if closing {
			state.Unlock()
			return
		}
		// Drop a stale wake-up from an earlier cycle so the wait below can only
		// be satisfied by the SIGCONT that follows this suspend.
		select {
		case <-resumed:
		default:
		}
		suspended = true
		_ = setTerminalMode(fd, unix.TIOCSETA, original)
		state.Unlock()
		_ = signalProcess(os.Getpid(), syscall.SIGTSTP)
		<-resumed
	}
	err = setTerminalMode(fd, unix.TIOCSETA, &raw)
	state.Unlock()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return cleanup, suspend, nil
}
