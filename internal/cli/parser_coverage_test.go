package cli

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func sampleRoot() *Command {
	child := &Command{
		Name: "add",
		Desc: "Add a long description that should wrap when the help width is large enough to exercise the formatter.",
		Args: []*Arg{{Name: "path", Required: true, Desc: "source"}, {Name: "name", Default: "auto", HasDefault: true}},
		Options: []*Option{
			{Short: "-f", Long: "--force", Desc: "replace"},
			{Short: "-n", Long: "--name", Value: "<name>", Desc: "entry", DefaultDisplay: "auto"},
		},
		Action: func(ctx *Context) error {
			ctx.Printf("%s %s %v\n", ctx.Green("ok"), ctx.String("name"), ctx.Bool("force"))
			ctx.Println(ctx.Yellow("done"), ctx.Cyan("table"), ctx.Dim("note"), ctx.Red("bad"))
			return nil
		},
	}
	return &Command{Name: "ianchor", Options: []*Option{{Short: "-V", Long: "--version", IsVersion: true}}, Commands: []*Command{child, {Name: "hidden", Hidden: true}}}
}

func runSample(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := Run(sampleRoot(), "1.1.1", args, strings.NewReader(""), &out, &err)
	return code, out.String(), err.String()
}

func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"NO_COLOR", "FORCE_COLOR", "TERM", "TERM_PROGRAM", "COLORTERM", "CI", "CI_NAME", "TEAMCITY_VERSION", "GITHUB_ACTIONS", "GITEA_ACTIONS", "CIRCLECI", "TRAVIS", "APPVEYOR", "GITLAB_CI", "BUILDKITE", "DRONE", "TF_BUILD", "AGENT_NAME"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	os.Args = []string{"ianchor-color-test"}
}

