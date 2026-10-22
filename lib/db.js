import path from 'node:path';
import Database from 'better-sqlite3';

const DB_FILENAME = 'ianchor.db';

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

export function openDb(configDir, { readonly = false } = {}) {
  const dbPath = path.join(configDir, DB_FILENAME);
  const db = new Database(dbPath, { readonly, fileMustExist: readonly });
  if (readonly) return db;
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
