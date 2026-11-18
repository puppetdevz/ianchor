# iAnchor

Sync your config files to iCloud Drive via symlinks. Share dotfiles and app configs after setting up the directory and links on each Mac.

[中文文档](README.md) · [Changelog](CHANGELOG.en.md)

## How It Works

iAnchor moves config files/directories into an iCloud Drive directory, then creates symlinks at the original locations. iCloud syncs the files, and symlinks ensure your apps still find their configs.

For cross-volume moves, content, directory structure, symlink targets, and ordinary permissions are checked before the original is deleted; a copy or check failure retains the original. ACLs, extended attributes, hard-link relationships and other hidden metadata are not guaranteed to be preserved. Local verification does not mean iCloud has uploaded the data. Back up important files separately and avoid modifying them during the move; the program cannot prevent writes through already-open file handles after verification. If original cleanup fails after commit, the move is already effective and the retained original reported in the error may be partially deleted.

**Concurrency limitation:** On one Mac, this version serializes `init`, `list`, `add`, `remove`, `recover`, and `relink` commands run by the same user, including while waiting for interactive input. Coordination files live locally in `/Users/Shared/.ianchor-<user UID>`; do not delete or move that directory while commands are running. Commands refuse to proceed if the directory is unsafe. With restrictive creation permissions, simultaneous first use may fail before initialization finishes; retry afterward. If interrupted initialization leaves an unsafe directory or file, stop all iAnchor commands and inspect it manually; existing objects are not automatically repaired. This does not coordinate other users, older versions, or programs that modify files directly, and does not provide cross-Mac locking or crash recovery. Wait for the operation and iCloud sync to finish before operating on another device.

## Install

Requires macOS, enabled iCloud Drive, and Homebrew.

```bash
brew tap puppetdevz/tap
brew install puppetdevz/tap/ianchor
```

