// Package cli is a small re-implementation of the commander v14 behaviour
// ianchor depends on: option parsing order, error wording, exit codes, help
// layout and did-you-mean suggestions.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Option declares one command line flag.
type Option struct {
	Short          string // "-V"
	Long           string // "--version"
	Value          string // "<name>" when the option takes a value
	Desc           string
	DefaultDisplay string // shown as "(default: ...)" in help
	IsVersion      bool   // prints the version and exits 0
}

func (o *Option) key() string {
	if o.Long != "" {
		return strings.TrimLeft(o.Long, "-")
	}
	return strings.TrimLeft(o.Short, "-")
}

func (o *Option) takesValue() bool { return o.Value != "" }

func (o *Option) matches(arg string) bool {
	return (o.Short != "" && o.Short == arg) || (o.Long != "" && o.Long == arg)
}

// Arg declares one positional argument.
type Arg struct {
	Name       string
	Required   bool
	Desc       string
	Default    string
	HasDefault bool
}

// Command is a node in the command tree.
type Command struct {
	Name     string
	Desc     string
	Args     []*Arg
	Options  []*Option
	Commands []*Command
	Hidden   bool
	Action   func(*Context) error

	parent *Command
}

func (c *Command) findCommand(name string) *Command {
	for _, sub := range c.Commands {
		if sub.Name == name {
			return sub
		}
	}
	return nil
}

func (c *Command) findOption(arg string) *Option {
	for _, o := range c.Options {
		if o.matches(arg) {
			return o
		}
	}
	return nil
}

// Error is a CLI failure. Prefix selects the "Error: " wording used for
// business failures; commander's program.error() omits it.
type Error struct {
	Message string
	Prefix  bool
}

func (e *Error) Error() string { return e.Message }

// NewError builds a business error, printed as "Error: <message>".
func NewError(format string, args ...any) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), Prefix: true}
}

// PlainError prints the message without an "Error: " prefix.
func PlainError(message string) *Error { return &Error{Message: message} }

type session struct {
	version    string
	in         *bufio.Reader
	out        io.Writer
	errOut     io.Writer
	color      *palette
	values     map[*Command]map[string]string
	inputFile  *os.File
	outputFile *os.File
	promptErr  error
}

// Context is handed to a command action.
type Context struct {
	s       *session
	Command *Command
	Args    []string
	Values  map[string]string
}

// Bool reports whether a boolean flag was set.
func (c *Context) Bool(name string) bool { return c.Values[name] == "true" }

// String returns an option value.
func (c *Context) String(name string) string { return c.Values[name] }

// Out is stdout.
func (c *Context) Out() io.Writer { return c.s.out }

// Err is stderr.
func (c *Context) Err() io.Writer { return c.s.errOut }

// Printf writes to stdout.
func (c *Context) Printf(format string, args ...any) { fmt.Fprintf(c.s.out, format, args...) }

// Println writes a line to stdout.
func (c *Context) Println(args ...any) { fmt.Fprintln(c.s.out, args...) }

// Red colours s for error text.
func (c *Context) Red(s string) string { return c.s.color.Red(s) }

// Green colours s for success text.
func (c *Context) Green(s string) string { return c.s.color.Green(s) }

// Yellow colours s for prompts.
func (c *Context) Yellow(s string) string { return c.s.color.Yellow(s) }

// Cyan colours s for table headers.
func (c *Context) Cyan(s string) string { return c.s.color.Cyan(s) }

// Dim colours s for secondary text.
func (c *Context) Dim(s string) string { return c.s.color.Dim(s) }

// Prompt asks a question on stdout and reads one line from stdin.
func (c *Context) Prompt(text string) (string, bool) { return c.s.Prompt(text) }

// ErrPromptClosed ends a question without submitting an answer at pipe EOF.
var ErrPromptClosed = errors.New("prompt closed without an answer")

// PromptError distinguishes EOF or a terminal abort from a completed answer.
func (c *Context) PromptError() error {
	if c.s.promptErr != nil {
		return c.s.promptErr
	}
	return ErrPromptClosed
}

// Spinner starts the loading indicator on stderr.
func (c *Context) Spinner() *Spinner { return c.s.StartSpinner() }

