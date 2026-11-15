package anchor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/puppetdevz/ianchor/internal/fsx"
)

// These scenarios add the branch-specific copy/compare/permission/cleanup
// boundaries to runInjection's exhaustive one-failure-at-a-time state checks.
func TestInjectionCrossVolume(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%t", directory), func(t *testing.T) {
			runInjection(t, injection{
				name: "add EXDEV", crossVolume: true,
				seed: func(w *world) {
					path := filepath.Join(w.localDir, "item")
					if directory {
						path = filepath.Join(path, "child")
					}
					writeFile(t, path, "cloud\n")
				},
				run: func(w *world) error {
					_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: filepath.Join(w.localDir, "item")})
					return err
				},
				verify: func(t *testing.T, w *world) {
					assertSymlink(t, filepath.Join(w.localDir, "item"), filepath.Join(w.configDir, "item"))
					path := filepath.Join(w.configDir, "item")
					if directory {
						path = filepath.Join(path, "child")
					}
					if readFile(t, path) != "cloud\n" {
						t.Fatal("cross-volume content lost")
					}
				},
			})
		})
	}
}

func TestInjectionRecoverDirectory(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%t", force), func(t *testing.T) {
			runInjection(t, injection{
				name: "recover directory", directory: true,
				seed: func(w *world) {
					source := filepath.Join(w.localDir, ".zshrc")
					writeFile(t, filepath.Join(source, "child"), "cloud\n")
					if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
						t.Fatal(err)
					}
					writeFile(t, filepath.Join(w.configDir, bakDirName, "zshrc", "child"), "previous backup\n")
					if force {
						if err := os.Remove(source); err != nil {
							t.Fatal(err)
						}
						writeFile(t, filepath.Join(source, "local-only"), "local edit\n")
					}
				},
				run: func(w *world) error { _, err := Recover(w.db, w.configDir, "zshrc", force); return err },
				verify: func(t *testing.T, w *world) {
					for _, path := range []string{filepath.Join(w.localDir, ".zshrc", "child"), filepath.Join(w.configDir, "zshrc", "child")} {
						if readFile(t, path) != "cloud\n" {
							t.Fatal("directory recovery content lost")
						}
					}
					if force {
						assertMissing(t, filepath.Join(w.localDir, ".zshrc", "local-only"))
					}
				},
			})
		})
	}
}

func TestInjectionRecoverConflictCleanup(t *testing.T) {
	runInjection(t, injection{
		name:          "recover conflict",
		expectedError: true,
		seed: func(w *world) {
			source := w.write(".zshrc", "cloud\n")
			if _, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source}); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			writeFile(t, source, "local edit\n")
			writeFile(t, filepath.Join(w.configDir, bakDirName, "zshrc"), "previous backup\n")
		},
		run: func(w *world) error { _, err := Recover(w.db, w.configDir, "zshrc", false); return err },
		verify: func(t *testing.T, w *world) {
			if readFile(t, filepath.Join(w.localDir, ".zshrc")) != "local edit\n" {
				t.Fatal("local conflict changed")
			}
			if readFile(t, filepath.Join(w.configDir, bakDirName, "zshrc")) != "cloud\n" {
				t.Fatal("conflict backup not refreshed")
			}
		},
	})
}

func TestCrossVolumeSecondCopyInterruption(t *testing.T) {
	w := newWorld(t)
	source := filepath.Join(w.localDir, "item")
	writeFile(t, filepath.Join(source, "child"), "cloud\n")
	forced, calls := false, 0
	opHook = func(operation, path string) error {
		if operation == "rename" && !forced {
			forced = true
			return syscall.EXDEV
		}
		return nil
	}
	fsx.TestCopyFault = func(src, dst string) error {
		if strings.Contains(dst, "-install") {
			calls++
			return errors.New("injected second copy failure")
		}
		return nil
	}
	defer func() { opHook = nil; fsx.TestCopyFault = nil }()
	_, err := Add(w.db, AddOptions{ConfigDir: w.configDir, Source: source})
	if err == nil || !strings.Contains(err.Error(), "injected second copy failure") {
		t.Fatalf("%v calls=%d", err, calls)
	}
	if calls != 1 {
		t.Fatalf("second copy calls = %d", calls)
	}
	if _, statErr := os.Lstat(filepath.Join(w.configDir, "item")); !os.IsNotExist(statErr) {
		t.Fatalf("cloud residue: %v", statErr)
	}
	if readFile(t, filepath.Join(source, "child")) != "cloud\n" {
		t.Fatal("source changed")
	}
}
