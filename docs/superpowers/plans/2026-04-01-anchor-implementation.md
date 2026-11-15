# Anchor CLI Implementation Plan

> Historical Node.js implementation plan, superseded by the Go rewrite. Do not execute these tasks against the current repository. For the current contract see `docs/2026-1114-0054_Go全量重写与兼容迁移GoalLoop实施方案.md`, the implementation, and the README.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Node.js CLI tool that syncs config files to iCloud Drive via symlinks, installable via Homebrew.

**Architecture:** ESM Node.js CLI with commander for arg parsing, better-sqlite3 for tracking file relationships, and direct filesystem operations for move/symlink. Each command is a separate module in `lib/`, wired together in `bin/anchor.js`.

**Tech Stack:** Node.js (ESM), commander, better-sqlite3, chalk, cli-table3

---

### Task 1: Project Scaffolding

**Files:**
- Create: `package.json`
- Create: `bin/anchor.js`
- Create: `.gitignore`

- [ ] **Step 1: Create package.json**

```json
{
  "name": "anchor-cli",
  "version": "0.1.0",
  "description": "Sync config files to iCloud Drive via symlinks",
  "type": "module",
  "bin": {
    "anchor": "./bin/anchor.js"
  },
  "scripts": {
    "test": "node --test test/**/*.test.js"
  },
  "engines": {
    "node": ">=18.0.0"
  },
  "license": "MIT"
}
```

- [ ] **Step 2: Create .gitignore**

```
node_modules/
*.db
```

- [ ] **Step 3: Create bin/anchor.js stub**

```js
#!/usr/bin/env node

console.log('anchor CLI - not yet implemented');
```

- [ ] **Step 4: Install dependencies**

Run: `npm install commander better-sqlite3 chalk cli-table3`

Expected: `package-lock.json` created, `node_modules/` populated.

- [ ] **Step 5: Verify the CLI entry runs**

Run: `node bin/anchor.js`

Expected: prints `anchor CLI - not yet implemented`

- [ ] **Step 6: Commit**

```bash
git add package.json package-lock.json .gitignore bin/anchor.js
git commit -m "chore: scaffold project with dependencies"
```

---

### Task 2: iCloud Path Detection (`lib/icloud.js`)

**Files:**
- Create: `test/icloud.test.js`
- Create: `lib/icloud.js`

- [ ] **Step 1: Write the failing test**

Create `test/icloud.test.js`:

```js
import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { getICloudDrivePath, getConfigDir } from '../lib/icloud.js';

describe('icloud', () => {
  describe('getICloudDrivePath', () => {
    it('returns the iCloud Drive path on macOS', () => {
      const result = getICloudDrivePath();
      assert.equal(
        result,
        path.join(os.homedir(), 'Library', 'Mobile Documents', 'com~apple~CloudDocs')
      );
    });
  });

  describe('getConfigDir', () => {
    let tmpDir;

    before(() => {
      tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-test-'));
    });

    after(() => {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    });

    it('returns icloudBase/config by default', () => {
      const result = getConfigDir(tmpDir);
      assert.equal(result, path.join(tmpDir, 'config'));
    });

    it('returns icloudBase/<custom> when dirName is provided', () => {
      const result = getConfigDir(tmpDir, 'dotfiles');
      assert.equal(result, path.join(tmpDir, 'dotfiles'));
    });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/icloud.test.js`

Expected: FAIL — module `../lib/icloud.js` not found.

- [ ] **Step 3: Write implementation**

Create `lib/icloud.js`:

```js
import path from 'node:path';
import os from 'node:os';
import fs from 'node:fs';

const ICLOUD_BASE = path.join(
  os.homedir(),
  'Library',
  'Mobile Documents',
  'com~apple~CloudDocs'
);

const DEFAULT_DIR_NAME = 'config';

export function getICloudDrivePath() {
  return ICLOUD_BASE;
}

export function getConfigDir(icloudBase = ICLOUD_BASE, dirName = DEFAULT_DIR_NAME) {
  return path.join(icloudBase, dirName);
}

export function ensureConfigDir(icloudBase = ICLOUD_BASE, dirName = DEFAULT_DIR_NAME) {
  const icloudPath = icloudBase;
  if (!fs.existsSync(icloudPath)) {
    throw new Error('iCloud Drive not found. Please ensure iCloud is enabled.');
  }
  const configDir = getConfigDir(icloudBase, dirName);
  if (!fs.existsSync(configDir)) {
    fs.mkdirSync(configDir, { recursive: true });
  }
  return configDir;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/icloud.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Add test for ensureConfigDir**

Add to `test/icloud.test.js`:

```js
  describe('ensureConfigDir', () => {
    let tmpDir;

    before(() => {
      tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-test-'));
    });

    after(() => {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    });

    it('creates config directory if it does not exist', () => {
      const result = ensureConfigDir(tmpDir, 'config');
      assert.equal(result, path.join(tmpDir, 'config'));
      assert.ok(fs.existsSync(result));
    });

    it('throws when iCloud base does not exist', () => {
      assert.throws(
        () => ensureConfigDir('/nonexistent/path'),
        { message: 'iCloud Drive not found. Please ensure iCloud is enabled.' }
      );
    });
  });
