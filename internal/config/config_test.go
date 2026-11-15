package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDirName(t *testing.T) {
	valid := []string{"ianchor", "ianchor-beta", ".hidden", "a b", "配置", `a\b`}
	for _, name := range valid {
		if err := ValidateDirName(name); err != nil {
			t.Fatalf("%q rejected: %v", name, err)
		}
	}
	invalid := map[string]string{
		"":         "single path component",
		".":        "single path component",
		"..":       "single path component",
		"a/b":      "single path component",
		"/abs":     "single path component",
		"a\x00b":   "single path component",
		"ianchor/": "single path component",
	}
	for name, want := range invalid {
		err := ValidateDirName(name)
		if err == nil {
			t.Fatalf("%q accepted", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q error = %v", name, err)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Save("work"); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".config", "ianchor.json"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "{\n  \"dirName\": \"work\"\n}\n"
	if string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
	if got := Load().DirName; got != "work" {
		t.Fatalf("loaded %q", got)
	}
	// A broken file must behave like an absent one.
	if err := os.WriteFile(filepath.Join(home, ".config", "ianchor.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Load().DirName; got != "" {
		t.Fatalf("broken file loaded %q", got)
	}
}

func TestGetConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "icloud")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := GetConfigDir(base, "")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if got != filepath.Join(base, "ianchor") {
		t.Fatalf("default = %q", got)
	}
	if err := Save("custom"); err != nil {
		t.Fatal(err)
	}
	got, err = GetConfigDir(base, "")
	if err != nil || got != filepath.Join(base, "custom") {
		t.Fatalf("saved default = %q err=%v", got, err)
	}
	got, err = GetConfigDir(base, "explicit")
	if err != nil || got != filepath.Join(base, "explicit") {
		t.Fatalf("explicit = %q err=%v", got, err)
	}
	for _, name := range []string{"..", "../evil", "/tmp/evil"} {
		if _, err := GetConfigDir(base, name); err == nil {
			t.Fatalf("%q escaped the iCloud directory", name)
		}
	}
}

func TestGetConfigDirRejectsSymlinkEscape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "icloud")
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := GetConfigDir(base, "escape"); err == nil {
		t.Fatal("symlinked directory outside iCloud was accepted")
	} else if !strings.Contains(err.Error(), "outside iCloud Drive") {
		t.Fatalf("error = %v", err)
	}
}

func TestEnsureConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, "icloud")
	if _, err := EnsureConfigDir(base, "ianchor"); err == nil {
		t.Fatal("expected an error when iCloud Drive is missing")
	} else if err.Error() != ErrICloudMissing {
		t.Fatalf("error = %v", err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := EnsureConfigDir(base, "ianchor")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	if _, err := EnsureConfigDir(base, "ianchor"); err != nil {
		t.Fatalf("idempotent ensure: %v", err)
	}
}

func TestConfigHomeFailures(t *testing.T) {
	original := homeDir
	homeDir = func() (string, error) { return "", os.ErrPermission }
	t.Cleanup(func() { homeDir = original })
	if _, err := ICloudBase(); err == nil {
		t.Fatal("ICloudBase accepted a missing home")
	}
	if _, err := FilePath(); err == nil {
		t.Fatal("FilePath accepted a missing home")
	}
	if err := Save("work"); err == nil {
		t.Fatal("Save accepted a missing home")
	}
	if Load().DirName != "" {
		t.Fatal("Load returned a name without home")
	}
	home := t.TempDir()
	homeDir = func() (string, error) { return home, nil }
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "ianchor.json"), []byte("{}"), 0o000); err != nil {
		t.Fatal(err)
	}
	if Load().DirName != "" {
		t.Fatal("unreadable config returned a name")
	}
	base := filepath.Join(home, "icloud")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o700) })
	if _, err := EnsureConfigDir(base, "blocked"); err == nil {
		t.Fatal("EnsureConfigDir created a directory without permission")
	}
	homeDir = func() (string, error) { return home, nil }
	basePath, err := ICloudBase()
	if err != nil || !strings.Contains(basePath, "CloudDocs") {
		t.Fatalf("base=%q err=%v", basePath, err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o700) })
	if err := Save("work"); err == nil {
		t.Fatal("Save created config without home write permission")
	}
	if _, err := EnsureConfigDir(base, "../outside"); err == nil {
		t.Fatal("EnsureConfigDir accepted an escaping name")
	}
	if err := validateComponent("/abs"); err == nil {
		t.Fatal("absolute component accepted")
	}
	if err := ensureWithin(filepath.Join(home, "missing-base"), filepath.Join(home, "missing-base", "child")); err != nil {
		t.Fatalf("missing base err=%v", err)
	}
	if !within("/", "/") || within("/missing", "relative") {
		t.Fatal("within boundary mismatch")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := ensureWithin(missing, filepath.Join(missing, "child")); err != nil {
		t.Fatalf("missing base: %v", err)
	}
	if err := validateComponent("/abs"); err == nil {
		t.Fatal("absolute component accepted")
	}
	originalMkdir, originalMarshal := mkdirConfig, marshalConfig
	mkdirConfig = func(string) error { return os.ErrPermission }
	if err := Save("blocked"); err == nil {
		t.Fatal("Save ignored a directory error")
	}
	mkdirConfig = originalMkdir
	marshalConfig = func(fileConfig) ([]byte, error) { return nil, os.ErrInvalid }
	if err := Save("blocked"); err == nil {
		t.Fatal("Save ignored an encoding error")
	}
	marshalConfig = originalMarshal
	if !absoluteComponent("/abs") || absoluteComponent("rel") {
		t.Fatal("absolute component check mismatch")
	}
	if err := unexpectedPathError(os.ErrPermission); err == nil || unexpectedPathError(os.ErrNotExist) != nil {
		t.Fatal("path error classification mismatch")
	}
	rootBase := t.TempDir()
	if err := ensureWithin(rootBase, "/"); err == nil {
		t.Fatal("root target was accepted")
	}
	writable := t.TempDir()
	filePath := filepath.Join(writable, "file")
	if writeErr := os.WriteFile(filePath, []byte("x"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err := ensureWithin(writable, filepath.Join(writable, "file", "child")); err == nil {
		t.Fatal("path through a file was accepted")
	}
	if err := ensureWithin("/", "/"); err != nil {
		t.Fatalf("root within root: %v", err)
	}
	originalParent := parentDir
	parentDir = func(path string) string { return path }
	t.Cleanup(func() { parentDir = originalParent })
	if err := ensureWithin(writable, filepath.Join(writable, "missing-final")); err != nil {
		t.Fatalf("root stop: %v", err)
	}
}