// Run parses args against root and executes the selected action. It returns
// the process exit code.
func Run(root *Command, version string, args []string, in io.Reader, out, errOut io.Writer) int {
	linkParents(root, nil)
	// A shell that started ianchor with SIGINT ignored must not make the
	// command uninterruptible.
	stopInterrupt := guardInheritedInterrupt()
	defer stopInterrupt()
	s := &session{
		version: version,
		in:      bufio.NewReader(in),
		out:     out,
		errOut:  errOut,
		color:   newPalette(),
		values:  map[*Command]map[string]string{},
	}
	s.inputFile, _ = in.(*os.File)
	s.outputFile, _ = out.(*os.File)
	return s.parseCommand(root, nil, args)
}

func linkParents(c *Command, parent *Command) {
	c.parent = parent
	for _, sub := range c.Commands {
		linkParents(sub, c)
	}
}

func (s *session) parseCommand(c *Command, operands, unknown []string) int {
	var code int
	var done bool
	extraOperands, extraUnknown, code, done := s.parseOptions(c, unknown)
	if done {
		return code
	}
	operands = append(operands, extraOperands...)
	unknown = extraUnknown

	if len(operands) > 0 {
		if sub := c.findCommand(operands[0]); sub != nil {
			return s.parseCommand(sub, operands[1:], unknown)
		}
	}
	if len(operands) > 0 && operands[0] == "help" {
		return s.helpCommand(c, operands[1:])
	}
	if len(c.Commands) > 0 && len(operands)+len(unknown) == 0 && c.Action == nil {
		s.outputHelp(c, s.errOut)
		return 1
	}
	if s.helpRequested(unknown) {
		s.outputHelp(c, s.out)
		return 0
	}
	if c.Action == nil {
		// A command with subcommands and no operands was handled above, so
		// only a leaf-less command without an Action or stray operands or
		// options reaches this point; it is a no-op, as in the Node CLI.
		if len(operands) > 0 {
			return s.unknownCommand(c, operands[0])
		}
		if len(unknown) > 0 {
			return s.unknownOption(c, unknown[0])
		}
		return 0
	}
	if len(unknown) > 0 {
		return s.unknownOption(c, unknown[0])
	}
	args, ok := s.processArgs(c, operands)
	if !ok {
		return 1
	}
	ctx := &Context{s: s, Command: c, Args: args, Values: s.values[c]}
	err := c.Action(ctx)
	if s.promptErr != nil {
		err = s.promptErr
	}
	if errors.Is(err, ErrPromptClosed) {
		return 0
	}
	if err != nil {
		return s.reportError(err)
	}
	return 0
}

func (s *session) parseOptions(c *Command, args []string) (operands, unknown []string, code int, done bool) {
	dest := &operands
	activeGroup := ""
	hasGroup := false
	i := 0

	for i < len(args) || hasGroup {
		var arg string
		if hasGroup {
			arg = activeGroup
			hasGroup = false
		} else {
			arg = args[i]
			i++
		}

		if arg == "--" {
			if dest == &unknown {
				unknown = append(unknown, arg)
			}
			*dest = append(*dest, args[i:]...)
			break
		}

		if len(arg) > 1 && arg[0] == '-' {
			if opt := c.findOption(arg); opt != nil {
				if opt.takesValue() {
					if i >= len(args) {
						return nil, nil, s.optionMissingArgument(opt), true
					}
					s.setValue(c, opt, args[i])
					i++
				} else {
					if opt.IsVersion {
						fmt.Fprintf(s.out, "%s\n", s.version)
						return nil, nil, 0, true
					}
					s.setValue(c, opt, "true")
				}
				continue
			}
		}

		if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
			if opt := c.findOption(arg[:2]); opt != nil {
				if opt.takesValue() {
					s.setValue(c, opt, arg[2:])
				} else {
					if opt.IsVersion {
						fmt.Fprintf(s.out, "%s\n", s.version)
						return nil, nil, 0, true
					}
					s.setValue(c, opt, "true")
					activeGroup = "-" + arg[2:]
					hasGroup = true
				}
				continue
			}
		}

		if eq := strings.IndexByte(arg, '='); eq > 2 && arg[0] == '-' && arg[1] == '-' {
			if opt := c.findOption(arg[:eq]); opt != nil && opt.takesValue() {
				s.setValue(c, opt, arg[eq+1:])
				continue
			}
		}

		if dest == &operands && len(arg) > 1 && arg[0] == '-' && !(len(c.Commands) == 0 && looksNumeric(arg)) {
			dest = &unknown
		}
		*dest = append(*dest, arg)
	}
	return operands, unknown, 0, false
}