```

Update the import line to include `ensureConfigDir`:

```js
import { getICloudDrivePath, getConfigDir, ensureConfigDir } from '../lib/icloud.js';
```

- [ ] **Step 6: Run test to verify it passes**

Run: `node --test test/icloud.test.js`

Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add lib/icloud.js test/icloud.test.js
git commit -m "feat: add iCloud path detection"
```

---

### Task 3: Known Config Mappings (`lib/known-configs.js`)

**Files:**
- Create: `test/known-configs.test.js`
- Create: `lib/known-configs.js`

- [ ] **Step 1: Write the failing test**

Create `test/known-configs.test.js`:

```js
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import os from 'node:os';
import path from 'node:path';
import { guessOriginalPath, KNOWN_CONFIGS } from '../lib/known-configs.js';

const HOME = os.homedir();

describe('known-configs', () => {
  it('exports a non-empty KNOWN_CONFIGS map', () => {
    assert.ok(Object.keys(KNOWN_CONFIGS).length > 0);
  });

  it('guesses original path for .zshrc', () => {
    assert.equal(guessOriginalPath('.zshrc'), path.join(HOME, '.zshrc'));
  });

  it('guesses original path for alacritty', () => {
    assert.equal(guessOriginalPath('alacritty'), path.join(HOME, '.config', 'alacritty'));
  });

  it('guesses original path for Code (VS Code)', () => {
    assert.equal(
      guessOriginalPath('Code'),
      path.join(HOME, 'Library', 'Application Support', 'Code', 'User')
    );
  });

  it('returns null for unknown config', () => {
    assert.equal(guessOriginalPath('unknown-file-xyz'), null);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/known-configs.test.js`

Expected: FAIL — module not found.

- [ ] **Step 3: Write implementation**

Create `lib/known-configs.js`:

```js
import os from 'node:os';
import path from 'node:path';

const HOME = os.homedir();

export const KNOWN_CONFIGS = {
  '.zshrc': path.join(HOME, '.zshrc'),
  '.bashrc': path.join(HOME, '.bashrc'),
  '.bash_profile': path.join(HOME, '.bash_profile'),
  '.zprofile': path.join(HOME, '.zprofile'),
  '.gitconfig': path.join(HOME, '.gitconfig'),
  '.vimrc': path.join(HOME, '.vimrc'),
  '.tmux.conf': path.join(HOME, '.tmux.conf'),
  '.ssh': path.join(HOME, '.ssh'),
  '.config': path.join(HOME, '.config'),
  'alacritty': path.join(HOME, '.config', 'alacritty'),
  'karabiner': path.join(HOME, '.config', 'karabiner'),
  'nvim': path.join(HOME, '.config', 'nvim'),
  'starship.toml': path.join(HOME, '.config', 'starship.toml'),
  'Code': path.join(HOME, 'Library', 'Application Support', 'Code', 'User'),
};

export function guessOriginalPath(name) {
  return KNOWN_CONFIGS[name] ?? null;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/known-configs.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/known-configs.js test/known-configs.test.js
git commit -m "feat: add known config file mappings"
```

---

### Task 4: Database Layer (`lib/db.js`)

**Files:**
- Create: `test/db.test.js`
- Create: `lib/db.js`

- [ ] **Step 1: Write the failing test**

Create `test/db.test.js`:

