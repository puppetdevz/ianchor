package cli

import (
	"io"
	"strings"

	"github.com/puppetdevz/ianchor/internal/textwidth"
)

// minWidthToWrap is commander's threshold below which descriptions are left on
// the same line instead of being wrapped.
const minWidthToWrap = 40

// implicitHelp is the -h/--help option every command exposes.
var implicitHelp = &Option{Short: "-h", Long: "--help", Desc: "display help for command"}

// visibleOptions returns the declared options plus the implicit help option.
func (c *Command) visibleOptions() []*Option {
	opts := make([]*Option, 0, len(c.Options)+1)
	opts = append(opts, c.Options...)
	for _, o := range c.Options {
		if o.Long == "--help" {
			return opts
		}
	}
	return append(opts, implicitHelp)
}

func (o *Option) term() string {
	parts := make([]string, 0, 2)
	if o.Short != "" {
		parts = append(parts, o.Short)
	}
	if o.Long != "" {
		parts = append(parts, o.Long)
	}
	term := strings.Join(parts, ", ")
	if o.Value != "" {
		term += " " + o.Value
	}
	return term
}

func (o *Option) description() string {
	if o.DefaultDisplay == "" {
		return o.Desc
	}
	if o.Desc == "" {
		return "(default: " + o.DefaultDisplay + ")"
	}
	return o.Desc + " (default: " + o.DefaultDisplay + ")"
}

// displayedCommands appends the implicit help command, which commander only
// creates for commands that own subcommands.
func (c *Command) displayedCommands() []*Command {
	out := make([]*Command, 0, len(c.Commands)+1)
	for _, sub := range c.Commands {
		if !sub.Hidden {
			out = append(out, sub)
		}
	}
	if len(c.Commands) > 0 {
		out = append(out, &Command{Name: "help", Desc: "display help for command", Args: []*Arg{{Name: "command"}}})
	}
	return out
}

func argUsage(a *Arg) string {
	if a.Required {
		return "<" + a.Name + ">"
	}
	return "[" + a.Name + "]"
}

func (a *Arg) description() string {
	if !a.HasDefault {
		return a.Desc
	}
	if a.Desc == "" {
		return "(default: \"" + a.Default + "\")"
	}
	return a.Desc + " (default: \"" + a.Default + "\")"
}

// subcommandTerm renders a command line in the Commands section; it only
// mentions [options] when the command declares options of its own.
func subcommandTerm(c *Command) string {
	term := c.Name
	if len(c.Options) > 0 {
		term += " [options]"
	}
	for _, a := range c.Args {
		term += " " + argUsage(a)
	}
	return term
}

func (c *Command) usageString() string {
	parts := []string{"[options]"}
	if len(c.Commands) > 0 {
		parts = append(parts, "[command]")
	}
	for _, a := range c.Args {
		parts = append(parts, argUsage(a))
	}
	return strings.Join(parts, " ")
}

// commandUsage prefixes the usage string with the command path.
func commandUsage(c *Command) string {
	var names []string
	for cur := c; cur != nil; cur = cur.parent {
		names = append([]string{cur.Name}, names...)
	}
	return strings.Join(names, " ") + " " + c.usageString()
}

func (s *session) helpWidth(w io.Writer) int {
	if cols := outputColumns(w); cols > 0 {
		return cols
	}
	return 80
}

// formatHelp reproduces commander's help layout for one command.
func (s *session) formatHelp(c *Command, w io.Writer) string {
	helpWidth := s.helpWidth(w)

	options := c.visibleOptions()
	commands := c.displayedCommands()
	termWidth := 0
	for _, o := range options {
		termWidth = max(termWidth, textwidth.Of(o.term()))
	}
	for _, sub := range commands {
		termWidth = max(termWidth, textwidth.Of(subcommandTerm(sub)))
	}
	for _, a := range c.Args {
		termWidth = max(termWidth, textwidth.Of(a.Name))
	}

	formatItem := func(term, description string) string {
		const indent = "  "
		if description == "" {
			return indent + term
		}
		padded := term + strings.Repeat(" ", termWidth-textwidth.Of(term))
		remaining := helpWidth - termWidth - 2 - 2
		formatted := description
		if remaining >= minWidthToWrap && !preformatted(description) {
			wrapped := boxWrap(description, remaining)
			formatted = strings.ReplaceAll(wrapped, "\n", "\n"+strings.Repeat(" ", termWidth+2))
		}
		return indent + padded + "  " + strings.ReplaceAll(formatted, "\n", "\n"+indent)
	}

	lines := []string{"Usage: " + commandUsage(c), ""}
	if c.Desc != "" {
		lines = append(lines, strings.Split(boxWrap(c.Desc, helpWidth), "\n")...)
		lines = append(lines, "")
	}
	if hasDescribedArgs(c) {
		lines = append(lines, "Arguments:")
		for _, a := range c.Args {
			lines = append(lines, formatItem(a.Name, a.description()))
		}
		lines = append(lines, "")
	}
	if len(options) > 0 {
		lines = append(lines, "Options:")
		for _, o := range options {
			lines = append(lines, formatItem(o.term(), o.description()))
		}
		lines = append(lines, "")
	}
	if len(commands) > 0 {
		lines = append(lines, "Commands:")
		for _, sub := range commands {
			lines = append(lines, formatItem(subcommandTerm(sub), sub.Desc))
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func hasDescribedArgs(c *Command) bool {
	for _, arg := range c.Args {
		if arg.Desc != "" {
			return true
		}
	}
	return false
}

func preformatted(s string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] == '\n' && (s[i] == ' ' || s[i] == '\t') {
			return true
		}
	}
	return false
}

// boxWrap wraps at whitespace while preserving existing line breaks and the
// leading whitespace of each chunk, like commander's boxWrap.
func boxWrap(s string, width int) string {
	if width < minWidthToWrap {
		return s
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		chunks := chunkLine(line)
		if len(chunks) == 0 {
			out = append(out, "")
			continue
		}
		sum := []string{chunks[0]}
		sumWidth := textwidth.Of(chunks[0])
		for _, chunk := range chunks[1:] {
			visible := textwidth.Of(chunk)
			if sumWidth+visible <= width {
				sum = append(sum, chunk)
				sumWidth += visible
				continue
			}
			out = append(out, strings.Join(sum, ""))
			sum = []string{strings.TrimLeft(chunk, " \t")}
			sumWidth = textwidth.Of(sum[0])
		}
		if len(sum) > 0 {
			out = append(out, strings.Join(sum, ""))
		}
	}
	return strings.Join(out, "\n")
}

// chunkLine splits a line into /[\s]*[^\s]+/ chunks.
func chunkLine(line string) []string {
	var chunks []string
	i := 0
	for i < len(line) {
		start := i
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i == len(line) {
			break
		}
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		chunks = append(chunks, line[start:i])
	}
	return chunks
}
