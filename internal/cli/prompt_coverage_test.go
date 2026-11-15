package cli

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func promptSession(input string) *session {
	return &session{in: bufio.NewReader(strings.NewReader(input)), out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
}

func TestTerminalPromptKeys(t *testing.T) {
	input := "a\x01\x05\x02\x06\x15\x0b\x17\x19\x0c\x1f\x1e\tb\x04\r"
	answer, ok := promptSession(input).terminalPrompt("> ")
	if !ok || answer == "" {
		t.Fatalf("answer=%q ok=%v", answer, ok)
	}
	if _, ok := promptSession("\x03").terminalPrompt("> "); ok {
		t.Fatal("Ctrl+C accepted")
	}
	if _, ok := promptSession("\x04").terminalPrompt("> "); ok {
		t.Fatal("Ctrl+D accepted")
	}
	if _, ok := promptSession("\x1a").terminalPrompt("> "); ok {
		t.Fatal("Ctrl+Z accepted")
	}
	// Every escape sequence is consumed as a cursor/word command, so the
	// following literal "Z" is what gets submitted. The bare ESC is an
	// Alt-prefix and therefore swallows the next character; the over-long
	// numeric parameter is abandoned once its digit budget runs out, leaving
	// the trailing digits as literal input.
	longDigits := "\x1b[" + strings.Repeat("1", 70)
	for _, tc := range []struct {
		seq  string
		want string
	}{
		{"\x1b", ""},
		{"\x1bb", "Z"},
		{"\x1bf", "Z"},
		{"\x1bd", "Z"},
		{"\x1by", "Z"},
		{"\x1b\x7f", "Z"},
		{"\x1b[D", "Z"},
		{"\x1b[C", "Z"},
		{"\x1b[1;5D", "Z"},
		{"\x1b[1;3C", "Z"},
		{"\x1b[5C", "Z"},
		{"\x1b[1;6D", "Z"},
		{"\x1b[H", "Z"},
		{"\x1b[F", "Z"},
		{"\x1b[1~", "Z"},
		{"\x1b[4~", "Z"},
		{"\x1b[3;5~", "Z"},
		{"\x1b[3;6~", "Z"},
		{"\x1b[3;7~", "Z"},
		{"\x1b[3~", "Z"},
		{"\x1b[999~", "Z"},
		{longDigits, "11111Z"},
	} {
		answer, ok := promptSession(tc.seq + "Z\n").terminalPrompt("> ")
		if !ok || answer != tc.want {
			t.Fatalf("%q accepted=%v answer=%q want=%q", tc.seq, ok, answer, tc.want)
		}
	}
	s := promptSession("")
	if _, err := s.escapeKey(); err == nil {
		t.Fatal("empty escape accepted")
	}
	if answer, ok := promptSession("").terminalPrompt("> "); ok || answer != "" {
		t.Fatalf("EOF prompt answer=%q ok=%v", answer, ok)
	}
	t.Setenv("TERM", "dumb")
	if _, ok := promptSession("x\n").terminalPrompt("> "); !ok {
		t.Fatal("dumb prompt rejected")
	}
}