func TestParserHelpAndErrors(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if code, out, err := runSample("--version"); code != 0 || out != "1.1.1\n" || err != "" {
		t.Fatalf("version code=%d out=%q err=%q", code, out, err)
	}
	if code, out, _ := runSample("-V"); code != 0 || out != "1.1.1\n" {
		t.Fatalf("short version code=%d out=%q", code, out)
	}
	if code, out, _ := runSample("add", "-fn", "named", "source"); code != 0 || !strings.Contains(out, "named true") {
		t.Fatalf("cluster code=%d out=%q", code, out)
	}
	if code, _, err := runSample("add", "--name"); code == 0 || !strings.Contains(err, "argument missing") {
		t.Fatalf("missing option code=%d err=%q", code, err)
	}
	if code, out, _ := runSample("--help"); code != 0 || !strings.Contains(out, "Commands:") {
		t.Fatalf("root help code=%d out=%q", code, out)
	}
	if code, _, err := runSample(); code == 0 || !strings.Contains(err, "Usage:") {
		t.Fatalf("bare root code=%d err=%q", code, err)
	}
	if code, _, err := runSample("help", "missing"); code == 0 || !strings.Contains(err, "Usage:") {
		t.Fatalf("bad help code=%d err=%q", code, err)
	}
	if code, _, err := runSample("ad"); code == 0 || !strings.Contains(err, "Did you mean add") {
		t.Fatalf("unknown command code=%d err=%q", code, err)
	}
	if code, _, err := runSample("add", "--nam", "x", "source"); code == 0 || !strings.Contains(err, "unknown option") {
		t.Fatalf("unknown option code=%d err=%q", code, err)
	}
	if code, _, err := runSample("add"); code == 0 || !strings.Contains(err, "missing required") {
		t.Fatalf("missing arg code=%d err=%q", code, err)
	}
	if code, _, err := runSample("add", "a", "b", "c"); code == 0 || !strings.Contains(err, "too many arguments") {
		t.Fatalf("excess code=%d err=%q", code, err)
	}
	if code, out, err := runSample("add", "--name", "named", "source", "--", "-1"); code != 0 || !strings.Contains(out, "named") {
		t.Fatalf("separator code=%d out=%q err=%q", code, out, err)
	}
	numeric := &Command{Name: "numeric", Args: []*Arg{{Name: "value"}}, Action: func(ctx *Context) error {
		ctx.Println(strings.Join(ctx.Args, ","))
		return nil
	}}
	var numericOut bytes.Buffer
	if code := Run(numeric, "v", []string{"-1.2"}, strings.NewReader(""), &numericOut, &bytes.Buffer{}); code != 0 || numericOut.String() != "-1.2\n" {
		t.Fatalf("numeric code=%d out=%q", code, numericOut.String())
	}
	root := sampleRoot()
	root.Commands[0].Action = func(ctx *Context) error { return NewError("business %s", "bad") }
	var out, err bytes.Buffer
	if code := Run(root, "v", []string{"add", "source"}, strings.NewReader(""), &out, &err); code != 1 || !strings.Contains(err.String(), "Error: business") {
		t.Fatalf("business code=%d err=%q", code, err.String())
	}
	root.Commands[0].Action = func(*Context) error { return PlainError("plain") }
	out.Reset()
	err.Reset()
	if code := Run(root, "v", []string{"add", "source"}, strings.NewReader(""), &out, &err); code != 1 || !strings.Contains(err.String(), "plain") {
		t.Fatalf("plain code=%d err=%q", code, err.String())
	}
	root.Commands[0].Action = func(*Context) error { return errors.New("other") }
	out.Reset()
	err.Reset()
	if code := Run(root, "v", []string{"add", "source"}, strings.NewReader(""), &out, &err); code != 1 || !strings.Contains(err.String(), "other") {
		t.Fatalf("generic code=%d err=%q", code, err.String())
	}
	root.Commands[0].Action = func(ctx *Context) error {
		ctx.s.promptErr = ErrPromptClosed
		return errors.New("ignored")
	}
	if code := Run(root, "v", []string{"add", "source"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("closed prompt code=%d", code)
	}
	if !looksNumeric("-1.2") || looksNumeric("-") || looksNumeric("-a") {
		t.Fatal("numeric classification mismatch")
	}
	leaf := &Command{Name: "leaf", Action: func(*Context) error { return nil }}
	var help bytes.Buffer
	if code := Run(leaf, "v", []string{"-h"}, strings.NewReader(""), &help, &bytes.Buffer{}); code != 0 || !strings.Contains(help.String(), "Usage:") {
		t.Fatalf("leaf code=%d help=%q", code, help.String())
	}
	var leafErr bytes.Buffer
	code := Run(leaf, "v", []string{"--wat"}, strings.NewReader(""), &bytes.Buffer{}, &leafErr)
	if code == 0 || leafErr.Len() == 0 {
		t.Fatalf("leaf unknown code=%d err=%q", code, leafErr.String())
	}
	parent := &Command{Name: "parent", Commands: []*Command{{Name: "child"}}}
	if code := Run(parent, "v", []string{"--wat"}, strings.NewReader(""), &bytes.Buffer{}, &err); code == 0 {
		t.Fatal("parent unknown option accepted")
	}
	custom := &Command{Name: "custom", Options: []*Option{{Long: "--help", Desc: "custom"}}}
	if len(custom.visibleOptions()) != 1 || custom.visibleOptions()[0].Desc != "custom" {
		t.Fatal("custom help option duplicated")
	}
	opt := &Option{Value: "<value>", Desc: "value"}
	if !strings.Contains(opt.term(), "<value>") || opt.description() == "" {
		t.Fatal("empty option formatting mismatch")
	}
	if (&Option{DefaultDisplay: "x"}).description() != "(default: x)" {
		t.Fatal("default-only option mismatch")
	}
	if (&Arg{Default: "x", HasDefault: true}).description() != "(default: \"x\")" {
		t.Fatal("default-only argument mismatch")
	}
	if !preformatted("a\n b") || preformatted("plain") {
		t.Fatal("preformatted mismatch")
	}
	if got := boxWrap("one two", 10); got != "one two" {
		t.Fatalf("narrow wrap=%q", got)
	}
	if chunks := chunkLine(" \t"); len(chunks) != 0 {
		t.Fatalf("whitespace chunks=%v", chunks)
	}
	if outputColumns(&bytes.Buffer{}) != 0 || terminalColumns(nil) != 0 {
		t.Fatal("non-terminal width accepted")
	}
	ctx := &Context{s: &session{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, color: &palette{enabled: true}, values: map[*Command]map[string]string{}}, Values: map[string]string{"name": "value"}}
	if ctx.Out() == nil || ctx.Err() == nil || ctx.String("name") != "value" || ctx.PromptError() == nil {
		t.Fatal("context accessors mismatch")
	}
	ctx.s.promptErr = errors.New("prompt")
	if ctx.PromptError().Error() != "prompt" {
		t.Fatal("prompt error mismatch")
	}
	if (&Error{Message: "x"}).Error() != "x" {
		t.Fatal("error text mismatch")
	}
	for _, key := range []string{"NO_COLOR", "FORCE_COLOR", "TERM", "TERM_PROGRAM", "COLORTERM", "CI", "CI_NAME", "TEAMCITY_VERSION", "GITHUB_ACTIONS", "TF_BUILD", "AGENT_NAME"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("FORCE_COLOR", "true")
	if !chalkColorEnabled(false) {
		t.Fatal("forced color disabled")
	}
	t.Setenv("FORCE_COLOR", "0")
	if chalkColorEnabled(true) {
		t.Fatal("zero force accepted")
	}
	t.Setenv("FORCE_COLOR", "2")
	t.Setenv("TERM", "dumb")
	if !chalkColorEnabled(true) {
		t.Fatal("minimum color disabled")
	}
	t.Setenv("CI", "1")
	t.Setenv("GITHUB_ACTIONS", "true")
	if !chalkColorEnabled(false) {
		t.Fatal("CI color disabled")
	}
	os.Unsetenv("GITHUB_ACTIONS")
	t.Setenv("CI_NAME", "codeship")
	if !chalkColorEnabled(false) {
		t.Fatal("codeship color disabled")
	}
	os.Unsetenv("CI")
	t.Setenv("TEAMCITY_VERSION", "2024.1")
	if !chalkColorEnabled(false) {
		t.Fatal("teamcity color disabled")
	}
	t.Setenv("COLORTERM", "truecolor")
	if !chalkColorEnabled(false) {
		t.Fatal("truecolor disabled")
	}
	t.Setenv("TERM", "xterm-kitty")
	if !chalkColorEnabled(false) {
		t.Fatal("kitty disabled")
	}
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	if !chalkColorEnabled(false) {
		t.Fatal("iterm disabled")
	}
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"ianchor", "--no-color"}
	t.Setenv("FORCE_COLOR", "")
	os.Unsetenv("FORCE_COLOR")
	if !hasColorFlag("no-color") || chalkColorEnabled(true) {
		t.Fatal("no-color flag ignored")
	}
	os.Args = []string{"ianchor", "--color"}
	if !chalkColorEnabled(false) {
		t.Fatal("color flag ignored")
	}
	t.Setenv("FORCE_COLOR", "false")
	if chalkColorEnabled(true) {
		t.Fatal("false force accepted")
	}
	described := &Command{Name: "described", Args: []*Arg{{Name: "path", Desc: "source"}}, Action: func(*Context) error { return nil }}
	var describedHelp bytes.Buffer
	if code := Run(described, "v", []string{"-h"}, strings.NewReader(""), &describedHelp, &bytes.Buffer{}); code != 0 || !strings.Contains(describedHelp.String(), "Arguments:") {
		t.Fatalf("described help=%q", describedHelp.String())
	}
	helpRoot := &Command{Name: "root", Commands: []*Command{{Name: "child", Desc: "child"}}}
	var helpOut, helpErr bytes.Buffer
	if code := Run(helpRoot, "v", []string{"help"}, strings.NewReader(""), &helpOut, &helpErr); code != 0 || !strings.Contains(helpOut.String(), "Commands:") {
		t.Fatalf("help command out=%q err=%q", helpOut.String(), helpErr.String())
	}
	helpOut.Reset()
	if code := Run(helpRoot, "v", []string{"help", "child"}, strings.NewReader(""), &helpOut, &helpErr); code != 0 || !strings.Contains(helpOut.String(), "child") {
		t.Fatalf("child help=%q", helpOut.String())
	}
	savedArgs := append([]string{}, os.Args...)
	os.Args = []string{"ianchor", "--", "--color"}
	if hasColorFlag("color") {
		t.Fatal("color after separator accepted")
	}
	os.Args = savedArgs
	if !strings.Contains((&Option{Desc: "value", DefaultDisplay: "auto"}).description(), "default") {
		t.Fatal("combined option description mismatch")
	}
	if !strings.Contains((&Arg{Desc: "value", Default: "auto", HasDefault: true}).description(), "default") {
		t.Fatal("combined argument description mismatch")
	}
	clearColorEnv(t)
	t.Setenv("CI", "1")
	t.Setenv("GITEA_ACTIONS", "true")
	if chalkColorEnabled(true) == false {
		t.Fatal("gitea tty color disabled")
	}
	clearColorEnv(t)
	os.Args = []string{"ianchor-test"}
	if chalkColorEnabled(false) {
		t.Fatal("non-tty color accepted")
	}
	t.Setenv("CI", "1")
	if chalkColorEnabled(true) {
		t.Fatal("bare CI accepted")
	}
	t.Setenv("TF_BUILD", "1")
	t.Setenv("AGENT_NAME", "builder")
	if !chalkColorEnabled(false) {
		t.Fatal("Azure color disabled")
	}
	clearColorEnv(t)
	t.Setenv("TEAMCITY_VERSION", "2024.1")
	if !chalkColorEnabled(true) {
		t.Fatal("teamcity detector mismatch")
	}
	clearColorEnv(t)
	t.Setenv("TERM", "xterm-ghostty")
	if !chalkColorEnabled(true) {
		t.Fatal("ghostty disabled")
	}
	clearColorEnv(t)
	t.Setenv("TERM", "wezterm")
	if !chalkColorEnabled(true) {
		t.Fatal("wezterm disabled")
	}
	clearColorEnv(t)
	t.Setenv("TERM", "vt100")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	if !chalkColorEnabled(true) {
		t.Fatal("Apple Terminal disabled")
	}
	clearColorEnv(t)
	t.Setenv("TERM", "xterm-256color")
	if !chalkColorEnabled(true) {
		t.Fatal("256-color disabled")
	}
	savedColorArgs := os.Args
	os.Args = []string{"ianchor", "--color=256"}
	if !chalkColorEnabled(false) {
		t.Fatal("256 flag disabled")
	}
	os.Args = savedColorArgs
	if !strings.Contains(boxWrap("one\n\ntwo", 40), "one") {
		t.Fatal("blank wrap mismatch")
	}
	shortOnly := &Option{Short: "-x"}
	if shortOnly.key() != "x" || containsString(nil, "x") || !containsString([]string{"x"}, "x") {
		t.Fatal("key or contains mismatch")
	}
	if (&palette{enabled: true}).wrap("31", "39", "x") == "x" {
		t.Fatal("enabled color produced plain text")
	}
	if excess := (&session{}).excessMessage(&Command{Name: "root", Args: []*Arg{{Name: "one"}}}, 2); !strings.Contains(excess, "argument") {
		t.Fatal(excess)
	}
	if (&Command{Args: []*Arg{{Name: "plain"}}}).usageString() == "" || hasDescribedArgs(&Command{}) {
		t.Fatal("usage classification mismatch")
	}
	stop := watchTerminalResize(nil, func(int) {})
	stop()
	branchRoot := &Command{Name: "root", Commands: []*Command{{Name: "child", Options: []*Option{{Long: "--force"}}, Action: func(*Context) error { return nil }}}}
	var branchOut, branchErr bytes.Buffer
	if code := Run(branchRoot, "v", []string{"--force"}, strings.NewReader(""), &branchOut, &branchErr); code == 0 {
		t.Fatal("unknown root option accepted")
	}
	if code := Run(branchRoot, "v", []string{"child", "--force"}, strings.NewReader(""), &branchOut, &branchErr); code != 0 {
		t.Fatalf("child option rejected: %s", branchErr.String())
	}
	if code := Run(branchRoot, "v", []string{"child", "--missing=value"}, strings.NewReader(""), &branchOut, &branchErr); code == 0 || !strings.Contains(branchErr.String(), "unknown option") {
		t.Fatal("unknown equals option accepted")
	}
	if code := Run(&Command{Name: "leaf", Args: []*Arg{{Name: "value"}}, Action: func(*Context) error { return nil }}, "v", []string{"--", "-1"}, strings.NewReader(""), &branchOut, &branchErr); code != 0 {
		t.Fatal("separator operand rejected")
	}
	if suggestSimilar("--ab", []string{"--a"}) != "" || damerauLevenshtein("abcd", "wxyz", 3) <= 3 {
		t.Fatal("suggestion boundary mismatch")
	}
	editor := questionEditor{line: []rune("abc def"), cursor: 0}
	if editor.wordRight(true) == 0 {
		t.Fatal("word boundary missing")
	}
	var spinnerErr bytes.Buffer
	spinner := (&session{errOut: &spinnerErr}).StartSpinner()
	spinner.Stop()
	spinner.Stop()
	if answer, ok := (&session{out: &bytes.Buffer{}, in: bufio.NewReader(strings.NewReader("x"))}).Prompt("> "); ok || answer != "" {
		t.Fatal("partial prompt accepted")
	}
	cluster := &Command{Name: "leaf", Options: []*Option{{Short: "-n", Long: "--name", Value: "<name>"}, {Short: "-V", Long: "--version", IsVersion: true}}, Action: func(*Context) error { return nil }}
	if code := Run(cluster, "v", []string{"-nvalue"}, strings.NewReader(""), &branchOut, &branchErr); code != 0 {
		t.Fatal("attached value rejected")
	}
	if code, out, _ := runSample("-V", "ignored"); code != 0 || out != "1.1.1\n" {
		t.Fatalf("cluster version=%q", out)
	}
	os.Args = []string{"ianchor", "--color=truecolor"}
	if !chalkColorEnabled(false) {
		t.Fatal("truecolor flag disabled")
	}
	if suggestion := suggestSimilar("--", []string{"--a"}); suggestion != "" || damerauLevenshtein("ab", "ba", 3) != 1 {
		t.Fatalf("distance boundary mismatch: %q", suggestion)
	}
	var empty *Spinner
	empty.Stop()
	emptyRoot := &Command{Name: "empty"}
	if code := Run(emptyRoot, "v", nil, strings.NewReader(""), &branchOut, &branchErr); code != 0 {
		t.Fatal("empty command rejected")
	}
	parentCommand := &Command{Name: "parent", Commands: []*Command{{Name: "child"}}}
	if code := Run(parentCommand, "v", []string{"--unknown"}, strings.NewReader(""), &branchOut, &branchErr); code == 0 {
		t.Fatal("unknown parent option accepted")
	}
	if code, grouped, _ := runSample("-Vextra"); code != 0 || grouped != "1.1.1\n" {
		t.Fatalf("grouped version=%q", grouped)
	}
	clearColorEnv(t)
	t.Setenv("COLORTERM", "truecolor")
	if !chalkColorEnabled(true) {
		t.Fatal("colorterm disabled")
	}
	if (&session{}).helpWidth(&bytes.Buffer{}) != 80 {
		t.Fatal("default help width missing")
	}
	valued := &Command{Name: "leaf", Options: []*Option{{Long: "--name", Value: "<name>"}}, Action: func(ctx *Context) error { return nil }}
	if code := Run(valued, "v", []string{"--name=value"}, strings.NewReader(""), &branchOut, &branchErr); code != 0 {
		t.Fatal("equals value rejected")
	}
	bare := &Command{Name: "bare", Commands: []*Command{{Name: "child"}}}
	if code := Run(bare, "v", nil, strings.NewReader(""), &branchOut, &branchErr); code == 0 || !strings.Contains(branchErr.String(), "Usage") {
		t.Fatal("bare parent help missing")
	}
	if suggestSimilar("--x", []string{"--a", "", "--", "x", "--c"}) != "" {
		t.Fatal("single-character suggestion accepted")
	}
	errSession := &session{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	if code := errSession.parseCommand(&Command{Name: "empty", Commands: []*Command{{Name: "child"}}}, nil, nil); code == 0 || !strings.Contains(errSession.errOut.(*bytes.Buffer).String(), "Usage") {
		t.Fatal("error help missing")
	}
	helped := &Command{Name: "helped", Commands: []*Command{{Name: "child", Action: func(ctx *Context) error { return nil }}}}
	var helpedOut, helpedErr bytes.Buffer
	if code := Run(helped, "v", []string{"--help"}, strings.NewReader(""), &helpedOut, &helpedErr); code != 0 || !strings.Contains(helpedOut.String()+helpedErr.String(), "Usage") {
		t.Fatalf("parent help code=%d out=%q err=%q", code, helpedOut.String(), helpedErr.String())
	}
}