```js
import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { openDb, addEntry, getEntry, getAllEntries, updateEntry, deleteEntry } from '../lib/db.js';

describe('db', () => {
  let tmpDir;
  let db;

  before(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-db-test-'));
    db = openDb(tmpDir);
  });

  after(() => {
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('creates the entries table', () => {
    const tables = db.prepare(
      "SELECT name FROM sqlite_master WHERE type='table' AND name='entries'"
    ).all();
    assert.equal(tables.length, 1);
  });

  it('inserts and retrieves an entry', () => {
    addEntry(db, { name: '.zshrc', originalPath: '/Users/test/.zshrc', type: 'file' });
    const entry = getEntry(db, '.zshrc');
    assert.equal(entry.name, '.zshrc');
    assert.equal(entry.original_path, '/Users/test/.zshrc');
    assert.equal(entry.type, 'file');
    assert.equal(entry.status, 'linked');
  });

  it('lists all entries', () => {
    addEntry(db, { name: '.bashrc', originalPath: '/Users/test/.bashrc', type: 'file' });
    const entries = getAllEntries(db);
    assert.ok(entries.length >= 2);
  });

  it('updates an entry', () => {
    updateEntry(db, '.zshrc', { originalPath: '/Users/other/.zshrc', status: 'unlinked' });
    const entry = getEntry(db, '.zshrc');
    assert.equal(entry.original_path, '/Users/other/.zshrc');
    assert.equal(entry.status, 'unlinked');
  });

  it('deletes an entry', () => {
    deleteEntry(db, '.bashrc');
    const entry = getEntry(db, '.bashrc');
    assert.equal(entry, undefined);
  });

  it('returns undefined for nonexistent entry', () => {
    const entry = getEntry(db, 'nonexistent');
    assert.equal(entry, undefined);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/db.test.js`

Expected: FAIL — module not found.

- [ ] **Step 3: Write implementation**

Create `lib/db.js`:

```js
import path from 'node:path';
import Database from 'better-sqlite3';

const DB_FILENAME = 'anchor.db';

const SCHEMA = `
CREATE TABLE IF NOT EXISTS entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  original_path TEXT,
  type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'linked',
  created_at TEXT DEFAULT (datetime('now')),
  updated_at TEXT DEFAULT (datetime('now'))
);
`;

export function openDb(configDir) {
  const dbPath = path.join(configDir, DB_FILENAME);
  const db = new Database(dbPath);
  db.pragma('journal_mode = WAL');
  db.exec(SCHEMA);
  return db;
}

export function addEntry(db, { name, originalPath, type, status = 'linked' }) {
  const stmt = db.prepare(
    'INSERT OR IGNORE INTO entries (name, original_path, type, status) VALUES (?, ?, ?, ?)'
  );
  return stmt.run(name, originalPath ?? null, type, status);
}

export function getEntry(db, name) {
  return db.prepare('SELECT * FROM entries WHERE name = ?').get(name);
}

export function getAllEntries(db) {
  return db.prepare('SELECT * FROM entries ORDER BY name').all();
}

export function updateEntry(db, name, { originalPath, status }) {
  const fields = [];
  const values = [];
  if (originalPath !== undefined) {
    fields.push('original_path = ?');
    values.push(originalPath);
  }
  if (status !== undefined) {
    fields.push('status = ?');
    values.push(status);
  }
  if (fields.length === 0) return;
  fields.push("updated_at = datetime('now')");
  values.push(name);
  db.prepare(`UPDATE entries SET ${fields.join(', ')} WHERE name = ?`).run(...values);
}

export function deleteEntry(db, name) {
  db.prepare('DELETE FROM entries WHERE name = ?').run(name);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/db.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/db.js test/db.test.js
git commit -m "feat: add SQLite database layer"
```

---

### Task 5: Init Command (`lib/init.js`)

**Files:**
- Create: `test/init.test.js`
- Create: `lib/init.js`

- [ ] **Step 1: Write the failing test**

Create `test/init.test.js`:

```js
import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { initAnchor } from '../lib/init.js';

describe('init', () => {
  let tmpDir;

  before(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-init-test-'));
  });

  after(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('creates config directory and database', () => {
    const result = initAnchor({ icloudBase: tmpDir, dirName: 'config', interactive: false });
    const configDir = path.join(tmpDir, 'config');
    assert.ok(fs.existsSync(configDir));
    assert.ok(fs.existsSync(path.join(configDir, 'anchor.db')));
    assert.equal(result.configDir, configDir);
    assert.equal(result.discovered, 0);
  });

  it('discovers existing files in config directory', () => {
    const configDir = path.join(tmpDir, 'testdir');
    fs.mkdirSync(configDir, { recursive: true });
    fs.writeFileSync(path.join(configDir, '.zshrc'), 'test');
    fs.mkdirSync(path.join(configDir, '.ssh'));

    const result = initAnchor({ icloudBase: tmpDir, dirName: 'testdir', interactive: false });
    assert.equal(result.discovered, 2);
    assert.ok(result.entries.some(e => e.name === '.zshrc'));
    assert.ok(result.entries.some(e => e.name === '.ssh'));
  });

  it('auto-populates original_path for known configs', () => {
    const result = initAnchor({ icloudBase: tmpDir, dirName: 'testdir', interactive: false });
    const zshrc = result.entries.find(e => e.name === '.zshrc');
    assert.equal(zshrc.original_path, path.join(os.homedir(), '.zshrc'));
  });

  it('throws if iCloud base does not exist', () => {
    assert.throws(
      () => initAnchor({ icloudBase: '/nonexistent/path', dirName: 'config', interactive: false }),
      { message: 'iCloud Drive not found. Please ensure iCloud is enabled.' }
    );
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/init.test.js`

