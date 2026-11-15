package cli

import (
	"os"
	"regexp"
	"strings"
)

var chalkIntegerPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)
var chalkTeamCity = regexp.MustCompile(`^(9\.(0*[1-9]\d*)\.|\d{2,}\.)`)
var chalkTerminal = regexp.MustCompile(`(?i)^screen|^xterm|^vt100|^vt220|^rxvt|color|ansi|cygwin|linux`)
var chalk256 = regexp.MustCompile(`(?i)-256(color)?$`)

func hasColorFlag(flags ...string) bool {
	for _, argument := range os.Args[1:] {
		if argument == "--" {
			return false
		}
		for _, flag := range flags {
			if argument == "--"+flag {
				return true
			}
		}
	}
	return false
}
func envPresent(key string) bool { _, ok := os.LookupEnv(key); return ok }

// This is the boolean subset of the pinned Chalk supports-color detector.
// Chalk and cli-table3 use different detectors in the legacy distribution.
func chalkColorEnabled(tty bool) bool {
	provided, minimum, zero := false, false, false
	if hasColorFlag("no-color", "no-colors", "color=false", "color=never") {
		provided, zero = true, true
	} else if hasColorFlag("color", "colors", "color=true", "color=always") {
		provided, minimum = true, true
	}
	if value, ok := os.LookupEnv("FORCE_COLOR"); ok {
		provided = true
		switch value {
		case "false":
			minimum, zero = false, true
		case "true", "":
			minimum, zero = true, false
		default:
			digits := chalkIntegerPrefix.FindString(strings.TrimSpace(value))
			digits = strings.TrimLeft(strings.TrimLeft(digits, "+-"), "0")
			// parseInt's NaN isn't zero, but is falsy when computing the minimum.
			minimum = digits != ""
			zero = chalkIntegerPrefix.MatchString(strings.TrimSpace(value)) && !minimum
		}
	}
	if zero {
		return false
	}
	if hasColorFlag("color=16m", "color=full", "color=truecolor", "color=256") {
		return true
	}
	if envPresent("TF_BUILD") && envPresent("AGENT_NAME") {
		return true
	}
	if !tty && !provided {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return minimum
	}
	if envPresent("CI") {
		for _, key := range []string{"GITHUB_ACTIONS", "GITEA_ACTIONS", "CIRCLECI", "TRAVIS", "APPVEYOR", "GITLAB_CI", "BUILDKITE", "DRONE"} {
			if envPresent(key) {
				return true
			}
		}
		return minimum || os.Getenv("CI_NAME") == "codeship"
	}
	if value, ok := os.LookupEnv("TEAMCITY_VERSION"); ok {
		return chalkTeamCity.MatchString(value)
	}
	if os.Getenv("COLORTERM") == "truecolor" {
		return true
	}
	switch os.Getenv("TERM") {
	case "xterm-kitty", "xterm-ghostty", "wezterm":
		return true
	}
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "Apple_Terminal":
		return true
	}
	return chalk256.MatchString(os.Getenv("TERM")) || chalkTerminal.MatchString(os.Getenv("TERM")) || envPresent("COLORTERM") || minimum
}
