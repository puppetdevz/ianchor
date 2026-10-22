# iAnchor

Sync your config files to iCloud Drive via symlinks. Keep dotfiles and app configs in sync across all your Macs automatically.

[中文文档](README.md)

## How It Works

iAnchor moves config files/directories into an iCloud Drive directory, then creates symlinks at the original locations. iCloud syncs the files, and symlinks ensure your apps still find their configs.

## Install

```bash
brew tap puppetdevz/ianchor
brew install puppetdevz/ianchor/ianchor
```

## Upgrade

```bash
brew update
brew upgrade puppetdevz/ianchor/ianchor
```

## Usage

### Initialize

```bash
ianchor init
ianchor init --dir dotfiles  # custom directory name
```

### Add a config file

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

### List tracked configs

```bash
ianchor list
ianchor list --json
```

### Recover a config file

Restore a file or directory to its original location on this device only, replacing the symlink with an independent local copy. The iCloud file and database entry are preserved, so other devices keep working. Local edits no longer sync after recovery. `ianchor list` shows the local entry as `unlinked`; use `ianchor relink <name>` to rejoin syncing.

Cloud contents are still backed up to `.bak/` before recovery. Entries without an original path are rejected without deleting cloud data.

```bash
ianchor recover .zshrc
ianchor recover --force .zshrc  # overwrite existing file
```

### Relink an unlinked file

```bash
ianchor relink .zshrc
```

### Remove a tracked file

**This deletes the shared iCloud file and database entry, breaking links on other devices. Use `recover` to stop syncing on this device only.**

```bash
ianchor remove .zshrc
ianchor remove -y .zshrc  # skip confirmation
```

## License

MIT
