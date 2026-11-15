package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Terminal rendering follows stdout, as in readline.question. Only a terminal
// stdin needs raw mode; redirected input still goes through the same key editor.
func (s *session) terminalPrompt(prompt string) (string, bool) {
	editor := questionEditor{out: s.out, prompt: prompt, columns: terminalColumns(s.outputFile)}
	var editorMu sync.Mutex
	suspend := func() {}
	if isTerminal(s.inputFile) {
		restore, pause, err := protectTerminal(int(s.inputFile.Fd()), func() {
			editorMu.Lock()
			defer editorMu.Unlock()
			editor.refresh()
		})
		if err != nil {
			s.promptErr = err
			return "", false
		}
		defer restore()
		suspend = pause
	}
	stopResize := watchResize(s.outputFile, func(columns int) {
		editorMu.Lock()
		defer editorMu.Unlock()
		editor.columns = columns
		editor.refresh()
	})
	defer stopResize()
	editorMu.Lock()
	if os.Getenv("TERM") == "dumb" {
		fmt.Fprint(s.out, prompt)
	} else {
		editor.refresh()
	}
	editorMu.Unlock()
	for {
		character, _, err := s.in.ReadRune()
		if err != nil {
			return "", false
		}
		if character == '\x1a' {
			// The pinned promises question pauses on Ctrl+Z; SIGCONT redraws
			// it, then the pending question closes without submitting a line.
			suspend()
			return "", false
		}
		done, answer, ok := func() (bool, string, bool) {
			editorMu.Lock()
			defer editorMu.Unlock()
			if character == '\r' || character == '\n' {
				editor.move(len(editor.line))
				fmt.Fprint(s.out, "\r\n")
				return true, string(editor.line), true
			}
			switch character {
			case '\x03':
				s.promptErr = fmt.Errorf("Aborted with Ctrl+C")
				return true, "", false
			case '\x04':
				if len(editor.line) == 0 {
					s.promptErr = fmt.Errorf("Aborted with Ctrl+D")
					return true, "", false
				}
				editor.key("delete")
			case '\x1b':
				key, err := s.escapeKey()
				if err != nil {
					return true, "", false
				}
				editor.key(key)
			default:
				key := map[rune]string{'\x7f': "backspace", '\b': "backspace", '\x01': "home", '\x05': "end", '\x02': "left", '\x06': "right", '\x15': "kill-left", '\x0b': "kill-right", '\x17': "delete-word-left", '\x19': "yank", '\x0c': "clear", '\x1f': "undo", '\x1e': "redo"}[character]
				if key != "" {
					editor.key(key)
				} else if character == '\t' || !unicode.IsControl(character) {
					editor.yanking = false
					editor.insert(string(character))
				}
			}
			return false, "", false
		}()
		if done {
			return answer, ok
		}
	}
}

// Consume a complete CSI/SS3 sequence before dispatch, including numeric keys
// and modifier parameters, so an unknown escape never inserts a trailing '~'.
func (s *session) escapeKey() (string, error) {
	next, err := s.readEscapeRune()
	if errors.Is(err, errEscapeTimeout) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if next != '[' && next != 'O' {
		return map[rune]string{'b': "word-left", 'f': "word-right", 'd': "delete-word-right", 'y': "yank-pop", '\x7f': "delete-word-left", '\b': "delete-word-left"}[next], nil
	}
	var sequence strings.Builder
	for {
		r, err := s.readEscapeRune()
		if errors.Is(err, errEscapeTimeout) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		sequence.WriteRune(r)
		if r >= 0x40 && r <= 0x7e {
			break
		}
		if sequence.Len() > 64 {
			return "", nil
		}
	}
	seq := sequence.String()
	final := seq[len(seq)-1]
	params := strings.Split(seq[:len(seq)-1], ";")
	modifier := 1
	if len(params) > 1 {
		modifier, _ = strconv.Atoi(params[len(params)-1])
	} else if final != '~' && params[0] == "5" {
		modifier = 5
	}
	ctrl, meta, shift := (modifier-1)&4 != 0, (modifier-1)&2 != 0, (modifier-1)&1 != 0
	if ctrl && shift && final != '~' {
		return "", nil
	}
	switch final {
	case 'd':
		return "word-left", nil
	case 'c':
		return "word-right", nil
	case 'D':
		if ctrl || meta {
			return "word-left", nil
		}
		return "left", nil
	case 'C':
		if ctrl || meta {
			return "word-right", nil
		}
		return "right", nil
	case 'H':
		return "home", nil
	case 'F':
		return "end", nil
	case '~':
		switch params[0] {
		case "1", "7":
			return "home", nil
		case "4", "8":
			return "end", nil
		case "3":
			if ctrl && shift {
				return "kill-right", nil
			}
			if ctrl || meta {
				return "delete-word-right", nil
			}
			return "delete", nil
		}
	}
	return "", nil
}
