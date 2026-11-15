package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// palette implements the subset of chalk that ianchor uses. Colours are only
// emitted when chalk would emit them: chalk keys its level off stdout, so a
// redirected stdout disables colour everywhere.
type palette struct{ enabled bool }

func newPalette() *palette {
	return &palette{enabled: colorEnabled()}
}

func colorEnabled() bool { return chalkColorEnabled(isTerminal(os.Stdout)) }

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	_, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	return err == nil
}

func (p *palette) wrap(open, close, s string) string {
	if !p.enabled {
		return s
	}
	return "\x1b[" + open + "m" + s + "\x1b[" + close + "m"
}

// Red renders s in red, used for error prefixes and deleted entries.
func (p *palette) Red(s string) string { return p.wrap("31", "39", s) }

// Green renders s in green.
func (p *palette) Green(s string) string { return p.wrap("32", "39", s) }

// Yellow renders s in yellow.
func (p *palette) Yellow(s string) string { return p.wrap("33", "39", s) }

// Cyan renders s in cyan.
func (p *palette) Cyan(s string) string { return p.wrap("36", "39", s) }

// Dim renders s dimmed.
func (p *palette) Dim(s string) string { return p.wrap("2", "22", s) }

// spinnerFrames are the braille frames the Node implementation cycled through.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	spinnerDelay    = 100 * time.Millisecond
	spinnerInterval = 80 * time.Millisecond
)

// Spinner writes the same loading indicator to stderr, including the
// clear-to-end-of-line escape when a frame was actually drawn.
type Spinner struct {
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// StartSpinner begins the indicator. Nothing is written for the first 100ms,
// so fast commands stay silent, exactly like the Node version.
func (s *session) StartSpinner() *Spinner {
	sp := &Spinner{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(sp.done)
		timer := time.NewTimer(spinnerDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-sp.stop:
			return
		}
		fmt.Fprintf(s.errOut, "%s Loading entries…", spinnerFrames[0])
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		frame := 0
		for {
			select {
			case <-ticker.C:
				frame = (frame + 1) % len(spinnerFrames)
				fmt.Fprintf(s.errOut, "\r%s Loading entries…", spinnerFrames[frame])
			case <-sp.stop:
				fmt.Fprint(s.errOut, "\r\x1b[K")
				return
			}
		}
	}()
	return sp
}

// Stop finishes the indicator and waits until its output is flushed.
func (sp *Spinner) Stop() {
	if sp == nil {
		return
	}
	sp.stopOnce.Do(func() { close(sp.stop) })
	<-sp.done
}

// Prompt writes text and reads one line from stdin. The second result is false
// when stdin reached EOF before any input, which makes the caller exit quietly
// like readline does.
func (s *session) Prompt(text string) (string, bool) {
	if isTerminal(s.outputFile) {
		return s.terminalPrompt(text)
	}
	fmt.Fprint(s.out, text)
	var answer strings.Builder
	for {
		character, _, err := s.in.ReadRune()
		if err != nil {
			// readline.question does not resolve a partial line at pipe EOF.
			return "", false
		}
		if character == '\r' || character == '\n' {
			return answer.String(), true
		}
		answer.WriteRune(character)
	}
}

var _ io.Reader = (*bufio.Reader)(nil)