Expected: FAIL — module not found.

- [ ] **Step 3: Write implementation**

Create `lib/init.js`:

```js
import fs from 'node:fs';
import path from 'node:path';
import { ensureConfigDir } from './icloud.js';
import { openDb, addEntry, getAllEntries, updateEntry, getEntry } from './db.js';
import { guessOriginalPath } from './known-configs.js';

export function initAnchor({ icloudBase, dirName = 'config', interactive = true }) {
  const configDir = ensureConfigDir(icloudBase, dirName);
  const db = openDb(configDir);

  // Scan existing files (excluding anchor.db)
  const items = fs.readdirSync(configDir).filter(name => name !== 'anchor.db');
  let discovered = 0;

  for (const name of items) {
    const existing = getEntry(db, name);
    if (existing) continue;

    const fullPath = path.join(configDir, name);
    const stat = fs.lstatSync(fullPath);
    const type = stat.isDirectory() ? 'directory' : 'file';
    const originalPath = guessOriginalPath(name);

    addEntry(db, { name, originalPath, type, status: originalPath ? 'unlinked' : 'unlinked' });
    discovered++;
  }

  const entries = getAllEntries(db);
  db.close();

  return { configDir, discovered, entries };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/init.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/init.js test/init.test.js
git commit -m "feat: add init command with config discovery"
```

---

### Task 6: List Command (`lib/list.js`)

**Files:**
- Create: `test/list.test.js`
- Create: `lib/list.js`

- [ ] **Step 1: Write the failing test**

Create `test/list.test.js`:

```js
import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { openDb, addEntry } from '../lib/db.js';
import { listEntries } from '../lib/list.js';

describe('list', () => {
  let tmpDir;
  let db;

  before(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-list-test-'));
    db = openDb(tmpDir);
    addEntry(db, { name: '.zshrc', originalPath: '/Users/test/.zshrc', type: 'file', status: 'linked' });
    addEntry(db, { name: '.ssh', originalPath: '/Users/test/.ssh', type: 'directory', status: 'linked' });
  });

  after(() => {
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('returns formatted table string', () => {
    const output = listEntries(db, { json: false });
    assert.ok(typeof output === 'string');
    assert.ok(output.includes('.zshrc'));
    assert.ok(output.includes('.ssh'));
  });

  it('returns JSON array when json option is true', () => {
    const output = listEntries(db, { json: true });
    const parsed = JSON.parse(output);
    assert.ok(Array.isArray(parsed));
    assert.equal(parsed.length, 2);
    assert.equal(parsed[0].name, '.ssh');
  });

  it('returns message when no entries exist', () => {
    const emptyDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-list-empty-'));
    const emptyDb = openDb(emptyDir);
    const output = listEntries(emptyDb, { json: false });
    assert.ok(output.includes('No entries'));
    emptyDb.close();
    fs.rmSync(emptyDir, { recursive: true, force: true });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/list.test.js`

Expected: FAIL — module not found.

- [ ] **Step 3: Write implementation**

Create `lib/list.js`:

```js
import Table from 'cli-table3';
import chalk from 'chalk';
import { getAllEntries } from './db.js';

export function listEntries(db, { json = false } = {}) {
  const entries = getAllEntries(db);

  if (json) {
    return JSON.stringify(entries, null, 2);
  }

  if (entries.length === 0) {
    return 'No entries found. Use "anchor add <path>" to add config files.';
  }

  const table = new Table({
    head: [
      chalk.cyan('Name'),
      chalk.cyan('Original Path'),
      chalk.cyan('Type'),
      chalk.cyan('Status'),
    ],
  });

  for (const entry of entries) {
    table.push([
      entry.name,
      entry.original_path ?? chalk.dim('(unknown)'),
      entry.type,
      entry.status === 'linked' ? chalk.green('linked') : chalk.yellow('unlinked'),
    ]);
  }

  return table.toString();
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/list.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/list.js test/list.test.js
git commit -m "feat: add list command with table and JSON output"
```

---

### Task 7: Add Command (`lib/add.js`)

