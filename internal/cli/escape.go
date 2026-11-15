package cli

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

var errEscapeTimeout = errors.New("readline escape timeout")

// pollEscapeInput is a test seam for the 500 ms escape wait; production polls
// the real descriptor.
var pollEscapeInput = unix.Poll

// Readline waits 500 ms for the rest of an escape key. Poll only when the
// buffered reader is empty; a later independent key must stay in the stream.
func (s *session) readEscapeRune() (rune, error) {
	if s.in.Buffered() == 0 && s.inputFile != nil {
		deadline := time.Now().Add(500 * time.Millisecond)
		for {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				return 0, errEscapeTimeout
			}
			events := []unix.PollFd{{Fd: int32(s.inputFile.Fd()), Events: unix.POLLIN}}
			n, err := pollEscapeInput(events, int((remaining+time.Millisecond-1)/time.Millisecond))
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				return 0, err
			}
			if n == 0 {
				return 0, errEscapeTimeout
			}
			break
		}
	}
	r, _, err := s.in.ReadRune()
	return r, err
}
