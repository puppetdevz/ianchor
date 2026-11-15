package table

import (
	"os"
	"strings"
	"testing"
)

func withoutColorEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"FORCE_COLOR", "CI", "TRAVIS", "CIRCLECI", "APPVEYOR", "GITLAB_CI",
		"CI_NAME", "TEAMCITY_VERSION", "TERM_PROGRAM", "TERM", "COLORTERM", "NO_COLOR",
	}
	saved := make(map[string]*string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			copy := value
			saved[key] = &copy
		}
		os.Unsetenv(key)
	}
	t.Cleanup(func() {
		for _, key := range keys {
			if value := saved[key]; value != nil {
				os.Setenv(key, *value)
			} else {
				os.Unsetenv(key)
			}
		}
	})
}

func TestRenderEmptyHeaders(t *testing.T) {
	if got := Render(nil, [][]string{{"cell"}}); got != "" {
		t.Fatalf("empty headers rendered %q", got)
	}
}

func TestDecorateClampsNegativePad(t *testing.T) {
	got := decorate("wide", 0, false)
	if got != " wide " {
		t.Fatalf("padded cell = %q", got)
	}
}

func TestBorderFollowsColorEnvironment(t *testing.T) {
	withoutColorEnv(t)
	if border("│") != "│" {
		t.Fatal("border stayed colored with colors disabled")
	}
	plain := Render([]string{"H"}, [][]string{{"wide"}})
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("plain table contains color: %q", plain)
	}
	if !strings.Contains(plain, "wide") {
		t.Fatalf("plain table = %q", plain)
	}

	t.Setenv("FORCE_COLOR", "0")
	if colorsEnabled() || border("│") != "│" {
		t.Fatal("FORCE_COLOR=0 enabled borders")
	}

	os.Unsetenv("FORCE_COLOR")
	t.Setenv("FORCE_COLOR", "nope")
	if !colorsEnabled() {
		t.Fatal("non-numeric FORCE_COLOR did not force color")
	}
	if !strings.Contains(border("│"), borderOpen) {
		t.Fatal("forced border lost its color")
	}
}

func TestColorsEnabledCIProviders(t *testing.T) {
	providers := []string{"TRAVIS", "CIRCLECI", "APPVEYOR", "GITLAB_CI"}
	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			withoutColorEnv(t)
			t.Setenv("CI", "1")
			t.Setenv(provider, "1")
			if !colorsEnabled() {
				t.Fatalf("%s did not enable colors", provider)
			}
		})
	}

	t.Run("codeship", func(t *testing.T) {
		withoutColorEnv(t)
		t.Setenv("CI", "1")
		t.Setenv("CI_NAME", "codeship")
		if !colorsEnabled() {
			t.Fatal("codeship did not enable colors")
		}
	})
	t.Run("ci-only", func(t *testing.T) {
		withoutColorEnv(t)
		t.Setenv("CI", "1")
		if colorsEnabled() {
			t.Fatal("CI alone enabled colors")
		}
	})
	t.Run("ci-forced", func(t *testing.T) {
		withoutColorEnv(t)
		t.Setenv("CI", "1")
		t.Setenv("FORCE_COLOR", "1")
		if !colorsEnabled() {
			t.Fatal("forced CI color was ignored")
		}
	})
}

func TestColorsEnabledTerminalHints(t *testing.T) {
	for _, version := range []struct {
		value string
		want  bool
	}{
		{"9.1.0", true},
		{"10.0", true},
		{"9.0.1", false},
	} {
		t.Run("teamcity-"+version.value, func(t *testing.T) {
			withoutColorEnv(t)
			t.Setenv("TEAMCITY_VERSION", version.value)
			if got := colorsEnabled(); got != version.want {
				t.Fatalf("TEAMCITY_VERSION=%s enabled=%v", version.value, got)
			}
		})
	}
	for _, program := range []string{"iTerm.app", "Hyper", "Apple_Terminal"} {
		t.Run(program, func(t *testing.T) {
			withoutColorEnv(t)
			t.Setenv("TERM_PROGRAM", program)
			if !colorsEnabled() {
				t.Fatalf("%s did not enable colors", program)
			}
		})
	}
	t.Run("colorterm", func(t *testing.T) {
		withoutColorEnv(t)
		t.Setenv("TERM", "dumb")
		t.Setenv("COLORTERM", "truecolor")
		if !colorsEnabled() {
			t.Fatal("COLORTERM did not enable colors")
		}
	})
	t.Run("none", func(t *testing.T) {
		withoutColorEnv(t)
		t.Setenv("TERM", "dumb")
		if colorsEnabled() {
			t.Fatal("dumb terminal enabled colors")
		}
	})
}
