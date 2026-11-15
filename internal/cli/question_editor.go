package cli

import (
	"fmt"
	"io"
	"unicode"

	"github.com/puppetdevz/ianchor/internal/textwidth"
)

type editState struct {
	line   string
	cursor int
}

// questionEditor follows the pinned readline/promises editor, whose completion
// is disabled: insertion writes the inserted text directly, even in mid-line.
// Positions use runes internally and terminal columns for output.
type questionEditor struct {
	out                           io.Writer
	prompt                        string
	line                          []rune
	cursor, columns, previousRows int
	undo, redo                    []editState
	kills                         []string
	killIndex                     int
	yanking                       bool
}

func (e *questionEditor) position(end int) (row, col int) {
	for _, r := range []rune(textwidth.Strip(e.prompt + string(e.line[:end]))) {
		w := readlineRuneWidth(r)
		if r == '\t' {
			w = 8 - col%8
		}
		if e.columns > 0 && w == 2 && col+1 == e.columns {
			row++
			col = 0
		}
		col += w
		if e.columns > 0 && col >= e.columns {
			row += col / e.columns
			col %= e.columns
		}
	}
	return
}

func (e *questionEditor) refresh() {
	if e.previousRows > 0 {
		fmt.Fprintf(e.out, "\x1b[%dA", e.previousRows)
	}
	fmt.Fprintf(e.out, "\x1b[1G\x1b[0J%s%s", e.prompt, string(e.line))
	lineRows, lineCols := e.position(len(e.line))
	row, col := e.position(e.cursor)
	if lineCols == 0 {
		fmt.Fprint(e.out, " ")
	}
	fmt.Fprintf(e.out, "\x1b[%dG", col+1)
	if lineRows > row {
		fmt.Fprintf(e.out, "\x1b[%dA", lineRows-row)
	}
	e.previousRows = row
}

func (e *questionEditor) move(target int) {
	if target < 0 {
		target = 0
	}
	if target > len(e.line) {
		target = len(e.line)
	}
	oldRow, oldCol := e.position(e.cursor)
	e.cursor = target
	row, col := e.position(target)
	if oldRow != row {
		e.refresh()
		return
	}
	if col < oldCol {
		fmt.Fprintf(e.out, "\x1b[%dD", oldCol-col)
	}
	if col > oldCol {
		fmt.Fprintf(e.out, "\x1b[%dC", col-oldCol)
	}
}

func (e *questionEditor) beforeEdit() {
	e.undo = append(e.undo, editState{string(e.line), e.cursor})
	if len(e.undo) > 2048 {
		e.undo = e.undo[1:]
	}
}
func (e *questionEditor) insert(s string) {
	e.beforeEdit()
	text := []rune(s)
	next := make([]rune, 0, len(e.line)+len(text))
	next = append(next, e.line[:e.cursor]...)
	next = append(next, text...)
	next = append(next, e.line[e.cursor:]...)
	e.line = next
	e.cursor += len(text)
	fmt.Fprint(e.out, s)
}
func (e *questionEditor) erase(start, end int) {
	e.beforeEdit()
	e.line = append(e.line[:start], e.line[end:]...)
	e.cursor = start
	e.refresh()
}
func (e *questionEditor) kill(start, end int) {
	removed := string(e.line[start:end])
	if removed != "" && (len(e.kills) == 0 || e.kills[0] != removed) {
		e.kills = append([]string{removed}, e.kills...)
		e.killIndex = 0
		if len(e.kills) > 32 {
			e.kills = e.kills[:32]
		}
	}
	e.erase(start, end)
}
func (e *questionEditor) yank() {
	if len(e.kills) > 0 {
		e.yanking = true
		e.insert(e.kills[e.killIndex])
	}
}
func (e *questionEditor) yankPop() {
	if !e.yanking || len(e.kills) < 2 {
		return
	}
	start := e.cursor - len([]rune(e.kills[e.killIndex]))
	e.killIndex = (e.killIndex + 1) % len(e.kills)
	text := []rune(e.kills[e.killIndex])
	next := append([]rune{}, e.line[:start]...)
	next = append(next, text...)
	next = append(next, e.line[e.cursor:]...)
	e.line = next
	e.cursor = start + len(text)
	e.refresh()
}
func asciiWord(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}
func (e *questionEditor) wordLeft() int {
	start := e.cursor
	for start > 0 && unicode.IsSpace(e.line[start-1]) {
		start--
	}
	if start > 0 {
		word := asciiWord(e.line[start-1])
		for start > 0 && !unicode.IsSpace(e.line[start-1]) && asciiWord(e.line[start-1]) == word {
			start--
		}
	}
	return start
}
func (e *questionEditor) wordRight(deleting bool) int {
	end := e.cursor
	if end < len(e.line) {
		first := e.line[end]
		for end < len(e.line) {
			r := e.line[end]
			same := unicode.IsSpace(r)
			if !unicode.IsSpace(first) {
				same = asciiWord(r) == asciiWord(first) && (deleting || !unicode.IsSpace(r))
			}
			if !same {
				break
			}
			end++
		}
	}
	for end < len(e.line) && unicode.IsSpace(e.line[end]) {
		end++
	}
	return end
}
func (e *questionEditor) historyEdit(redo bool) {
	from, to := &e.undo, &e.redo
	if redo {
		from, to = &e.redo, &e.undo
	}
	if len(*from) == 0 {
		return
	}
	*to = append(*to, editState{string(e.line), e.cursor})
	last := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	e.line = []rune(last.line)
	e.cursor = last.cursor
	e.refresh()
}

func (e *questionEditor) key(name string) {
	if name != "yank-pop" {
		e.yanking = false
	}
	switch name {
	case "left":
		e.move(e.cursor - 1)
	case "right":
		e.move(e.cursor + 1)
	case "home":
		e.move(0)
	case "end":
		e.move(len(e.line))
	case "word-left":
		e.move(e.wordLeft())
	case "word-right":
		e.move(e.wordRight(false))
	case "backspace":
		if e.cursor > 0 {
			e.erase(e.cursor-1, e.cursor)
		}
	case "delete":
		if e.cursor < len(e.line) {
			e.erase(e.cursor, e.cursor+1)
		}
	case "delete-word-left":
		if e.cursor > 0 {
			e.erase(e.wordLeft(), e.cursor)
		}
	case "delete-word-right":
		if e.cursor < len(e.line) {
			e.erase(e.cursor, e.wordRight(true))
		}
	case "kill-left":
		e.kill(0, e.cursor)
	case "kill-right":
		e.kill(e.cursor, len(e.line))
	case "yank":
		e.yank()
	case "yank-pop":
		e.yankPop()
	case "undo":
		e.historyEdit(false)
	case "redo":
		e.historyEdit(true)
	case "clear":
		fmt.Fprint(e.out, "\x1b[1;1H\x1b[0J")
		e.refresh()
	}
}
