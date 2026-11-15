// Package textwidth measures terminal columns the way cli-table3 does: ANSI
// escapes count as zero, East Asian characters as two columns, combining marks
// as zero.
package textwidth

import (
	"regexp"
	"strings"
	"unicode"
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

// Strip removes SGR escape sequences.
func Strip(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// Of returns the display width of s, taking the widest line.
func Of(s string) int {
	best := 0
	for _, line := range strings.Split(Strip(s), "\n") {
		w := 0
		for _, r := range line {
			w += Rune(r)
		}
		if w > best {
			best = w
		}
	}
	return best
}

// Rune returns the column width of a single rune.
func Rune(r rune) int {
	switch {
	case r == 0:
		return 0
	case r < 32 || (r >= 0x7f && r < 0xa0):
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r):
		return 0
	case isWide(r):
		return 2
	}
	return 1
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK radicals, Kangxi
		r >= 0x3041 && r <= 0x33FF, // Hiragana .. CJK compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xA960 && r <= 0xA97F,
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F, // emoji
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}
