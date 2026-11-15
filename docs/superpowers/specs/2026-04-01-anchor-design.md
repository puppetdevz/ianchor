# Anchor - iCloud Config File Sync Tool

> Historical design only. The Node.js implementation and some database and recovery details below have been superseded. See `docs/2026-1114-0054_Go全量重写与兼容迁移GoalLoop实施方案.md`, the current implementation, and the README for the active contract.

## Overview

Anchor is a Node.js CLI tool that maintains a directory in iCloud Drive for syncing application config files across machines. It moves config files/directories into iCloud, creates symlinks at the original locations pointing back to iCloud (using absolute paths), and tracks the relationships in a SQLite database.

Installable via Homebrew.

## Tech Stack

- **Runtime**: Node.js (ESM)
- **CLI Framework**: commander
- **Database**: better-sqlite3 (SQLite)
- **Distribution**: Homebrew Formula via tap repository

## Project Structure

```
anchor/
├── bin/
│   └── anchor.js          # CLI entry point (#!/usr/bin/env node)
├── lib/
│   ├── db.js              # SQLite database operations
│   ├── init.js            # Initialization logic
│   ├── list.js            # List display
│   ├── add.js             # Add file/directory
│   ├── recover.js         # Recover file/directory
│   ├── icloud.js          # iCloud path detection
│   └── known-configs.js   # Known config file mappings
├── package.json
├── Formula/
│   └── anchor.rb          # Homebrew Formula
└── README.md
```

## iCloud Directory Layout

Default location: `~/Library/Mobile Documents/com~apple~CloudDocs/config/`

```
config/
├── anchor.db          # SQLite database
├── .zshrc             # Synced config files
├── .gitconfig
├── .ssh/              # Synced config directories
├── alacritty/
└── ...
```

The directory name (`config`) is customizable via `anchor init --dir <name>`.

## Data Model

SQLite database stored at `<icloud_config_dir>/anchor.db`.

```sql
CREATE TABLE entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,         -- Relative path within iCloud config directory
  original_path TEXT,                -- Original absolute path on disk
  type TEXT NOT NULL,                -- 'file' or 'directory'
  status TEXT NOT NULL DEFAULT 'linked',  -- 'linked' or 'unlinked'
  created_at TEXT DEFAULT (datetime('now')),
  updated_at TEXT DEFAULT (datetime('now'))
);
```

- `name`: The filename or directory name as stored in the iCloud config directory
- `original_path`: The absolute path where the file/directory originally lived (and where the symlink is created)
- `status`: `linked` means a symlink exists at `original_path` pointing to iCloud; `unlinked` means the entry is tracked but no symlink exists

## Commands

### `anchor init [--dir <name>]`

1. Detect iCloud Drive at `~/Library/Mobile Documents/com~apple~CloudDocs/`
2. Create config directory (default: `config`, customizable via `--dir`)
3. Initialize SQLite database with schema
4. Scan existing files in the config directory
5. For each existing file, add to database if not already tracked
6. For known config files, auto-populate `original_path` based on the known-configs mapping
7. For unknown files, prompt user to provide `original_path` (with option to skip)
8. Print summary of initialized/discovered entries

### `anchor list`

- Display a table of all entries: name, original path, type, status
- Support `--json` flag for JSON output

### `anchor add <source_path> [--name <name>]`

1. Resolve `source_path` to absolute path
2. Validate source exists and is not already a symlink
3. Determine destination name (basename of source, or `--name` override)
4. Check for name conflicts in iCloud directory
5. Move file/directory to iCloud config directory (`fs.rename` or copy+delete for cross-device)
6. Create symlink at original location pointing to iCloud path (absolute path)
7. Insert entry into database with status `linked`

### `anchor recover <name>`

1. Look up entry in database by name
2. Validate the file exists in iCloud config directory
3. Remove symlink at `original_path` (if it exists and is a symlink)
4. Copy file/directory from iCloud back to `original_path`
5. Remove file/directory from iCloud config directory
6. Delete entry from database
7. Print confirmation

## Known Config File Mappings

Used during `init` to auto-detect `original_path` for files already in the config directory.

```
.zshrc          -> ~/.zshrc
.bashrc         -> ~/.bashrc
.bash_profile   -> ~/.bash_profile
.zprofile       -> ~/.zprofile
.gitconfig      -> ~/.gitconfig
.vimrc          -> ~/.vimrc
.tmux.conf      -> ~/.tmux.conf
.ssh            -> ~/.ssh
.config         -> ~/.config
alacritty       -> ~/.config/alacritty
karabiner       -> ~/.config/karabiner
nvim            -> ~/.config/nvim
starship.toml   -> ~/.config/starship.toml
Code            -> ~/Library/Application Support/Code/User
```

All `~` paths are resolved to the actual home directory at runtime.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| iCloud Drive not found | Error with message: "iCloud Drive not found. Please ensure iCloud is enabled." |
| Config directory already exists on init | Proceed normally, scan existing files |
| File already exists at destination (add) | Error: "File already exists in iCloud directory. Use --force to overwrite." |
| Source is already a symlink (add) | Error: "Source is already a symlink." |
| File conflict at original_path (recover) | Error: "File already exists at original path. Use --force to overwrite." |
| Entry not found in database | Error: "Entry '<name>' not found." |
| Permission denied | Error with specific permission message |
| Cross-device move | Fall back to copy + delete |

## Homebrew Distribution

### Formula (`Formula/anchor.rb`)

- Uses `npm install --production` for dependencies
- Links `bin/anchor.js` as the `anchor` command
- Depends on `node`

### Installation

```bash
brew tap <username>/anchor
brew install anchor
```

The tap repository (`homebrew-anchor`) will contain the Formula and point to the release tarball.

## Dependencies

- `commander` - CLI argument parsing
- `better-sqlite3` - SQLite database
- `chalk` - Terminal colors (for table output)
- `cli-table3` - Table formatting for list command
