package textwidth

import "testing"

func TestOf(t *testing.T) {
	cases := map[string]int{
		"":                    0,
		"abc":                 3,
		".zshrc":              6,
		"中文 名称":               9,
		"日本語":                 6,
		"café":                4,
		"e\u0301":             1,
		"a	b":                 2,
		"\x1b[31mred\x1b[39m": 3,
		"line one\nline two":  8,
		// ZWJ sequences count per code point; only exotic names differ.
		"👨‍👩‍👧":              6,
		"한국어":                6,
		"ＡＢ":                 4,
		"deleted (clean up)": 18,
		"─":                  1,
		"\u200b":             0,
	}
	for input, want := range cases {
		if got := Of(input); got != want {
			t.Fatalf("Of(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestStrip(t *testing.T) {
	if got := Strip("\x1b[90m│\x1b[39m\x1b[31m Name \x1b[39m"); got != "│ Name " {
		t.Fatalf("strip = %q", got)
	}
	if got := Strip("plain"); got != "plain" {
		t.Fatalf("strip plain = %q", got)
	}
	if Rune(0) != 0 || Rune('\n') != 0 || Rune(0x7f) != 0 || Rune(0x0301) != 0 || Rune('A') != 1 || Rune('中') != 2 {
		t.Fatal("rune width mismatch")
	}
}

func TestRune(t *testing.T) {
	zero := []rune{'\u0301', '\u200b', '\u200d', '\ufe0f'}
	for _, r := range zero {
		if got := Rune(r); got != 0 {
			t.Fatalf("Rune(%U) = %d, want 0", r, got)
		}
	}
	if got := Rune('a'); got != 1 {
		t.Fatalf("a = %d", got)
	}
	if got := Rune('中'); got != 2 {
		t.Fatalf("中 = %d", got)
	}
}
