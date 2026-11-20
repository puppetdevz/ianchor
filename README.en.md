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

Starting with 2.1.0, the Formula installs a prebuilt binary for the host architecture: users need neither Go, source compilation, Go module downloads, Node nor Python. SQLite is compiled in through the pure-Go `modernc.org/sqlite` driver. The already-published 2.0.2 release still builds from source. Installation requires Homebrew 6.0.20 or newer; the support target is macOS 13 or newer on Apple Silicon and Intel, but the release runners' OS versions do not establish that macOS 13 has been tested.

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

The source repository's `Formula/ianchor.rb` is the authoritative placeholder template, not a directly installable Formula. A stable `vMAJOR.MINOR.PATCH` tag must match the `VERSION` file. Actions renders the template from that tagged commit with the separate SHA256 hashes of both architecture archives and syncs the complete Formula to the tap's `main`. Keep installation changes in the source template, not in tap-only edits. Prerelease tags are rejected, and tags are immutable: existing tags are never deleted or moved, so template fixes require a new version.

Before the first release, configure and verify server-side protection against deleting or force-updating released tags. The script's repeated checks are not an atomic lock and cannot prevent an external concurrent tag move. Also prepare an existing `puppetdevz/homebrew-tap` repository with at least one commit on `main`, and configure `HOMEBREW_TAP_TOKEN` in the source repository with permission to push directly to that branch. For a fine-grained PAT, include `puppetdevz/homebrew-tap` in the authorized repositories and grant Contents read/write permission; update the secret if the old token only covered the old repository. Missing `Formula/` directories or the target Formula are created automatically; the workflow does not create repositories or change branch protection. Maintainers must explicitly invoke the release process; do not move old tags to adopt a new workflow.

`scripts/tag.sh` requires a strictly formatted stable `VERSION` and checks the single origin push destination, remote main and tag before pushing branch and tag atomically. The server must support atomic push; there is no sequential fallback. On failure the local commit and tag are retained, but the remote may have accepted the push: inspect it, fix the cause, then run `./scripts/tag.sh resume` instead of bumping again. If both refs already exist remotely, resume does not generate a new tag event. Resolve conflicts manually; never move tags.

Tags validate and create GitHub Releases independently; a late older version observed against a newer tap creates a non-latest Release without downgrading the tap. Publish jobs for the same tag are serialized, but different tags may run concurrently; GitHub's latest status can depend on release timing and should be checked and corrected manually if needed. Tap updates use server-side CAS against the validated commit. If a concurrent change causes a failure, rerun that tag's workflow to revalidate (the Release may already exist). An unavailable tap also prevents Release creation; fix the cause and rerun. A Release can become public before both assets finish uploading, so inspect and recover after an interruption. An old tag rerun uses the workflow at that tag, not this newer release logic.

After building the archives, the release workflow uses ephemeral Apple Silicon and Intel macOS runners to exercise a real fresh install, `brew test`, and an upgrade from the 2.0.2 source-built version in an isolated tap. Either failing gate prevents publication. They test the same archives that will be uploaded, without writing to the real tap or using a release token. **Homebrew acceptance** can also be run manually on a branch or stable tag containing the new workflow; `VERSION` must be newer than 2.0.2. Testing the legacy upgrade still requires network access for source and Go modules; if the runner's own Homebrew is older than the 6.0.20 that 2.0.2 requires, the acceptance script updates Homebrew itself first. An actual GitHub runner execution is needed to establish that the acceptance workflow passes.

The workflow reads metadata through Ruby's syntax tree without executing tap code. It accepts safely recognizable static Formula declarations, including supported architecture branches. Dynamic/indirect metadata and unsupported class-level structures are rejected before publishing. Verify the actual version and normalize those declarations before rerunning; do not bypass version checks. Release query failures report sanitized categories and recovery guidance, never raw error output.

Identical reruns create no duplicate Release or commit, and older versions cannot overwrite newer ones. Existing assets are never overwritten: their bytes must match, or publication stops for human investigation. Do not delete or move old tags to bypass this check. After an interruption, prefer rerunning failed jobs in the original workflow to reuse the same verified artifacts. Temporary artifacts are retained for 30 days; after expiry or a full rerun, inspect asset identity manually, since rebuilt bytes are not guaranteed to be identical. A same-name asset left in a non-`uploaded` state also requires manual investigation; the script will not delete it. If tap push fails after Release creation, the Release is retained: fix permissions, network or concurrent changes, then rerun and verify asset identity. CI also runs `go vet`, `go test` and `go test -race`. Local testing requires 100% statement coverage for every package that has statements, enforced by `./scripts/coverage-check.sh <coverprofile>`: it demands 100.0% for every function and zero uncovered statements counted as integers. The injection seams used to reach error branches default to the real implementations. The Formula test checks version and completion output without accessing iCloud data or the local coordinator. Release unit tests separately exercise an init/add/list/recover roundtrip with an isolated coordinator.

## Usage

### Initialize

```bash
ianchor init
ianchor init --dir dotfiles  # custom directory name
```

Without `--dir`, initialization prompts for a directory name; press Enter for `ianchor`. The default sync directory is `~/Library/Mobile Documents/com~apple~CloudDocs/ianchor/`, containing `ianchor.db`. The directory choice is saved locally in `~/.config/ianchor.json`.

Initialization discovers and registers eligible existing cloud configs but does not create local symlinks. It ignores `.bak`, images, names beginning with `ianchor.db`, and certain system files. Original paths for discovered entries are guessed from known names and may be unknown; check the records with `list` before running `relink`.

The shared database does not rewrite an added entry's absolute original path for the current device. Relinking across devices requires identical original paths, including usernames and config locations. Do not run `relink` directly when those paths differ.

### Add a config file

```bash
ianchor add ~/.zshrc
ianchor add ~/.ssh --name ssh-config
ianchor add ~/.config/alacritty
```

Unless `--name` is provided, the leading dot is stripped: `~/.zshrc` is tracked as `zshrc`. Use the names shown by `ianchor list` in subsequent commands; existing entries are not renamed automatically.

If an entry with that name is still tracked but its iCloud copy is missing, `add` fails and says the name is still tracked instead of keeping the old entry's original path. Use `ianchor add --force` to take over the entry, or `ianchor remove <name>` first.

On a case-insensitive volume, one file may have several spellings. Adding a name when another row resolves to a path differing only by case and its iCloud copy is missing fails and names that row (`--force` does not override the check, just as `recover` refuses a dangling local link even with force, so remove that row first). `remove` refuses only when another entry still tracks the very file it would delete, and names that entry; renaming the copy inside the iCloud directory does not make it unremovable. When the named row is a legacy path-shaped name, which this build refuses to touch, the message says to correct the row with `sqlite3` instead.

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

Re-create the symlink to the cloud copy. Requires an existing tracking entry, original path, and cloud contents. An existing local file or directory is first renamed to `<original-path>.bak`; local changes are not merged. An existing `.bak` may be replaced; incompatible types or a nonempty backup directory cause refusal. Save important backups elsewhere first. A symlink at the original path pointing elsewhere is replaced without being saved as `.bak`.

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
