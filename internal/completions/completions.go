// Package completions embeds the zsh and bash completion scripts.
package completions

import (
	"embed"
	"fmt"
)

//go:embed assets/ianchor.bash assets/ianchor.zsh
var assets embed.FS

// Script returns the completion script for the given shell.
func Script(shell string) ([]byte, error) {
	return assets.ReadFile("assets/ianchor." + shell)
}

// Supported reports whether shell has an embedded script.
func Supported(shell string) bool {
	return shell == "zsh" || shell == "bash"
}

// UnsupportedError is the message commander's program.error() would print.
func UnsupportedError(shell string) error {
	return fmt.Errorf("Unsupported shell: %s. Expected zsh or bash.", shell)
}
