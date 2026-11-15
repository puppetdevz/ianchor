// Package known maps well-known config file names to the path they normally
// live at, so `ianchor init` can rediscover files it already moved.
package known

import (
	"os"
	"path/filepath"
)

// userHome is os.UserHomeDir in production.
var userHome = os.UserHomeDir

// Guess returns the conventional home for name, or nil when it is unknown.
func Guess(name string) *string {
	home, err := userHome()
	if err != nil {
		return nil
	}
	at := func(parts ...string) *string {
		p := filepath.Join(append([]string{home}, parts...)...)
		return &p
	}
	switch name {
	case ".zshrc", ".bashrc", ".bash_profile", ".zprofile", ".gitconfig",
		".vimrc", ".tmux.conf", ".ssh", ".config":
		return at(name)
	case "alacritty", "karabiner", "nvim":
		return at(".config", name)
	case "starship.toml":
		return at(".config", "starship.toml")
	case "Code":
		return at("Library", "Application Support", "Code", "User")
	}
	return nil
}
