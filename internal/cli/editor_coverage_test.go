package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func newEditor(out *bytes.Buffer) *questionEditor {
	return &questionEditor{out: out, prompt: "> ", columns: 10, line: []rune("ab cd"), cursor: 2}
}

// TestQuestionEditorPositions pins the row/column arithmetic used for redraws
// of the whole line ("prompt + line"): a rune that would straddle the last
// column is pushed to the next row, and a wide rune never splits.
func TestQuestionEditorPositions(t *testing.T) {
	var out bytes.Buffer
	e := newEditor(&out)
	e.columns = 4
	if row, col := e.position(len(e.line)); row != 1 || col != 3 {
		t.Fatalf("ascii wrap row=%d col=%d", row, col)
	}
	e.line = []rune("中文")
	e.cursor = 1
	e.columns = 1
	if row, col := e.position(len(e.line)); row != 8 || col != 0 {
		t.Fatalf("wide rune wrap row=%d col=%d", row, col)
	}
	e.line = []rune("\tword")
	e.cursor = 1
	e.columns = 8
	if row, col := e.position(len(e.line)); row != 1 || col != 4 {
		t.Fatalf("tab expansion row=%d col=%d", row, col)
	}
}

// TestQuestionEditorKeys asserts the resulting line and cursor after every key
// name the editor accepts, so a key that silently stops editing fails here.
func TestQuestionEditorKeys(t *testing.T) {
	for _, tc := range []struct {
		name     string
		line     string
		cursor   int
		key      string
		wantLine string
		wantCur  int
	}{
		{"left", "ab cd", 2, "left", "ab cd", 1},
		{"right", "ab cd", 2, "right", "ab cd", 3},
		{"home", "ab cd", 2, "home", "ab cd", 0},
		{"end", "ab cd", 2, "end", "ab cd", 5},
		{"word-left", "ab cd", 2, "word-left", "ab cd", 0},
		{"word-right", "ab cd", 2, "word-right", "ab cd", 3},
		{"backspace", "ab cd", 2, "backspace", "a cd", 1},
		{"backspace at start", "ab", 0, "backspace", "ab", 0},
		{"delete", "ab cd", 2, "delete", "abcd", 2},
		{"delete at end", "ab", 2, "delete", "ab", 2},
		{"delete-word-left", "ab cd", 2, "delete-word-left", " cd", 0},
		{"delete-word-right", "ab cd", 2, "delete-word-right", "abcd", 2},
		{"kill-left", "ab cd", 2, "kill-left", " cd", 0},
		{"kill-right", "ab cd", 2, "kill-right", "ab", 2},
		{"clear", "ab cd", 2, "clear", "ab cd", 2},
		{"unknown", "ab cd", 2, "unknown", "ab cd", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			e := newEditor(&out)
			e.line, e.cursor = []rune(tc.line), tc.cursor
			e.key(tc.key)
			if string(e.line) != tc.wantLine || e.cursor != tc.wantCur {
				t.Fatalf("line=%q cursor=%d want %q,%d", string(e.line), e.cursor, tc.wantLine, tc.wantCur)
			}
			if tc.key == "clear" && !strings.Contains(out.String(), "\x1b[1;1H\x1b[0J") {
				t.Fatalf("clear output=%q", out.String())
			}
		})
	}
}

