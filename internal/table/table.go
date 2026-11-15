// Package table renders the entry table exactly like cli-table3 with its
// default style: grey box drawing, red headers, one space of cell padding.
package table

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/puppetdevz/ianchor/internal/textwidth"
)

// Default cli-table3 styling; @colors/colors detects TERM even with redirected stdout.
const (
	borderOpen  = "\x1b[90m"
	borderClose = "\x1b[39m"
	headOpen    = "\x1b[31m"
	headClose   = "\x1b[39m"
)

// Render builds the table for the given headers and rows. Every row is
// preceded by a horizontal rule, matching cli-table3's non-compact layout.
func Render(headers []string, rows [][]string) string {
	all := make([][]string, 0, len(rows)+1)
	all = append(all, headers)
	all = append(all, rows...)

	cols := len(headers)
	if cols == 0 {
		return ""
	}
	widths := make([]int, cols)
	for _, row := range all {
		for j := 0; j < cols && j < len(row); j++ {
			if w := textwidth.Of(row[j]) + 2; w > widths[j] {
				widths[j] = w
			}
		}
	}

	var out []string
	for i, row := range all {
		out = append(out, horizontal(widths, i))
		height := 1
		for j := 0; j < cols && j < len(row); j++ {
			if n := len(strings.Split(row[j], "\n")); n > height {
				height = n
			}
		}
		for line := 0; line < height; line++ {
			var b strings.Builder
			for j := 0; j < cols; j++ {
				cell := ""
				if j < len(row) {
					lines := strings.Split(row[j], "\n")
					if line < len(lines) {
						cell = lines[line]
					}
				}
				b.WriteString(border("│"))
				b.WriteString(decorate(cell, widths[j], i == 0))
			}
			b.WriteString(border("│"))
			out = append(out, b.String())
		}
	}
	out = append(out, bottom(widths))
	return strings.Join(out, "\n") + "\n"
}

func border(s string) string {
	if !colorsEnabled() {
		return s
	}
	return borderOpen + s + borderClose
}

// cli-table3 uses @colors/colors independently of chalk. In particular,
// NO_COLOR doesn't affect its borders, whereas FORCE_COLOR=0 does.
func colorsEnabled() bool {
	forced := false
	if value, ok := os.LookupEnv("FORCE_COLOR"); ok {
		// JavaScript parseInt accepts a signed decimal prefix and yields NaN
		// for no digits; NaN !== 0 enables colors in the baseline.
		prefix := regexp.MustCompile(`^[+-]?[0-9]+`).FindString(strings.TrimSpace(value))
		if number, err := strconv.ParseInt(prefix, 10, 64); err == nil && number == 0 {
			return false
		}
		forced = true
	}
	if _, ok := os.LookupEnv("CI"); ok {
		for _, key := range []string{"TRAVIS", "CIRCLECI", "APPVEYOR", "GITLAB_CI"} {
			if _, ok := os.LookupEnv(key); ok {
				return true
			}
		}
		return forced || os.Getenv("CI_NAME") == "codeship"
	}
	if version, ok := os.LookupEnv("TEAMCITY_VERSION"); ok {
		return regexp.MustCompile(`^(9\.(0*[1-9]\d*)\.|\d{2,}\.)`).MatchString(version)
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "Hyper", "Apple_Terminal":
		return true
	}
	if regexp.MustCompile(`(?i)^screen|^xterm|^vt100|^rxvt|color|ansi|cygwin|linux`).MatchString(os.Getenv("TERM")) {
		return true
	}
	_, colorterm := os.LookupEnv("COLORTERM")
	return forced || colorterm
}

func decorate(cell string, width int, head bool) string {
	pad := width - 2 - textwidth.Of(cell)
	if pad < 0 {
		pad = 0
	}
	content := " " + cell + strings.Repeat(" ", pad) + " "
	if head && colorsEnabled() {
		return headOpen + strings.ReplaceAll(content, headClose, headOpen) + headClose
	}
	return content
}

func horizontal(widths []int, row int) string {
	left, mid, right := "├", "┼", "┤"
	if row == 0 {
		left, mid, right = "┌", "┬", "┐"
	}
	var b strings.Builder
	for j, w := range widths {
		seg := mid
		if j == 0 {
			seg = left
		}
		seg += strings.Repeat("─", w)
		if j == len(widths)-1 {
			seg += right
		}
		b.WriteString(border(seg))
	}
	return b.String()
}

func bottom(widths []int) string {
	var b strings.Builder
	for j, w := range widths {
		seg := "┴"
		if j == 0 {
			seg = "└"
		}
		seg += strings.Repeat("─", w)
		if j == len(widths)-1 {
			seg += "┘"
		}
		b.WriteString(border(seg))
	}
	return b.String()
}
