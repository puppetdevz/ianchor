# Changelog

[简体中文](CHANGELOG.md)

## 1.1.0 - 2026-10-22

### Features

- Add an authoritative Homebrew Formula template in the source repository. Bind the launcher to Homebrew Node, install locked dependencies with lifecycle scripts disabled, and download only SQLite prebuilds matching the Node ABI, macOS and architecture. Stop on failure without source-compilation fallback.
- Support creating `Formula/ianchor.rb` in an initialized personal tap on the first release. Installation and Formula tests verify SQLite reads and writes in temporary directories without touching real iCloud data.

### Fixes

- Correct the personal tap repository to `puppetdevz/homebrew-puppetdevz` (tap name: `puppetdevz/puppetdevz`).
- Require stable tags to match the package version. Validate static Formula metadata through Ruby's syntax tree, rejecting dynamic, conditional and indirect declarations. Prevent downgrades and detect concurrent tap branch changes.
- Skip duplicate Releases and commits on identical same-version reruns. Stop publishing on Release query failures with sanitized categories and recovery guidance. Preserve a created Release if tap push fails so maintainers can fix the issue and rerun.

### Dependencies

- Update the locked `better-sqlite3` version from 12.8.0 to 12.10.0.

### Documentation

- Document old-tap migration, first-release prerequisites, prebuild-only installation limits and failure recovery. Clarify that same-version reruns use that tag's template; template fixes committed after tagging require a new version.

### Notes

- Maintainers must prepare the personal tap repository with a committed `main` branch and configure a writable `HOMEBREW_TAP_TOKEN`. The workflow does not create repositories or change branch protection.
- Homebrew Node ABI upgrades may require reinstallation. Prebuild availability for every future ABI and real-device verification of every Mac architecture are not guaranteed.

## 1.0.0 - 2026-10-22

### Fixes

- Make `recover` restore an independent copy on the current device while preserving the iCloud file and shared database entry, so links on other devices keep working. Reject entries without an original path without deleting cloud data.
- Point the Homebrew release workflow at `puppetdevz/ianchor` and `puppetdevz/homebrew-ianchor`. Skip creating an existing GitHub Release, stop on source archive download failures, and calculate SHA256 only after a successful download.

### Documentation

- Make Chinese the default README, retain the English edition, and add upgrade instructions. Explain local recovery versus global deletion.
- Correct dotfile entry name examples and document installation prerequisites, initialization and data locations, recovery overwrite risks, and relinking without merging local changes.
- Add Chinese and English changelogs and backfill verifiable historical version records.

### Behavior Notes

- `recover` no longer deletes shared files or tracking entries. Local edits no longer sync after recovery, and `list` reports `unlinked`. Use `relink <name>` to rejoin syncing (the local copy is backed up as `.bak`). For global deletion, use `remove`, which affects other devices.

## Historical Version Records

The records below were reconstructed from version changes and related Git commits. Dates are version-change commit dates, not verified public release dates. At the time of this check, neither the local repository nor `origin` had historical tags, and GitHub had no Release records.

## 0.1.6 - 2026-04-04

### Maintenance

- Update `package.json` and `package-lock.json` to 0.1.6; no other code changes were found relative to the previous version. Source: [dbeb977](https://github.com/puppetdevz/ianchor/commit/dbeb97791d033294d1b4cef94ed6ed22c695effe).

## 0.1.5 - 2026-04-04

### Refactor

- Move the Homebrew formula out of the main repository into a dedicated tap, with the release workflow updating that tap. Update installation docs and remove the obsolete release script. Source: [888a1fe](https://github.com/puppetdevz/ianchor/commit/888a1fe0be281a12ba00d4e28eb8f256b1d2f7f0) and preceding commits.

## 0.1.4 - 2026-04-04

### Fixes

- Include `package-lock.json` in version bump commits and correct its 0.1.3 version metadata. Source: [47abc79](https://github.com/puppetdevz/ianchor/commit/47abc79083eae00b48befbbfc25b38a0c9117025) and preceding commits.

## 0.1.3 - 2026-04-04

### Fixes

- Generate separate zsh and bash completion scripts. Use `#compdef`, `_arguments`, and `_describe` for proper zsh autoloading. Source: [258621f](https://github.com/puppetdevz/ianchor/commit/258621fb500c4ac8a01e2aa9425577a232d86930) and preceding commits.

## 0.1.2 - 2026-04-04

### Features

- Check files asynchronously in `list`, showing a loading spinner after a 100ms wait.
- Add shell completion for zsh and bash, including tracked entry names.
- Add a tag-triggered GitHub Actions release workflow and a version tagging script.

### Changes

- Move `--dir` to the `init` subcommand and persist the selected directory. Update earlier invocations to `ianchor init --dir <name>`. Source: [2eade3b](https://github.com/puppetdevz/ianchor/commit/2eade3b08ad797131c767f8c69a47378045bd4c9) and preceding commits.

## 0.1.1 - 2026-04-01

### Features

- Strip the leading dot from dotfile names unless a custom name is provided; for example, store `.zshrc` as `zshrc` in iCloud. Source: [af2257d](https://github.com/puppetdevz/ianchor/commit/af2257d7b835967eeb4a963333bded2980cec206).

## 0.1.0 - 2026-04-01

### Features

- Initial implementation: iCloud Drive detection, a SQLite tracking database, initialization and known-config discovery, adding configs with symlinks, table/JSON listing, recovery, relinking, and removal commands.
- Provide a Homebrew formula and rename the project and command from `anchor` to `ianchor`. Source: [ccdca79](https://github.com/puppetdevz/ianchor/commit/ccdca79ddbc9a36cd651693535a9390d10476a1a) and preceding commits.

### Historical Behavior

- In this version, `recover` deleted the cloud file and database entry; 1.0.0 changes it to restore a local copy only.
