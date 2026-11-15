package ianchor

import (
	"regexp"
	"strings"
	"testing"
)

// TestVersionDerivesFromEmbeddedFile checks that the exported version is the
// embedded VERSION file with surrounding whitespace removed, so the release
// workflow keeps a single source of truth.
func TestVersionDerivesFromEmbeddedFile(t *testing.T) {
	if versionFile == "" {
		t.Fatal("embedded VERSION file is empty")
	}
	if Version != strings.TrimSpace(versionFile) {
		t.Fatalf("Version = %q, want trimmed %q", Version, strings.TrimSpace(versionFile))
	}
	if strings.TrimSpace(Version) != Version {
		t.Fatalf("Version %q has surrounding whitespace", Version)
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version %q is not a semantic version", Version)
	}
}