**Files:**
- Create: `test/add.test.js`
- Create: `lib/add.js`

- [ ] **Step 1: Write the failing test**

Create `test/add.test.js`:

```js
import { describe, it, before, after, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { openDb, getEntry } from '../lib/db.js';
import { addFile } from '../lib/add.js';

describe('add', () => {
  let tmpDir;
  let configDir;
  let sourceDir;
  let db;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-add-test-'));
    configDir = path.join(tmpDir, 'config');
    sourceDir = path.join(tmpDir, 'home');
    fs.mkdirSync(configDir, { recursive: true });
    fs.mkdirSync(sourceDir, { recursive: true });
    db = openDb(configDir);
  });

  after(() => {
    // cleanup handled per test via tmpDir
  });

  it('moves a file to config dir and creates symlink', () => {
    const sourcePath = path.join(sourceDir, '.zshrc');
    fs.writeFileSync(sourcePath, 'export PATH=$PATH');

    addFile(db, { sourcePath, configDir });

    // File should be in config dir
    assert.ok(fs.existsSync(path.join(configDir, '.zshrc')));
    // Original location should be a symlink
    const stat = fs.lstatSync(sourcePath);
    assert.ok(stat.isSymbolicLink());
    // Symlink should point to config dir (absolute path)
    const target = fs.readlinkSync(sourcePath);
    assert.equal(target, path.join(configDir, '.zshrc'));
    // Content should be readable via symlink
    assert.equal(fs.readFileSync(sourcePath, 'utf8'), 'export PATH=$PATH');
    // Database should have entry
    const entry = getEntry(db, '.zshrc');
    assert.equal(entry.name, '.zshrc');
    assert.equal(entry.original_path, sourcePath);
    assert.equal(entry.type, 'file');
    assert.equal(entry.status, 'linked');

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('moves a directory to config dir and creates symlink', () => {
    const sourcePath = path.join(sourceDir, '.ssh');
    fs.mkdirSync(sourcePath);
    fs.writeFileSync(path.join(sourcePath, 'config'), 'Host *');

    addFile(db, { sourcePath, configDir });

    assert.ok(fs.existsSync(path.join(configDir, '.ssh', 'config')));
    const stat = fs.lstatSync(sourcePath);
    assert.ok(stat.isSymbolicLink());
    assert.equal(fs.readlinkSync(sourcePath), path.join(configDir, '.ssh'));

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('uses custom name when provided', () => {
    const sourcePath = path.join(sourceDir, 'settings.json');
    fs.writeFileSync(sourcePath, '{}');

    addFile(db, { sourcePath, configDir, name: 'vscode-settings' });

    assert.ok(fs.existsSync(path.join(configDir, 'vscode-settings')));
    const entry = getEntry(db, 'vscode-settings');
    assert.equal(entry.name, 'vscode-settings');

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('throws if source does not exist', () => {
    assert.throws(
      () => addFile(db, { sourcePath: '/nonexistent', configDir }),
      { message: /does not exist/ }
    );
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('throws if source is already a symlink', () => {
    const realFile = path.join(sourceDir, 'real');
    fs.writeFileSync(realFile, 'data');
    const linkPath = path.join(sourceDir, 'link');
    fs.symlinkSync(realFile, linkPath);

    assert.throws(
      () => addFile(db, { sourcePath: linkPath, configDir }),
      { message: /already a symlink/ }
    );
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('throws if name conflicts in config dir', () => {
    const sourcePath = path.join(sourceDir, '.zshrc');
    fs.writeFileSync(sourcePath, 'data');
    fs.writeFileSync(path.join(configDir, '.zshrc'), 'existing');

    assert.throws(
      () => addFile(db, { sourcePath, configDir }),
      { message: /already exists in iCloud directory/ }
    );
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/add.test.js`

Expected: FAIL — module not found.

- [ ] **Step 3: Write implementation**

Create `lib/add.js`:

