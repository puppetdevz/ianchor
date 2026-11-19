package table

import (
	"os"
	"strings"
	"testing"
)

// golden is produced by cli-table3@0.6.5 with default options (see
// testdata/table.golden), which is the renderer the Node baseline used.
func TestRenderMatchesCliTable3(t *testing.T) {
	withoutColorEnv(t)
	t.Setenv("TERM", "xterm-256color")
	data, err := os.ReadFile("testdata/table.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	cases := map[string]struct {
		headers []string
		rows    [][]string
	}{
		"list": {
			headers: []string{"Name", "Original Path", "Type", "Status"},
			rows: [][]string{
				{".zshrc", "/tmp/ianchor-probe/home/.zshrc", "file", "linked"},
				{"deleted-entry", "(unknown)", "directory", `deleted (use "ianchor remove" to clean up)`},
				{"unknown-entry", "(unknown)", "directory", "deleted"},
				{"中文 名称", "(unknown)", "file", "deleted"},
			},
		},
		"ascii": {
			headers: []string{"A", "Bee"},
			rows:    [][]string{{"1", "2"}, {"three", "four"}},
		},
		"multiline": {
			headers: []string{"H"},
			rows:    [][]string{{"line one\nline two"}},
		},
	}
	sections := strings.Split(string(data), "== ")
	for _, section := range sections {
		if section == "" {
			continue
		}
		name, body, ok := strings.Cut(section, " ==\n")
		if !ok {
			t.Fatalf("malformed golden section %q", section)
		}
		c, ok := cases[name]
		if !ok {
			t.Fatalf("unexpected golden section %q", name)
		}
		want := strings.TrimSuffix(body, "\n")
		got := strings.TrimSuffix(Render(c.headers, c.rows), "\n")
		if got != want {
			t.Fatalf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
}

func TestRenderEmptyRows(t *testing.T) {
	got := Render([]string{"Name", "Status"}, nil)
	if !strings.Contains(got, "Name") || !strings.Contains(got, "Status") {
		t.Fatalf("header missing: %q", got)
	}
	if lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n"); len(lines) != 3 {
		t.Fatalf("expected top/header/bottom, got %d lines: %q", len(lines), got)
	}
}

func TestColorEnvironmentBranches(t *testing.T) {
	env := []string{"FORCE_COLOR", "CI", "TRAVIS", "CIRCLECI", "APPVEYOR", "GITLAB_CI", "CI_NAME", "TEAMCITY_VERSION", "TERM_PROGRAM", "TERM", "COLORTERM"}
	for _, key := range env {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	if colorsEnabled() {
		t.Fatal("colors enabled without a terminal signal")
	}
	t.Setenv("FORCE_COLOR", "0")
	if colorsEnabled() || border("│") != "│" {
		t.Fatal("FORCE_COLOR=0 did not disable colors")
	}
	t.Setenv("FORCE_COLOR", "1")
	if !colorsEnabled() || border("│") == "│" {
		t.Fatal("FORCE_COLOR=1 did not enable colors")
	}
	os.Unsetenv("FORCE_COLOR")
	t.Setenv("CI", "1")
	if colorsEnabled() {
		t.Fatal("generic CI enabled colors")
	}
	t.Setenv("FORCE_COLOR", "nope")
	for _, key := range []string{"TRAVIS", "CIRCLECI", "APPVEYOR", "GITLAB_CI"} {
		t.Setenv(key, "1")
		if !colorsEnabled() {
			t.Fatalf("%s did not enable colors", key)
		}
		os.Unsetenv(key)
	}
	t.Setenv("CI_NAME", "codeship")
	if !colorsEnabled() {
		t.Fatal("codeship did not enable colors")
	}
	os.Unsetenv("CI")
	os.Unsetenv("CI_NAME")
	os.Unsetenv("FORCE_COLOR")
	t.Setenv("TEAMCITY_VERSION", "9.0")
	if colorsEnabled() {
		t.Fatal("old TeamCity enabled colors")
	}
	t.Setenv("TEAMCITY_VERSION", "9.1.0")
	if !colorsEnabled() {
		t.Fatal("TeamCity 9.1 did not enable colors")
	}
	os.Unsetenv("TEAMCITY_VERSION")
	for _, program := range []string{"iTerm.app", "Hyper", "Apple_Terminal"} {
		t.Setenv("TERM_PROGRAM", program)
		if !colorsEnabled() {
			t.Fatalf("%s did not enable colors", program)
		}
	}
	os.Unsetenv("TERM_PROGRAM")
	t.Setenv("TERM", "xterm-256color")
	if !colorsEnabled() {
		t.Fatal("TERM did not enable colors")
	}
	os.Unsetenv("TERM")
	t.Setenv("COLORTERM", "truecolor")
	if !colorsEnabled() {
		t.Fatal("COLORTERM did not enable colors")
	}
	got := Render([]string{"Name"}, [][]string{{"value"}})
	if !strings.Contains(got, "Name") {
		t.Fatalf("colored render=%q", got)
	}
	if Render(nil, nil) != "" {
		t.Fatal("empty headers rendered a table")
	}
	long := Render([]string{"N"}, [][]string{{"too-long"}})
	if !strings.Contains(long, "too-long") {
		t.Fatalf("long cell=%q", long)
	}
	if got := decorate("too-long", 2, true); !strings.Contains(got, "too-long") {
		t.Fatalf("decorated=%q", got)
	}
}
