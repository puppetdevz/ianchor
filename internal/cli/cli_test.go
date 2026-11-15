package cli

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Suggested candidates were captured from the Node baseline (commander v14
// suggestSimilar) so the wording and threshold stay identical.
func TestSuggestSimilar(t *testing.T) {
	optionLongs := []string{"--dir", "--name", "--force", "--json", "--yes", "--help", "--version"}
	cases := []struct {
		word       string
		candidates []string
		want       string
	}{
		{"--jsn", []string{"--json", "--help"}, "\n(Did you mean --json?)"},
		{"--forc", optionLongs, "\n(Did you mean --force?)"},
		{"--wat", optionLongs, ""},
		{"--json", []string{"--json"}, "\n(Did you mean --json?)"},
		{"--hel", []string{"--help"}, "\n(Did you mean --help?)"},
		{"--xyzzy", optionLongs, ""},
		{"--nam", []string{"--name", "--yes"}, "\n(Did you mean --name?)"},
		{"--versionn", []string{"--version"}, "\n(Did you mean --version?)"},
		{"--verydifferent", []string{"--version", "--verbose"}, ""},
	}
	for _, c := range cases {
		if got := suggestSimilar(c.word, c.candidates); got != c.want {
			t.Fatalf("suggestSimilar(%q) = %q, want %q", c.word, got, c.want)
		}
	}
	if got := suggestSimilar("lis", []string{"list", "init"}); got != "\n(Did you mean list?)" {
		t.Fatalf("command suggestion = %q", got)
	}
	if got := suggestSimilar("ab", []string{"ac", "ad"}); !strings.Contains(got, "one of") {
		t.Fatalf("multi suggestion = %q", got)
	}
	if got := suggestSimilar("x", []string{"x"}); got != "" {
		t.Fatalf("short candidates must be skipped: %q", got)
	}
}

func TestBoxWrap(t *testing.T) {
	if got := boxWrap("a b c", 100); got != "a b c" {
		t.Fatalf("no wrap = %q", got)
	}
	// Below commander's minimum wrapping width the text is left untouched.
	if got := boxWrap("aa bb cc dd", 20); got != "aa bb cc dd" {
		t.Fatalf("narrow width = %q", got)
	}
	input := "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii"
	if got := boxWrap(input, 40); got != "aaaa bbbb cccc dddd eeee ffff gggg hhhh\niiii" {
		t.Fatalf("wrap = %q", got)
	}
	if got := boxWrap("aa\n\nbb", 10); got != "aa\n\nbb" {
		t.Fatalf("blank lines = %q", got)
	}
	if got := boxWrap("aaaaaaaaaaaa", 5); got != "aaaaaaaaaaaa" {
		t.Fatalf("long word = %q", got)
	}
}

func TestPreformatted(t *testing.T) {
	if !preformatted("line\n  indented") {
		t.Fatal("expected preformatted")
	}
	if preformatted("plain description") {
		t.Fatal("unexpected preformatted")
	}
}

// syncBuffer is a concurrency-safe writer for the spinner goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestSpinnerContractConstants(t *testing.T) {
	if got := strings.Join(spinnerFrames, ""); got != "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏" {
		t.Fatalf("spinner frames = %q", got)
	}
	if spinnerDelay != 100*time.Millisecond || spinnerInterval != 80*time.Millisecond {
		t.Fatalf("spinner schedule = %v / %v", spinnerDelay, spinnerInterval)
	}
}

func TestSpinner(t *testing.T) {
	out := &syncBuffer{}
	s := &session{in: bufio.NewReader(strings.NewReader("")), out: out, errOut: out}
	spinner := s.StartSpinner()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), spinnerFrames[0]) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := out.String(); got != spinnerFrames[0]+" Loading entries…" {
		t.Fatalf("first frame = %q", got)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "\r") {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "\r"+spinnerFrames[1]) {
		t.Fatalf("second frame missing: %q", out.String())
	}
	spinner.Stop()
	if !strings.HasSuffix(out.String(), "\r\x1b[K") {
		t.Fatalf("cleanup missing: %q", out.String())
	}
	before := out.String()
	time.Sleep(150 * time.Millisecond)
	if out.String() != before {
		t.Fatal("spinner still running after Stop")
	}
}

func TestSpinnerFastPath(t *testing.T) {
	out := &syncBuffer{}
	s := &session{in: bufio.NewReader(strings.NewReader("")), out: out, errOut: out}
	s.StartSpinner().Stop()
	if out.String() != "" {
		t.Fatalf("short operation wrote animation: %q", out.String())
	}
}

func TestPrompt(t *testing.T) {
	out := &syncBuffer{}
	s := &session{in: bufio.NewReader(strings.NewReader("abc \n")), out: out, errOut: out}
	answer, ok := s.Prompt("ask: ")
	if !ok || answer != "abc " {
		t.Fatalf("prompt = %q %v", answer, ok)
	}
	if out.String() != "ask: " {
		t.Fatalf("prompt text = %q", out.String())
	}
	// EOF without input: the prompt is printed but nothing is read.
	out2 := &syncBuffer{}
	s2 := &session{in: bufio.NewReader(strings.NewReader("")), out: out2, errOut: out2}
	if _, ok := s2.Prompt("ask: "); ok {
		t.Fatal("expected EOF without answer")
	}
	if out2.String() != "ask: " {
		t.Fatalf("prompt text = %q", out2.String())
	}
}

func TestNullDeviceIsNotTerminal(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("/dev/null is a character device but not a terminal")
	}
}

func TestPaletteDisabled(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	p := newPalette()
	if p.enabled {
		t.Fatal("FORCE_COLOR=0 must disable colours")
	}
	if got := p.Red("x"); got != "x" {
		t.Fatalf("Red = %q", got)
	}
}