func TestQuestionEditorYankCycleAndHistory(t *testing.T) {
	var out bytes.Buffer
	e := newEditor(&out)
	e.line, e.cursor = []rune("ab cd"), 2
	e.kills = []string{"cd", "XY"}
	e.key("yank")
	if string(e.line) != "abcd cd" || e.cursor != 4 {
		t.Fatalf("yank line=%q cursor=%d", string(e.line), e.cursor)
	}
	e.key("yank-pop")
	if string(e.line) != "abXY cd" || e.cursor != 4 || !e.yanking {
		t.Fatalf("yank-pop line=%q cursor=%d yanking=%v", string(e.line), e.cursor, e.yanking)
	}

	e.line, e.cursor, e.undo, e.redo = []rune("ab"), 2, nil, nil
	e.key("backspace")
	if string(e.line) != "a" || e.cursor != 1 {
		t.Fatalf("backspace line=%q cursor=%d", string(e.line), e.cursor)
	}
	e.key("undo")
	if string(e.line) != "ab" || e.cursor != 2 {
		t.Fatalf("undo line=%q cursor=%d", string(e.line), e.cursor)
	}
	e.key("redo")
	if string(e.line) != "a" || e.cursor != 1 {
		t.Fatalf("redo line=%q cursor=%d", string(e.line), e.cursor)
	}
	e.redo = nil
	e.key("redo")
	if string(e.line) != "a" {
		t.Fatalf("empty redo changed line=%q", string(e.line))
	}

	// The kill ring keeps the newest 32 entries and ignores a repeated head.
	e.kills = nil
	for i := 0; i < 40; i++ {
		e.line, e.cursor = []rune(strings.Repeat("z", i+1)), i+1
		e.kill(0, len(e.line))
	}
	if len(e.kills) != 32 || e.kills[0] != strings.Repeat("z", 40) {
		t.Fatalf("kills=%d head=%q", len(e.kills), e.kills[0])
	}
	head := e.kills[0]
	e.line, e.cursor = []rune(head), len(head)
	e.kill(0, len(head))
	if len(e.kills) != 32 || e.kills[0] != head {
		t.Fatalf("duplicate kill pushed the ring: kills=%d", len(e.kills))
	}

	e.line, e.cursor = []rune("  word"), 6
	if got := e.wordLeft(); got != 2 {
		t.Fatalf("wordLeft=%d", got)
	}
	e.line = []rune("abc def")
	e.cursor = 3
	if got := e.wordRight(false); got != 4 {
		t.Fatalf("wordRight after space=%d", got)
	}
	e.cursor = 0
	if got := e.wordRight(true); got != 4 {
		t.Fatalf("wordRight deleting=%d", got)
	}
	e.move(-5)
	if e.cursor != 0 {
		t.Fatalf("move below zero=%d", e.cursor)
	}
	e.move(500)
	if e.cursor != len(e.line) {
		t.Fatalf("move beyond end=%d", e.cursor)
	}
	if !asciiWord('A') || asciiWord('-') {
		t.Fatal("ascii word classification mismatch")
	}
	if readlineRuneWidth(0) != 0 || readlineRuneWidth('中') != 2 || readlineRuneWidth('A') != 1 {
		t.Fatal("rune width mismatch")
	}
	if strings.Contains(out.String(), "\x00") || !utf8.ValidString(out.String()) {
		t.Fatalf("editor output=%q", out.String())
	}
}

// TestQuestionEditorRedraws exercises the cursor-movement and refresh
// branches that only appear once the line wraps or a redraw follows a redraw.
func TestQuestionEditorRedraws(t *testing.T) {
	var out bytes.Buffer
	e := newEditor(&out)
	e.columns = 4
	e.line = []rune("abcdef")
	e.cursor = 6
	e.refresh()
	if !strings.Contains(out.String(), "\x1b[1G\x1b[0J") || e.previousRows == 0 {
		t.Fatalf("refresh output=%q previousRows=%d", out.String(), e.previousRows)
	}
	e.previousRows = 2
	e.refresh()
	if !strings.Contains(out.String(), "\x1b[2A") {
		t.Fatalf("redraw did not move up: %q", out.String())
	}

	// lineRows > row: the cursor sits on an earlier row than the line end.
	out.Reset()
	e.previousRows = 0
	e.cursor = 0
	e.refresh()
	if !strings.HasSuffix(out.String(), "\x1b[2A") {
		t.Fatalf("no upward adjustment: %q", out.String())
	}

	// A line that fills the last column exactly reserves a trailing space.
	out.Reset()
	e.prompt, e.line, e.cursor, e.columns = "", []rune("abcd"), 0, 4
	e.refresh()
	if !strings.Contains(out.String(), "\x1b[1G\x1b[0Jabcd ") {
		t.Fatalf("trailing space missing: %q", out.String())
	}

	// move across rows refreshes; move within a row emits relative motion.
	out.Reset()
	e.prompt, e.line, e.columns, e.cursor = "> ", []rune("abcdef"), 4, 0
	e.move(6)
	if !strings.Contains(out.String(), "\x1b[1G\x1b[0J") {
		t.Fatalf("row move did not refresh: %q", out.String())
	}
	out.Reset()
	e.columns = 20
	e.cursor = 0
	e.move(2)
	if !strings.Contains(out.String(), "\x1b[2C") {
		t.Fatalf("forward move output=%q", out.String())
	}
	out.Reset()
	e.move(0)
	if !strings.Contains(out.String(), "\x1b[2D") {
		t.Fatalf("backward move output=%q", out.String())
	}

	// Spaces before the cursor are skipped, then the whole word before them.
	e.line, e.cursor = []rune("ab  cd"), 4
	if got := e.wordLeft(); got != 0 {
		t.Fatalf("wordLeft across spaces=%d", got)
	}

	for i := 0; i < 2100; i++ {
		e.line = []rune("x")
		e.beforeEdit()
	}
	if len(e.undo) != 2048 {
		t.Fatalf("undo depth=%d", len(e.undo))
	}
}
