// Package app wires the ianchor commands to the CLI layer.
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/puppetdevz/ianchor/internal/anchor"
	"github.com/puppetdevz/ianchor/internal/cli"
	"github.com/puppetdevz/ianchor/internal/completions"
	"github.com/puppetdevz/ianchor/internal/config"
	"github.com/puppetdevz/ianchor/internal/store"
	"github.com/puppetdevz/ianchor/internal/table"
)

// javascriptJSONSeparators matches JSON.stringify, which emits U+2028 and
// U+2029 literally. Process escape pairs so a literal \\u2028 stays escaped.
func javascriptJSONSeparators(input string) string {
	var out strings.Builder
	for i := 0; i < len(input); {
		if input[i] == '\\' && i+1 < len(input) {
			if strings.HasPrefix(input[i:], `\u2028`) {
				out.WriteRune('\u2028')
				i += 6
				continue
			}
			if strings.HasPrefix(input[i:], `\u2029`) {
				out.WriteRune('\u2029')
				i += 6
				continue
			}
			out.WriteString(input[i : i+2])
			i += 2
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.String()
}

// New builds the command tree.
func New() *cli.Command {
	return &cli.Command{
		Name: "ianchor",
		Desc: "Sync config files to iCloud Drive via symlinks",
		Options: []*cli.Option{
			{Short: "-V", Long: "--version", Desc: "output the version number", IsVersion: true},
		},
		Commands: []*cli.Command{
			{
				Name: "init",
				Desc: "Initialize ianchor: create iCloud config directory and database",
				Options: []*cli.Option{
					{Long: "--dir", Value: "<name>", Desc: "config directory name in iCloud Drive"},
				},
				Action: runInit,
			},
			{
				Name: "list",
				Desc: "List all tracked config files",
				Options: []*cli.Option{
					{Long: "--json", Desc: "output as JSON"},
				},
				Action: runList,
			},
			{
				Name: "add",
				Desc: "Add a file or directory to iCloud and create a symlink",
				Args: []*cli.Arg{{Name: "source", Required: true}},
				Options: []*cli.Option{
					{Long: "--name", Value: "<name>", Desc: "custom name in iCloud directory"},
					{Long: "--force", Desc: "overwrite if already exists", DefaultDisplay: "false"},
				},
				Action: runAdd,
			},
			{
				Name: "recover",
				Desc: "Restore a local copy without deleting the iCloud file or tracking entry",
				Args: []*cli.Arg{{Name: "name", Required: true}},
				Options: []*cli.Option{
					{Long: "--force", Desc: "overwrite if file exists at original location", DefaultDisplay: "false"},
				},
				Action: runRecover,
			},
			{
				Name:   "relink",
				Desc:   "Re-create symlink for an unlinked file in iCloud",
				Args:   []*cli.Arg{{Name: "name", Required: true}},
				Action: runRelink,
			},
			{
				Name: "remove",
				Desc: "Remove an entry from the database and delete the file from iCloud",
				Args: []*cli.Arg{{Name: "name", Required: true}},
				Options: []*cli.Option{
					{Short: "-y", Long: "--yes", Desc: "skip confirmation", DefaultDisplay: "false"},
				},
				Action: runRemove,
			},
			{
				Name:   "__complete",
				Hidden: true,
				Action: runComplete,
			},
			{
				Name:   "completion",
				Desc:   "Output shell completion script",
				Hidden: true,
				Args: []*cli.Arg{
					{Name: "shell", Desc: "shell type: zsh or bash", Default: "zsh", HasDefault: true},
				},
				Action: runCompletion,
			},
		},
	}
}

var openDBFunc = store.Open

func configDir() (string, error) {
	cfg := config.Load()
	dirName := cfg.DirName
	if dirName == "" {
		dirName = config.DefaultDirName
	}
	base, err := config.ICloudBase()
	if err != nil {
		return "", err
	}
	return config.GetConfigDir(base, dirName)
}

func openDB(dir string) (*store.DB, error) { return openDBFunc(dir) }

// listEntries is anchor.List. AllEntries returns a non-nil slice, so the nil
// JSON payload branch cannot be reached through the database.
var listEntries = anchor.List

// encodeListPayload uses the same encoder settings as list --json.
// encoding/json cannot fail for []store.Entry, so tests replace this to
// exercise the error return. Successful output stays byte-identical.
var encodeListPayload = func(payload any) (string, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(payload); err != nil {
		return "", err
	}
	return encoded.String(), nil
}

// entryAfterRelink is DB.GetEntry. Relink already read that row on the same
// handle, so the following lookup cannot fail through the public command.
var entryAfterRelink = func(db *store.DB, name string) (*store.Entry, error) {
	return db.GetEntry(name)
}

// completionScript is completions.Script. Supported is only zsh and bash, and
// those assets are embedded, so ReadFile cannot fail after the support check.
var completionScript = completions.Script

func runInit(ctx *cli.Context) error {
	icloudBase, err := config.ICloudBase()
	if err != nil {
		return err
	}
	dirName := ctx.String("dir")
	if dirName == "" {
		answer, ok := ctx.Prompt(fmt.Sprintf("iCloud config directory name %s: ", ctx.Dim("(default: ianchor)")))
		if !ok {
			return nil
		}
		dirName = strings.TrimSpace(answer)
		if dirName == "" {
			dirName = config.DefaultDirName
		}
	}
	if err := config.ValidateDirName(dirName); err != nil {
		return err
	}
	result, err := anchor.Init(icloudBase, dirName)
	if err != nil {
		return err
	}
	if err := config.Save(dirName); err != nil {
		return err
	}
	ctx.Println(ctx.Green("iAnchor initialized!"))
	ctx.Printf("Config directory: %s\n", result.ConfigDir)
	if result.Discovered > 0 {
		ctx.Printf("Discovered %d existing file(s).\n", result.Discovered)
	}
	if len(result.Entries) > 0 {
		ctx.Println("\nTracked entries:")
		for _, entry := range result.Entries {
			original := ctx.Dim("(unknown)")
			if entry.OriginalPath != nil {
				original = *entry.OriginalPath
			}
			ctx.Printf("  %s -> %s\n", entry.Name, original)
		}
	}
	return nil
}

func runList(ctx *cli.Context) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	db, err := openDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	spinner := ctx.Spinner()
	entries, err := listEntries(db, dir)
	spinner.Stop()
	if err != nil {
		return err
	}
	if ctx.Bool("json") {
		payload := entries
		if payload == nil {
			payload = []store.Entry{}
		}
		encoded, err := encodeListPayload(payload)
		if err != nil {
			return err
		}
		ctx.Printf("%s", javascriptJSONSeparators(encoded))
		return nil
	}
	if len(entries) == 0 {
		ctx.Println(`No entries found. Use "ianchor add <path>" to add config files.`)
		return nil
	}
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		original := ctx.Dim("(unknown)")
		if entry.OriginalPath != nil {
			original = *entry.OriginalPath
		}
		rows = append(rows, []string{entry.Name, original, entry.Type, formatStatus(ctx, entry.Status)})
	}
	head := []string{ctx.Cyan("Name"), ctx.Cyan("Original Path"), ctx.Cyan("Type"), ctx.Cyan("Status")}
	ctx.Printf("%s", table.Render(head, rows))
	return nil
}

