# iAnchor

Sync your config files to iCloud Drive via symlinks. Share dotfiles and app configs after setting up the directory and links on each Mac.

[中文文档](README.md) · [Changelog](CHANGELOG.en.md)

## How It Works

iAnchor moves config files/directories into an iCloud Drive directory, then creates symlinks at the original locations. iCloud syncs the files, and symlinks ensure your apps still find their configs.

## Install

Requires macOS, enabled iCloud Drive, and Homebrew.

```bash
brew tap puppetdevz/puppetdevz
brew install puppetdevz/puppetdevz/ianchor
```

This personal tap is hosted at [puppetdevz/homebrew-puppetdevz](https://github.com/puppetdevz/homebrew-puppetdevz).

## Upgrade

For installations already using the new tap:

```bash
brew update
brew upgrade puppetdevz/puppetdevz/ianchor
```

### Migrate from an old tap

If you installed from `vissong/ianchor`, switch the installation source first rather than just changing the upgrade command:

```bash
brew uninstall ianchor
brew untap vissong/ianchor
brew tap puppetdevz/puppetdevz
brew install puppetdevz/puppetdevz/ianchor
```

If your old tap was `puppetdevz/ianchor`, replace the untap command above with `brew untap puppetdevz/ianchor`; remove only the old tap you actually used.

Migration only changes the Homebrew installation source. The command remains `ianchor`, installed under `$(brew --prefix)/Cellar/ianchor/<version>`. Uninstalling the program does not delete the iCloud sync directory, tracking database, or `~/.config/ianchor.json`. There is no need to run `recover` or initialize again.

## Usage

### Initialize

```bash
ianchor init
ianchor init --dir dotfiles  # custom directory name
```

Without `--dir`, initialization prompts for a directory name; press Enter for `ianchor`. The default sync directory is `~/Library/Mobile Documents/com~apple~CloudDocs/ianchor/`, containing `ianchor.db`. The directory choice is saved locally in `~/.config/ianchor.json`.

Initialization discovers and registers existing cloud configs but does not create local symlinks. Initialize other devices too, then run `relink` for entries with a known original path.

### Add a config file

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

Unless `--name` is provided, the leading dot is stripped: `~/.zshrc` is tracked as `zshrc`. Use the names shown by `ianchor list` in subsequent commands; existing entries are not renamed automatically.

### List tracked configs

```bash
ianchor list
ianchor list --json
```

### Recover a config file

Restore a file or directory to its original location on this device only, replacing the symlink with an independent local copy. The iCloud file and database entry are preserved, so other devices keep working. Local edits no longer sync after recovery. `ianchor list` shows the local entry as `unlinked`; use `ianchor relink <name>` to rejoin syncing.

Cloud contents are backed up to `<sync-directory>/.bak/<name>` before recovery, replacing any previous backup of the same name. `--force` deletes an existing local file or directory at the original path; this backup preserves cloud contents, not the overwritten local contents. Entries without an original path are rejected without deleting cloud data.

```bash
ianchor recover zshrc
ianchor recover --force zshrc  # overwrite existing file
```

### Relink an unlinked file

Re-create the symlink to the cloud copy. Requires an existing tracking entry, original path, and cloud contents. An existing local file or directory is first renamed to `<original-path>.bak`; local changes are not merged.

```bash
ianchor relink zshrc
```

### Remove a tracked file

**This deletes the shared iCloud file and database entry, breaking links on other devices. Use `recover` to stop syncing on this device only.**

```bash
ianchor remove zshrc
ianchor remove -y zshrc  # skip confirmation
```

## Shell Completion

Generate a zsh or bash completion script, including tracked entry names:

```bash
ianchor completion zsh
ianchor completion bash
```

## License

`package.json` declares the MIT license; this repository does not yet include the `LICENSE` text.
