package cli

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// stubEscapePoll swaps the escape-wait poll seam for one test.
func stubEscapePoll(t *testing.T, poll func([]unix.PollFd, int) (int, error)) {
	t.Helper()
	original := pollEscapeInput
	pollEscapeInput = poll
	t.Cleanup(func() { pollEscapeInput = original })
}

// emptyPipe gives the session a real descriptor with nothing buffered.
func emptyPipe(t *testing.T) *os.File {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { read.Close(); write.Close() })
	return read
}

// The 500 ms window restarts on EINTR, then expires while still waiting.
func TestReadEscapeRuneInterruptedThenExpired(t *testing.T) {
	stubEscapePoll(t, func([]unix.PollFd, int) (int, error) {
		time.Sleep(550 * time.Millisecond)
		return 0, unix.EINTR
	})
	s := &session{in: bufio.NewReader(strings.NewReader("")), inputFile: emptyPipe(t)}
	if _, err := s.readEscapeRune(); !errors.Is(err, errEscapeTimeout) {
		t.Fatalf("interrupted escape err=%v", err)
	}
}

// A poll failure other than EINTR surfaces unchanged.
func TestReadEscapeRunePollFailure(t *testing.T) {
	want := errors.New("poll failed")
	stubEscapePoll(t, func([]unix.PollFd, int) (int, error) { return 0, want })
	s := &session{in: bufio.NewReader(strings.NewReader("")), inputFile: emptyPipe(t)}
	if _, err := s.readEscapeRune(); !errors.Is(err, want) {
		t.Fatalf("poll failure err=%v", err)
	}
}

// A CSI prefix whose tail never arrives leaves the escape unstyled.
func TestEscapeKeySequenceTimesOut(t *testing.T) {
	calls := 0
	stubEscapePoll(t, func([]unix.PollFd, int) (int, error) {
		calls++
		if calls == 1 {
			return 1, nil // the CSI introducer is ready
		}
		return 0, nil // the tail never arrives
	})
	s := &session{in: bufio.NewReader(strings.NewReader("[")), inputFile: emptyPipe(t)}
	key, err := s.escapeKey()
	if err != nil || key != "" {
		t.Fatalf("truncated escape key=%q err=%v", key, err)
	}
	if calls != 2 {
		t.Fatalf("escape polls=%d", calls)
	}
}

// The same truncated prefix propagates a poll error.
func TestEscapeKeySequencePollFailure(t *testing.T) {
	want := errors.New("poll failed")
	calls := 0
	stubEscapePoll(t, func([]unix.PollFd, int) (int, error) {
		calls++
		if calls == 1 {
			return 1, nil
		}
		return 0, want
	})
	s := &session{in: bufio.NewReader(strings.NewReader("[")), inputFile: emptyPipe(t)}
	if _, err := s.escapeKey(); !errors.Is(err, want) {
		t.Fatalf("escape tail err=%v", err)
	}
	if calls != 2 {
		t.Fatalf("escape polls=%d", calls)
	}
}

// Lowercase CSI finals map to word motions. The caller consumed the ESC.
func TestEscapeKeyLowercaseFinals(t *testing.T) {
	for input, want := range map[string]string{"[d": "word-left", "[c": "word-right"} {
		s := &session{in: bufio.NewReader(strings.NewReader(input))}
		key, err := s.escapeKey()
		if err != nil || key != want {
			t.Fatalf("escape %q key=%q err=%v", input, key, err)
		}
	}
}