func formatStatus(ctx *cli.Context, status string) string {
	switch status {
	case "linked":
		return ctx.Green("linked")
	case "deleted":
		return ctx.Red(`deleted (use "ianchor remove" to clean up)`)
	default:
		return ctx.Yellow("unlinked")
	}
}

func runAdd(ctx *cli.Context) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	db, err := openDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := anchor.Add(db, anchor.AddOptions{
		ConfigDir: dir,
		Source:    ctx.Args[0],
		Name:      ctx.String("name"),
		Force:     ctx.Bool("force"),
		OnConflict: func(destName string) (string, error) {
			answer, ok := ctx.Prompt(
				ctx.Yellow(fmt.Sprintf("\"%s\" already exists. ", destName)) +
					fmt.Sprintf("Enter a prefix %s: ", ctx.Dim(fmt.Sprintf("(e.g. abc -> abc_%s)", destName))))
			if !ok {
				return "", ctx.PromptError()
			}
			return strings.TrimSpace(answer), nil
		},
	})
	if err != nil {
		return err
	}
	ctx.Println(ctx.Green(fmt.Sprintf("Added \"%s\" to iCloud.", result.Name)))
	ctx.Printf("  %s -> %s/%s\n", result.OriginalPath, dir, result.Name)
	return nil
}

func runRecover(ctx *cli.Context) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	db, err := openDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := anchor.Recover(db, dir, ctx.Args[0], ctx.Bool("force"))
	if err != nil {
		return err
	}
	ctx.Println(ctx.Green(fmt.Sprintf("Recovered \"%s\" locally.", result.Name)))
	ctx.Println("iCloud file and tracking entry preserved; other devices can keep using them.")
	if result.OriginalPath != "" {
		ctx.Printf("  Restored to: %s\n", result.OriginalPath)
	}
	return nil
}

func runRelink(ctx *cli.Context) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	db, err := openDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	name := ctx.Args[0]
	if err := anchor.Relink(db, dir, name); err != nil {
		return err
	}
	entry, err := entryAfterRelink(db, name)
	if err != nil {
		return err
	}
	ctx.Println(ctx.Green(fmt.Sprintf("Relinked \"%s\".", name)))
	if entry != nil && entry.OriginalPath != nil {
		ctx.Printf("  %s -> %s\n", *entry.OriginalPath, filepath.Join(dir, name))
	}
	return nil
}

func runRemove(ctx *cli.Context) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	db, err := openDB(dir)
	if err != nil {
		return err
	}
	defer db.Close()
	name := ctx.Args[0]
	if !ctx.Bool("yes") {
		answer, ok := ctx.Prompt(ctx.Yellow(fmt.Sprintf(
			"Remove \"%s\"? This will delete the file from iCloud and the database record. [y/N] ", name)))
		if !ok {
			return ctx.PromptError()
		}
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			ctx.Println("Cancelled.")
			return nil
		}
	}
	result, err := anchor.Remove(db, dir, name)
	if err != nil {
		return err
	}
	ctx.Println(ctx.Green(fmt.Sprintf("Removed \"%s\".", result.Name)))
	if result.OriginalPath != nil && *result.OriginalPath != "" {
		ctx.Printf("  Original path was: %s\n", *result.OriginalPath)
	}
	return nil
}

func runComplete(ctx *cli.Context) error {
	dir, err := configDir()
	if err != nil {
		return nil
	}
	db, err := store.OpenReadOnly(dir)
	if err != nil {
		return nil
	}
	defer db.Close()
	entries, err := db.AllEntries()
	if err != nil {
		return nil
	}
	var out strings.Builder
	for _, entry := range entries {
		if strings.ContainsAny(entry.Name, "\x00\n\r") {
			continue
		}
		out.WriteString(entry.Name)
		out.WriteString("\x00")
	}
	fmt.Fprint(ctx.Out(), out.String())
	return nil
}

func runCompletion(ctx *cli.Context) error {
	shell := ctx.Args[0]
	if !completions.Supported(shell) {
		return cli.PlainError(completions.UnsupportedError(shell).Error())
	}
	script, err := completionScript(shell)
	if err != nil {
		return err
	}
	_, err = ctx.Out().Write(script)
	return err
}