```js
import fs from 'node:fs';
import path from 'node:path';
import { addEntry } from './db.js';

export function addFile(db, { sourcePath, configDir, name, force = false }) {
  const resolvedSource = path.resolve(sourcePath);

  // Validate source exists
  if (!fs.existsSync(resolvedSource)) {
    throw new Error(`Source "${resolvedSource}" does not exist.`);
  }

  // Check if source is already a symlink
  const sourceStat = fs.lstatSync(resolvedSource);
  if (sourceStat.isSymbolicLink()) {
    throw new Error(`Source "${resolvedSource}" is already a symlink.`);
  }

  const destName = name ?? path.basename(resolvedSource);
  const destPath = path.join(configDir, destName);

  // Check for conflict in config dir
  if (fs.existsSync(destPath) && !force) {
    throw new Error(
      `"${destName}" already exists in iCloud directory. Use --force to overwrite.`
    );
  }

  const type = sourceStat.isDirectory() ? 'directory' : 'file';

  // Move to config dir
  // Use rename first, fall back to copy+delete for cross-device
  try {
    fs.renameSync(resolvedSource, destPath);
  } catch (err) {
    if (err.code === 'EXDEV') {
      fs.cpSync(resolvedSource, destPath, { recursive: true });
      fs.rmSync(resolvedSource, { recursive: true, force: true });
    } else {
      throw err;
    }
  }

  // Create symlink at original location (absolute path)
  fs.symlinkSync(destPath, resolvedSource);

  // Record in database
  addEntry(db, { name: destName, originalPath: resolvedSource, type, status: 'linked' });

  return { name: destName, originalPath: resolvedSource, type };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/add.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/add.js test/add.test.js
git commit -m "feat: add command to move files to iCloud with symlinks"
```

---

### Task 8: Recover Command (`lib/recover.js`)

**Files:**
- Create: `test/recover.test.js`
- Create: `lib/recover.js`

- [ ] **Step 1: Write the failing test**

Create `test/recover.test.js`:

```js
import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { openDb, addEntry, getEntry } from '../lib/db.js';
import { recoverFile } from '../lib/recover.js';

describe('recover', () => {
  let tmpDir, configDir, originalDir, db;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'anchor-recover-test-'));
    configDir = path.join(tmpDir, 'config');
    originalDir = path.join(tmpDir, 'home');
    fs.mkdirSync(configDir, { recursive: true });
    fs.mkdirSync(originalDir, { recursive: true });
    db = openDb(configDir);
  });

  it('recovers a file: copies back, removes symlink, deletes from iCloud and db', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    // Simulate state after "add": file in config dir, symlink at original
    fs.writeFileSync(path.join(configDir, '.zshrc'), 'export PATH=$PATH');
    fs.symlinkSync(path.join(configDir, '.zshrc'), originalPath);
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });

    recoverFile(db, { name: '.zshrc', configDir });

    // Original should be a real file, not a symlink
    assert.ok(fs.existsSync(originalPath));
    assert.ok(!fs.lstatSync(originalPath).isSymbolicLink());
    assert.equal(fs.readFileSync(originalPath, 'utf8'), 'export PATH=$PATH');
    // Config dir should not have the file
    assert.ok(!fs.existsSync(path.join(configDir, '.zshrc')));
    // Database entry should be removed
    assert.equal(getEntry(db, '.zshrc'), undefined);

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('recovers a directory', () => {
    const originalPath = path.join(originalDir, '.ssh');
    fs.mkdirSync(path.join(configDir, '.ssh'));
    fs.writeFileSync(path.join(configDir, '.ssh', 'config'), 'Host *');
    fs.symlinkSync(path.join(configDir, '.ssh'), originalPath);
    addEntry(db, { name: '.ssh', originalPath, type: 'directory', status: 'linked' });

    recoverFile(db, { name: '.ssh', configDir });

    assert.ok(fs.existsSync(path.join(originalPath, 'config')));
    assert.ok(!fs.lstatSync(originalPath).isSymbolicLink());
    assert.ok(!fs.existsSync(path.join(configDir, '.ssh')));

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('throws if entry not found in database', () => {
    assert.throws(
      () => recoverFile(db, { name: 'nonexistent', configDir }),
      { message: /not found/ }
    );
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('throws if file conflicts at original path', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    fs.writeFileSync(path.join(configDir, '.zshrc'), 'icloud version');
    fs.writeFileSync(originalPath, 'local version'); // real file, not symlink
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });

    assert.throws(
      () => recoverFile(db, { name: '.zshrc', configDir }),
      { message: /already exists at original path/ }
    );

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('recovers with --force when file conflicts at original path', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    fs.writeFileSync(path.join(configDir, '.zshrc'), 'icloud version');
    fs.writeFileSync(originalPath, 'local version');
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });

    recoverFile(db, { name: '.zshrc', configDir, force: true });

    assert.equal(fs.readFileSync(originalPath, 'utf8'), 'icloud version');

    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test test/recover.test.js`

Expected: FAIL — module not found.

- [ ] **Step 3: Write implementation**

Create `lib/recover.js`:

