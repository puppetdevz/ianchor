package known

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := map[string]string{
		".zshrc":        filepath.Join(home, ".zshrc"),
		".bashrc":       filepath.Join(home, ".bashrc"),
		".bash_profile": filepath.Join(home, ".bash_profile"),
		".zprofile":     filepath.Join(home, ".zprofile"),
		".vimrc":        filepath.Join(home, ".vimrc"),
		".tmux.conf":    filepath.Join(home, ".tmux.conf"),
		".config":       filepath.Join(home, ".config"),
		"karabiner":     filepath.Join(home, ".config", "karabiner"),
		".gitconfig":    filepath.Join(home, ".gitconfig"),
		".ssh":          filepath.Join(home, ".ssh"),
		"alacritty":     filepath.Join(home, ".config", "alacritty"),
		"nvim":          filepath.Join(home, ".config", "nvim"),
		"starship.toml": filepath.Join(home, ".config", "starship.toml"),
		"Code":          filepath.Join(home, "Library", "Application Support", "Code", "User"),
	}
	for name, want := range cases {
		got := Guess(name)
		if got == nil {
			t.Fatalf("%s: no guess", name)
		}
		if *got != want {
			t.Fatalf("%s = %q, want %q", name, *got, want)
		}
	}
	for _, name := range []string{"unknown", "foo.txt", "Zshrc", ""} {
		if got := Guess(name); got != nil {
			t.Fatalf("%q = %q, want nil", name, *got)
		}
	}
	original := userHome
	userHome = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { userHome = original })
	if got := Guess(".zshrc"); got != nil {
		t.Fatalf("missing home = %v", *got)
	}
}
