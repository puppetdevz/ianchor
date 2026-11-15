package cli

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// outputColumns reports the terminal width of w, or 0 when w is not a TTY.
func outputColumns(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	sz, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0
	}
	return int(sz.Col)
}