```js
import fs from 'node:fs';
import path from 'node:path';
import { getEntry, deleteEntry } from './db.js';

export function recoverFile(db, { name, configDir, force = false }) {
  const entry = getEntry(db, name);
  if (!entry) {
    throw new Error(`Entry "${name}" not found.`);
  }

  const icloudPath = path.join(configDir, name);
  const originalPath = entry.original_path;

  // Validate the file exists in iCloud
  if (!fs.existsSync(icloudPath)) {
    throw new Error(`"${name}" not found in iCloud directory.`);
  }

  if (originalPath) {
    // Check if something exists at original path
    if (fs.existsSync(originalPath)) {
      const stat = fs.lstatSync(originalPath);
      if (stat.isSymbolicLink()) {
        // It's a symlink (expected state) — remove it
        fs.unlinkSync(originalPath);
      } else if (!force) {
        throw new Error(
          `File already exists at original path "${originalPath}". Use --force to overwrite.`
        );
      } else {
        // Force: remove the existing file/directory
        fs.rmSync(originalPath, { recursive: true, force: true });
      }
    }

    // Ensure parent directory exists
    fs.mkdirSync(path.dirname(originalPath), { recursive: true });

    // Copy from iCloud back to original location
    const icloudStat = fs.lstatSync(icloudPath);
    if (icloudStat.isDirectory()) {
      fs.cpSync(icloudPath, originalPath, { recursive: true });
    } else {
      fs.copyFileSync(icloudPath, originalPath);
    }
  }

  // Remove from iCloud directory
  fs.rmSync(icloudPath, { recursive: true, force: true });

  // Remove from database
  deleteEntry(db, name);

  return { name, originalPath };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test test/recover.test.js`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/recover.js test/recover.test.js
git commit -m "feat: add recover command to restore files from iCloud"
```

---

### Task 9: CLI Wiring (`bin/anchor.js`)

**Files:**
- Modify: `bin/anchor.js`

- [ ] **Step 1: Write the full CLI entry point**

Replace `bin/anchor.js` with:

```js
#!/usr/bin/env node

import { program } from 'commander';
import chalk from 'chalk';
import { getICloudDrivePath, getConfigDir } from '../lib/icloud.js';
import { openDb } from '../lib/db.js';
import { initAnchor } from '../lib/init.js';
import { listEntries } from '../lib/list.js';
import { addFile } from '../lib/add.js';
import { recoverFile } from '../lib/recover.js';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const pkg = require('../package.json');

program
  .name('anchor')
  .description('Sync config files to iCloud Drive via symlinks')
  .version(pkg.version);

