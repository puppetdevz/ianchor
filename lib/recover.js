import fs from 'node:fs';
import path from 'node:path';
import { getEntry } from './db.js';

const BAK_DIR = '.bak';

function backupToBAk(configDir, name, icloudPath) {
  const bakDir = path.join(configDir, BAK_DIR);
  if (!fs.existsSync(bakDir)) {
    fs.mkdirSync(bakDir, { recursive: true });
  }
  const bakPath = path.join(bakDir, name);
  // Remove old backup if exists
  if (fs.existsSync(bakPath)) {
    fs.rmSync(bakPath, { recursive: true, force: true });
  }
  const stat = fs.lstatSync(icloudPath);
  if (stat.isDirectory()) {
    fs.cpSync(icloudPath, bakPath, { recursive: true });
  } else {
    fs.copyFileSync(icloudPath, bakPath);
  }
}

export function recoverFile(db, { name, configDir, force = false }) {
  const entry = getEntry(db, name);
  if (!entry) {
    throw new Error(`Entry "${name}" not found.`);
  }

  const icloudPath = path.join(configDir, name);
  const originalPath = entry.original_path;

  if (!fs.existsSync(icloudPath)) {
    throw new Error(`"${name}" not found in iCloud directory.`);
  }

  if (!originalPath) {
    throw new Error(`Entry "${name}" has no original path. Cannot recover locally.`);
  }

  // Backup before any destructive operation
  backupToBAk(configDir, name, icloudPath);

  if (fs.existsSync(originalPath)) {
    const stat = fs.lstatSync(originalPath);
    if (stat.isSymbolicLink()) {
      fs.unlinkSync(originalPath);
    } else if (!force) {
      throw new Error(
        `File already exists at original path "${originalPath}". Use --force to overwrite.`
      );
    } else {
      fs.rmSync(originalPath, { recursive: true, force: true });
    }
  }

  fs.mkdirSync(path.dirname(originalPath), { recursive: true });

  const icloudStat = fs.lstatSync(icloudPath);
  if (icloudStat.isDirectory()) {
    fs.cpSync(icloudPath, originalPath, { recursive: true });
  } else {
    fs.copyFileSync(icloudPath, originalPath);
  }

  // Recovery is local only: other devices still need the cloud file and entry.

  return { name, originalPath };
}
