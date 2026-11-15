// Command ianchor syncs config files to iCloud Drive via symlinks.
package main

import (
	"os"

	ianchor "github.com/puppetdevz/ianchor"
	"github.com/puppetdevz/ianchor/internal/app"
	"github.com/puppetdevz/ianchor/internal/cli"
)

// exit is os.Exit in production. Tests replace it because calling os.Exit
// ends the test process before coverage for this wrapper can be recorded.
// The production path still terminates through os.Exit.
var exit = os.Exit

func main() {
	exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, in *os.File, out, errOut *os.File) int {
	return cli.Run(app.New(), ianchor.Version, args, in, out, errOut)
}