program
  .command('init')
  .description('Initialize anchor: create iCloud config directory and database')
  .option('--dir <name>', 'config directory name in iCloud Drive', 'config')
  .action((options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const result = initAnchor({ icloudBase, dirName: options.dir });
      console.log(chalk.green('Anchor initialized!'));
      console.log(`Config directory: ${result.configDir}`);
      if (result.discovered > 0) {
        console.log(`Discovered ${result.discovered} existing file(s).`);
      }
      if (result.entries.length > 0) {
        console.log(`\nTracked entries:`);
        for (const entry of result.entries) {
          const orig = entry.original_path ?? chalk.dim('(unknown)');
          console.log(`  ${entry.name} -> ${orig}`);
        }
      }
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('list')
  .description('List all tracked config files')
  .option('--json', 'output as JSON')
  .action((options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);
      const output = listEntries(db, { json: options.json });
      console.log(output);
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('add <source>')
  .description('Add a file or directory to iCloud and create a symlink')
  .option('--name <name>', 'custom name in iCloud directory')
  .option('--force', 'overwrite if already exists', false)
  .action((source, options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);
      const result = addFile(db, {
        sourcePath: source,
        configDir,
        name: options.name,
        force: options.force,
      });
      console.log(chalk.green(`Added "${result.name}" to iCloud.`));
      console.log(`  ${result.originalPath} -> ${configDir}/${result.name}`);
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program
  .command('recover <name>')
  .description('Recover a file from iCloud back to its original location')
  .option('--force', 'overwrite if file exists at original location', false)
  .action((name, options) => {
    try {
      const icloudBase = getICloudDrivePath();
      const configDir = getConfigDir(icloudBase);
      const db = openDb(configDir);
      const result = recoverFile(db, {
        name,
        configDir,
        force: options.force,
      });
      console.log(chalk.green(`Recovered "${result.name}".`));
      if (result.originalPath) {
        console.log(`  Restored to: ${result.originalPath}`);
      }
      db.close();
    } catch (err) {
      console.error(chalk.red(`Error: ${err.message}`));
      process.exit(1);
    }
  });

program.parse();
```

- [ ] **Step 2: Make the file executable**

Run: `chmod +x bin/anchor.js`

- [ ] **Step 3: Test CLI help output**

Run: `node bin/anchor.js --help`

Expected output should show:
```
Usage: anchor [options] [command]

Sync config files to iCloud Drive via symlinks

Options:
  -V, --version   output the version number
  -h, --help      display help for command

Commands:
  init [options]  Initialize anchor: create iCloud config directory and database
  list [options]  List all tracked config files
  add [options] <source>  Add a file or directory to iCloud and create a symlink
  recover [options] <name>  Recover a file from iCloud back to its original location
  help [command]  display help for command
```

- [ ] **Step 4: Test each subcommand help**

Run: `node bin/anchor.js init --help`

Expected: Shows `--dir` option.

Run: `node bin/anchor.js add --help`

Expected: Shows `<source>` argument, `--name` and `--force` options.

- [ ] **Step 5: Commit**

```bash
git add bin/anchor.js
git commit -m "feat: wire CLI commands with commander"
```

---

### Task 10: Homebrew Formula

**Files:**
- Create: `Formula/anchor.rb`

- [ ] **Step 1: Create the Homebrew Formula**

Create `Formula/anchor.rb`:

```ruby
class Anchor < Formula
  desc "Sync config files to iCloud Drive via symlinks"
  homepage "https://github.com/vissong/anchor"
  url "https://github.com/vissong/anchor/archive/refs/tags/v0.1.0.tar.gz"
  # sha256 will be filled after first release
  sha256 ""
  license "MIT"

  depends_on "node"

  def install
    system "npm", "install", "--production"
    libexec.install Dir["*"]
    bin.install_symlink libexec/"bin/anchor.js" => "anchor"
  end

  test do
    assert_match "anchor", shell_output("#{bin}/anchor --version")
  end
end
```

- [ ] **Step 2: Verify formula syntax**

Run: `ruby -c Formula/anchor.rb`

Expected: `Syntax OK`

- [ ] **Step 3: Commit**

```bash
git add Formula/anchor.rb
git commit -m "chore: add Homebrew formula"
```

---

### Task 11: npm link and End-to-End Smoke Test

**Files:**
- No new files — integration test of the full workflow.

- [ ] **Step 1: Link the CLI globally**

Run: `npm link`

Expected: `anchor` command available globally.

- [ ] **Step 2: Run all unit tests**

Run: `npm test`

Expected: All tests PASS.

- [ ] **Step 3: Smoke test the full workflow**

Run the following commands in sequence to test the full lifecycle:

```bash
# Initialize (uses real iCloud Drive)
anchor init --dir anchor-test

# Verify config dir was created
ls ~/Library/Mobile\ Documents/com~apple~CloudDocs/anchor-test/

# Create a test file to add
echo "test content" > /tmp/anchor-test-file.txt

# Add the test file
anchor add /tmp/anchor-test-file.txt --name test-file.txt

# Verify symlink was created
ls -la /tmp/anchor-test-file.txt

# List entries
anchor list

# List as JSON
anchor list --json

# Recover the file
anchor recover test-file.txt

# Verify file is back and not a symlink
ls -la /tmp/anchor-test-file.txt
cat /tmp/anchor-test-file.txt

# Cleanup
rm -rf ~/Library/Mobile\ Documents/com~apple~CloudDocs/anchor-test/
rm /tmp/anchor-test-file.txt
```

- [ ] **Step 4: Commit any fixes found during smoke testing**

```bash
git add -A
git commit -m "fix: address issues from smoke testing" # only if there are fixes
```

---

### Task 12: README

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write README**

Create `README.md`:

````markdown
# Anchor

Sync your config files to iCloud Drive via symlinks. Keep dotfiles and app configs in sync across all your Macs automatically.

## How It Works

Anchor moves config files/directories into an iCloud Drive directory, then creates symlinks at the original locations. iCloud syncs the files, and symlinks ensure your apps still find their configs.

## Install

```bash
brew tap vissong/anchor
brew install anchor
```

Or via npm:

```bash
npm install -g anchor-cli
```

## Usage

### Initialize

```bash
anchor init
anchor init --dir dotfiles  # custom directory name
```

### Add a config file

```bash
anchor add ~/.zshrc
anchor add ~/.ssh --name ssh-config
anchor add ~/.config/alacritty
```

### List tracked configs

```bash
anchor list
anchor list --json
```

### Recover a config file

```bash
anchor recover .zshrc
anchor recover --force .zshrc  # overwrite existing file
```

## License

MIT
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: add README"
```