func looksNumeric(arg string) bool {
	trimmed := strings.TrimLeft(arg, "-")
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func (s *session) setValue(c *Command, opt *Option, v string) {
	values, ok := s.values[c]
	if !ok {
		values = map[string]string{}
		s.values[c] = values
	}
	values[opt.key()] = v
}

func (s *session) processArgs(c *Command, args []string) ([]string, bool) {
	for i, a := range c.Args {
		if a.Required && i >= len(args) {
			s.printError(fmt.Sprintf("missing required argument '%s'", a.Name))
			return nil, false
		}
	}
	if len(args) > len(c.Args) {
		s.printError(s.excessMessage(c, len(args)))
		return nil, false
	}
	out := append([]string{}, args...)
	for i, a := range c.Args {
		if i >= len(out) && a.HasDefault {
			out = append(out, a.Default)
		}
	}
	return out, true
}

func (s *session) excessMessage(c *Command, got int) string {
	expected := len(c.Args)
	noun := "arguments"
	if expected == 1 {
		noun = "argument"
	}
	target := ""
	if c.parent != nil {
		target = fmt.Sprintf(" for '%s'", c.Name)
	}
	return fmt.Sprintf("too many arguments%s. Expected %d %s but got %d.", target, expected, noun, got)
}

// helpRequested reports whether -h or --help was passed as an unknown option.
func (s *session) helpRequested(unknown []string) bool {
	for _, arg := range unknown {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func (s *session) helpCommand(c *Command, args []string) int {
	if len(args) == 0 {
		s.outputHelp(c, s.out)
		return 0
	}
	if sub := c.findCommand(args[0]); sub != nil {
		s.outputHelp(sub, s.out)
		return 0
	}
	s.outputHelp(c, s.errOut)
	return 1
}

func (s *session) outputHelp(c *Command, w io.Writer) {
	fmt.Fprint(w, s.formatHelp(c, w))
}

func (s *session) unknownCommand(c *Command, name string) int {
	message := fmt.Sprintf("unknown command '%s'", name)
	candidates := make([]string, 0, len(c.Commands))
	for _, sub := range c.Commands {
		candidates = append(candidates, sub.Name)
	}
	message += suggestSimilar(name, candidates)
	s.printError(message)
	return 1
}

func (s *session) unknownOption(c *Command, flag string) int {
	message := fmt.Sprintf("unknown option '%s'", flag)
	if strings.HasPrefix(flag, "--") {
		message += suggestSimilar(flag, s.optionCandidates(c))
	}
	s.printError(message)
	return 1
}

// optionCandidates collects long flags from the command and all its ancestors,
// including the implicit help option, mirroring commander.
func (s *session) optionCandidates(c *Command) []string {
	var names []string
	seen := map[string]bool{}
	for cur := c; cur != nil; cur = cur.parent {
		for _, o := range cur.visibleOptions() {
			if o.Long == "" || seen[o.Long] {
				continue
			}
			seen[o.Long] = true
			names = append(names, o.Long)
		}
	}
	return names
}

func (s *session) optionMissingArgument(opt *Option) int {
	s.printError(fmt.Sprintf("option '%s' argument missing", opt.term()))
	return 1
}

func (s *session) printError(message string) {
	fmt.Fprintf(s.errOut, "%s\n", s.color.Red("error: "+message))
}

// reportError prints an action failure and returns its exit code.
func (s *session) reportError(err error) int {
	var cliErr *Error
	if errors.As(err, &cliErr) {
		if cliErr.Prefix {
			fmt.Fprintf(s.errOut, "%s\n", s.color.Red("Error: "+cliErr.Message))
		} else {
			fmt.Fprintf(s.errOut, "%s\n", cliErr.Message)
		}
		return 1
	}
	fmt.Fprintf(s.errOut, "%s\n", s.color.Red("Error: "+err.Error()))
	return 1
}
