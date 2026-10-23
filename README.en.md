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

The Formula declares only `node` as a direct dependency and binds the launcher to Homebrew Node, independent of nvm or other PATH settings. `better-sqlite3` requires a prebuilt binary matching that Node ABI, macOS and architecture. Download/load failures stop installation: there is no source-compilation fallback or extra Python declaration. Node may still have transitive dependencies, and installation requires network access. A Homebrew Node ABI upgrade can invalidate an already-installed native module; reinstall to fetch a matching prebuild. If none exists, report the error rather than compiling from source.

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

## Release maintenance

The source repository's `Formula/ianchor.rb` is the authoritative placeholder template, not a directly installable Formula. A stable `vMAJOR.MINOR.PATCH` tag must match `package.json`. Actions renders the template from that tagged commit with the source archive SHA256 and syncs the complete Formula to the tap's `main`. Keep installation changes in the source template, not in tap-only edits. Prerelease tags are rejected.

Before the first release, prepare an existing `puppetdevz/homebrew-puppetdevz` repository with at least one commit on `main`, and configure `HOMEBREW_TAP_TOKEN` in the source repository with permission to push directly to that branch. Missing `Formula/` directories or the target Formula are created automatically; the workflow does not create repositories or change branch protection. Maintainers must explicitly invoke the release process; do not move old tags to adopt a new workflow.

The workflow reads metadata through Ruby's syntax tree without executing tap code. It accepts static, unconditional, direct DSL declarations within a single `Ianchor < Formula` class. Dynamic/indirect calls, conditional metadata and unsupported class-level structures are rejected before publishing. Verify the actual version and normalize those declarations before rerunning; do not bypass version checks. Release query failures report sanitized categories and recovery guidance, never raw error output.

Identical reruns create no duplicate Release or commit, and older versions cannot overwrite newer ones. Same-version reruns can restore the tap Formula to the rendered template from that tag. Template fixes committed after tagging require a new version; do not move existing tags. If tap push fails after Release creation, the Release is retained: fix permissions/network or resolve concurrent changes, then rerun the same tag. CI validates the lockfile with that run's Homebrew Node, not every future ABI or Mac architecture. Formula tests exercise SQLite only in temporary directories and never initialize real iCloud data.

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

Complete subcommands, options, local paths for `add`, and tracked entry names for `recover/relink/remove`. **Type `ianchor` with a trailing space, then press Tab to complete subcommands. Without the space, the shell completes executable names.**

Names containing spaces, colons, or shell metacharacters are escaped literally. Start with an ordinary name prefix and press Tab; do not type unescaped `$()` or backticks, which the shell interprets before completion. Entries starting with `-` require `--`, for example `ianchor recover -- -name`. Names containing newline, carriage return, or NUL are omitted from completion. With default Bash settings, displaying multiple candidates may require pressing Tab twice.

### Homebrew installation

The Formula installs zsh and bash completion files. In zsh, include Homebrew's completion directory **before** running `compinit`:

```zsh
fpath=("$(brew --prefix)/share/zsh/site-functions" $fpath)
autoload -Uz compinit
compinit
```

If a framework such as Oh My Zsh runs `compinit` for you, place only the `fpath` line before framework initialization; do not initialize twice. Bash requires Homebrew's bash-completion to be enabled, or the manual loading method below.

### Manual loading

`ianchor completion zsh/bash` only prints a script; it does not activate completion. In the current zsh session, run this after `compinit`:

```zsh
source <(ianchor completion zsh)
```

For bash:

```bash
source <(ianchor completion bash)
```

For persistence, put the appropriate `source` line in your shell startup file (`~/.zshrc` for zsh or `~/.bashrc` for bash; login bash must also load `~/.bashrc` from `~/.bash_profile`). Alternatively, save the zsh script as `_ianchor` in a directory on `$fpath`, then rerun `compinit`; manual sourcing is unnecessary.

If `_ianchor: function definition file not found` persists after an upgrade, first verify that `_ianchor` exists on `$fpath`. Once the file is installed, remove stale `~/.zcompdump*` caches and restart the terminal.

## License

`package.json` declares the MIT license; this repository does not yet include the `LICENSE` text.
