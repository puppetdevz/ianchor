# AGENTS.md

Project instructions for coding agents working in this repository.

## Project Overview

**iAnchor** (`@vissong/ianchor`) is a Node.js CLI tool that syncs dotfiles and app configs across Macs via iCloud Drive. It moves config files into iCloud Drive and creates symlinks at their original locations so apps still find them.

## Commands

```bash
npm install              # Install dependencies
npm test                 # Run all tests
node --test test/add.test.js   # Run a single test file
```

No build step — ES modules run directly. Entry point: `bin/ianchor.js`.

## Architecture

**CLI layer** (`bin/ianchor.js`): Uses `commander` to parse commands, each dispatching to a function in `lib/`.

**Core modules** (`lib/`):

- `icloud.js` — Detects iCloud Drive path (`~/Library/Mobile Documents/com~apple~CloudDocs/`)
- `db.js` — SQLite wrapper (via `better-sqlite3`) managing the `entries` table
- `init.js` — Creates config directory in iCloud, opens DB, discovers existing files
- `add.js` — Moves file/dir to iCloud, creates symlink, records in DB
- `recover.js` — Moves file back from iCloud to original location
- `relink.js` — Re-creates broken symlinks for tracked entries
- `remove.js` — Deletes iCloud copy and DB record
- `list.js` — Shows tracked entries with live status (linked/unlinked/missing)
- `known-configs.js` — Maps common config filenames to their expected home paths

**Data flow**: CLI parses args → command handler calls `lib/` function → function reads/writes filesystem + SQLite DB → result printed to stdout.

**Database**: Single SQLite file (`ianchor.db`) stored inside the iCloud config directory. One table `entries` with columns: `id`, `name` (unique), `original_path`, `type` (file/directory), `status` (linked/unlinked/deleted), `created_at`, `updated_at`.

## Testing

Tests use Node.js built-in `node:test` module with `node:assert/strict`. Each `lib/` module has a corresponding `test/*.test.js` file. Tests create temp directories for isolation and clean up after themselves.

## Key Conventions

- ES modules (`"type": "module"` in package.json)
- Node.js >=18.0.0 required
- Dependencies: `better-sqlite3`, `chalk`, `cli-table3`, `commander`
- Distribution via Homebrew (tap repository: `puppetdevz/homebrew-puppetdevz`, tap name: `puppetdevz/puppetdevz`); release via `scripts/tag.sh` + GitHub Actions. The source repository's `Formula/ianchor.rb` is the authoritative placeholder template (not directly installable). The release workflow renders it from a stable tagged commit, creates or updates only `Formula/ianchor.rb` on the initialized personal tap's `main`, refuses downgrades, and skips identical reruns. Only `node` is a direct Formula dependency; CI and Formula use Homebrew Node, locked dependencies with lifecycle scripts disabled, and matching SQLite prebuilds without compilation fallback. Maintainers must prepare the tap and `HOMEBREW_TAP_TOKEN`; real publishing requires explicit release authorization.