This personal tap is hosted at [puppetdevz/homebrew-tap](https://github.com/puppetdevz/homebrew-tap).

The Formula builds the Go program from source and declares `go` only as a build dependency. Nothing is needed at runtime beyond the binary: no Node, no nvm or PATH setup, and no native modules. SQLite is compiled in through the pure-Go `modernc.org/sqlite` driver, so there is no Node ABI or Python requirement. Installation requires Homebrew 6.0.20 or newer; it downloads Go modules before building, then builds without network access using Homebrew's `go`; macOS 13 or newer on Apple Silicon and Intel is supported.

## Upgrade

For installations already using the new tap:

```bash
brew update
brew upgrade puppetdevz/tap/ianchor
```

### Migrate from an old tap

If you installed from `vissong/ianchor`, switch the installation source first rather than just changing the upgrade command:

```bash
brew uninstall ianchor
brew untap vissong/ianchor
brew tap puppetdevz/tap
brew install puppetdevz/tap/ianchor
```

If your old tap was `puppetdevz/puppetdevz` or `puppetdevz/ianchor`, replace the untap command above with `brew untap puppetdevz/puppetdevz` or `brew untap puppetdevz/ianchor`; remove only the old tap you actually used.

Migration only changes the Homebrew installation source. The command remains `ianchor`, installed under `$(brew --prefix)/Cellar/ianchor/<version>`. Uninstalling the program does not delete the iCloud sync directory, tracking database, or `~/.config/ianchor.json`. There is no need to run `recover` or initialize again.

### Upgrade from the Node implementation

The Go rewrite targets compatibility with the Node version's commands, output, exit codes, completion scripts, and data layout. Approved safety differences apply to cases such as unsafe names, dangling symlinks, and special files. When `recover` encounters a symlink to an unrelated existing object, it refuses by default and replaces it only with `--force`. The sync directory, `ianchor.db` schema, `.bak` backups and `~/.config/ianchor.json` are unchanged, so existing installations keep working in place with no `recover`, re-`init`, or data migration. If an old `ianchor.db-wal`/`-shm` pair is left in the sync directory, the Go build opens and manages it as before.

## Release maintenance

The source repository's `Formula/ianchor.rb` is the authoritative placeholder template, not a directly installable Formula. A stable `vMAJOR.MINOR.PATCH` tag must match the `VERSION` file. Actions renders the template from that tagged commit with the source archive SHA256 and syncs the complete Formula to the tap's `main`. Keep installation changes in the source template, not in tap-only edits. Prerelease tags are rejected, and tags are immutable: existing tags are never deleted or moved, so template fixes require a new version.

Before the first release, configure and verify server-side protection against deleting or force-updating released tags. The script's repeated checks are not an atomic lock and cannot prevent an external concurrent tag move. Also prepare an existing `puppetdevz/homebrew-tap` repository with at least one commit on `main`, and configure `HOMEBREW_TAP_TOKEN` in the source repository with permission to push directly to that branch. For a fine-grained PAT, include `puppetdevz/homebrew-tap` in the authorized repositories and grant Contents read/write permission; update the secret if the old token only covered the old repository. Missing `Formula/` directories or the target Formula are created automatically; the workflow does not create repositories or change branch protection. Maintainers must explicitly invoke the release process; do not move old tags to adopt a new workflow.

If a push fails after `scripts/tag.sh` creates the version commit, fix the cause and run `./scripts/tag.sh resume` to push that version instead of bumping again. HEAD must still be the matching version commit; existing tags are never moved.

The workflow reads metadata through Ruby's syntax tree without executing tap code. It accepts static, unconditional, direct DSL declarations within a single `Ianchor < Formula` class. Dynamic/indirect calls, conditional metadata and unsupported class-level structures are rejected before publishing. Verify the actual version and normalize those declarations before rerunning; do not bypass version checks. Release query failures report sanitized categories and recovery guidance, never raw error output.

Identical reruns create no duplicate Release or commit, and older versions cannot overwrite newer ones. Same-version reruns can restore the tap Formula to the rendered template from that tag. Template fixes committed after tagging require a new version; do not move existing tags. If tap push fails after Release creation, the Release is retained: fix permissions/network or resolve concurrent changes, then rerun the same tag. CI verifies the sources with `go vet`, `go test`, `go test -race`, and darwin/amd64 plus darwin/arm64 cross-builds; it does not prove both architectures were exercised on real hardware. Local testing requires 100% statement coverage for every package that has statements, enforced by `./scripts/coverage-check.sh <coverprofile>`: it demands 100.0% for every function and zero uncovered statements counted as integers, so the rounded total of `go tool cover -func` cannot hide a gap. The injection seams used to reach error branches default to the real implementations. The Formula test checks version and completion output without accessing iCloud data or the local coordinator. Release unit tests separately exercise an init/add/list/recover roundtrip with an isolated coordinator.

## Usage

### Initialize

```bash
ianchor init
ianchor init --dir dotfiles  # custom directory name
```

Without `--dir`, initialization prompts for a directory name; press Enter for `ianchor`. The default sync directory is `~/Library/Mobile Documents/com~apple~CloudDocs/ianchor/`, containing `ianchor.db`. The directory choice is saved locally in `~/.config/ianchor.json`.

Initialization discovers and registers existing cloud configs but does not create local symlinks. Initialize other devices too, then run `relink` for entries with a known original path.

The shared database stores the absolute original path recorded when an entry was added; it is not rewritten for the current device. Relinking across devices requires identical original paths, including usernames and config locations. Do not run `relink` directly when those paths differ.

### Add a config file

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

Unless `--name` is provided, the leading dot is stripped: `~/.zshrc` is tracked as `zshrc`. Use the names shown by `ianchor list` in subsequent commands; existing entries are not renamed automatically.

If an entry with that name is still tracked but its iCloud copy is missing, `add` fails and says the name is still tracked instead of keeping the old entry's original path. Use `ianchor add --force` to take over the entry, or `ianchor remove <name>` first.

macOS stores one file under several spellings, so a name that differs from a tracked entry only by case is refused: adding it while another row resolves to the same path and its iCloud copy is missing fails and names that row (`--force` does not override the check, just as `recover` refuses a dangling local link even with force, so remove that row first). `remove` refuses only when another entry still tracks the very file it would delete, and names that entry; renaming the copy inside the iCloud directory does not make it unremovable. When the named row is a legacy path-shaped name, which this build refuses to touch, the message says to correct the row with `sqlite3` instead.

### List tracked configs

```bash
ianchor list
ianchor list --json
```

### Recover a config file

Restore a file or directory to its original location on this device only, replacing the symlink with an independent local copy. The iCloud file and database entry are preserved, so other devices keep working. Local edits no longer sync after recovery. `ianchor list` shows the local entry as `unlinked`; use `ianchor relink <name>` to rejoin syncing.

Cloud contents are backed up to `<sync-directory>/.bak/<name>` before recovery, replacing any previous backup of the same name. Before committing, recovery compares the local copy with the backup's contents, directory structure, ordinary permissions and link targets; a mismatch aborts and attempts rollback. During copying and comparison, new copies may temporarily gain owner access, with final ordinary modes restored after verification; a process crash can leave temporary copies with wider owner permissions. Recovery does not guarantee ACL preservation. If final permissions prevent the current user from reading a copy or reading and searching a directory, recovery aborts and attempts rollback; the unforced conflict branch also refuses to retain an inaccessible new backup. This check is not an atomic snapshot and cannot prevent later concurrent changes. `--force` deletes an existing local file or directory at the original path; this backup preserves cloud contents, not the overwritten local contents. Entries without an original path are rejected without deleting cloud data.

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

If a legacy entry's original path points inside the iCloud sync directory or another unsafe location, `remove` refuses the whole operation and touches neither the cloud data nor the database; fix that entry's original path first.

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

This project is MIT licensed; the repository does not yet include the `LICENSE` text.
