// Package config locates the iCloud config directory and the small JSON file
// that remembers a custom directory name.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultDirName is the config directory used when the user did not pick one.
const DefaultDirName = "ianchor"

// stderr/config error texts shared with the CLI.
const (
	ErrICloudMissing = "iCloud Drive not found. Please ensure iCloud is enabled."
)

type fileConfig struct {
	DirName string `json:"dirName"`
}

// homeDir is os.UserHomeDir in production. Tests replace it to exercise
// config paths without changing the caller's real home directory.
var homeDir = os.UserHomeDir

// Home returns the current user's home directory.
func Home() (string, error) {
	return homeDir()
}

// ICloudBase returns the iCloud Drive mount point for the current user.
func ICloudBase() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs"), nil
}

// FilePath is where the config directory name is persisted.
func FilePath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ianchor.json"), nil
}

// Save persists dirName, creating ~/.config when needed.
func Save(dirName string) error {
	p, err := FilePath()
	if err != nil {
		return err
	}
	if err := mkdirConfig(filepath.Dir(p)); err != nil {
		return err
	}
	data, err := marshalConfig(fileConfig{DirName: dirName})
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(data, '\n'), 0o644)
}

// Load returns the persisted config, or an empty config when it is missing or
// unreadable. #nosec G304 - path is derived from the user's home directory.
func Load() fileConfig {
	p, err := FilePath()
	if err != nil {
		return fileConfig{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return fileConfig{}
	}
	var cfg fileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fileConfig{}
	}
	return cfg
}

// ValidateDirName rejects names that would escape the iCloud Drive root.
func ValidateDirName(name string) error {
	if err := validateComponent(name); err != nil {
		return fmt.Errorf("Config directory name %q must be a single path component inside iCloud Drive", name)
	}
	return nil
}

func validateComponent(name string) error {
	if name == "" || name == "." || name == ".." {
		return errors.New("empty or relative component")
	}
	if strings.ContainsRune(name, 0) || strings.ContainsRune(name, os.PathSeparator) || strings.ContainsRune(name, '/') || absoluteComponent(name) {
		return errors.New("contains path separator")
	}
	return nil
}

// GetConfigDir resolves base/dirName, falling back to the persisted name and
// then to DefaultDirName. The result is guaranteed to stay inside base.
func GetConfigDir(base, dirName string) (string, error) {
	if dirName == "" {
		dirName = Load().DirName
	}
	if dirName == "" {
		dirName = DefaultDirName
	}
	if err := ValidateDirName(dirName); err != nil {
		return "", err
	}
	dir := filepath.Join(base, dirName)
	if err := ensureWithin(base, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// EnsureConfigDir resolves the config directory and creates it when missing.
func EnsureConfigDir(base, dirName string) (string, error) {
	if _, err := os.Stat(base); err != nil {
		return "", errors.New(ErrICloudMissing)
	}
	dir, err := GetConfigDir(base, dirName)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// ensureWithin rejects paths whose symlink-aware location leaves base, which
// keeps a tampered config file from redirecting ianchor outside iCloud Drive.
// evalSymlinks is filepath.EvalSymlinks. "/" always resolves on Darwin, so
// the walk cannot observe a missing path whose parent is itself.
var evalSymlinks = filepath.EvalSymlinks

func ensureWithin(base, target string) error {
	realBase, err := evalSymlinks(base)
	if err != nil {
		// base itself is missing: nothing can escape through it yet.
		return nil
	}
	probe := target
	for {
		real, err := evalSymlinks(probe)
		if err == nil {
			if !within(realBase, real) {
				return fmt.Errorf("Config directory %q resolves outside iCloud Drive", target)
			}
			return nil
		}
		if unexpectedPathError(err) != nil {
			return err
		}
		parent := parentDir(probe)
		if parent == probe {
			return nil
		}
		probe = parent
	}
}

var mkdirConfig = func(path string) error { return os.MkdirAll(path, 0o755) }

var marshalConfig = func(cfg fileConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}

func absoluteComponent(name string) bool { return filepath.IsAbs(name) }

var parentDir = filepath.Dir

func unexpectedPathError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func within(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
