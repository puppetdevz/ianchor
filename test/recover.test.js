import { describe, it, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { openDb, addEntry, getEntry } from '../lib/db.js';
import { recoverFile } from '../lib/recover.js';
import { relinkFile } from '../lib/relink.js';
import { listEntries } from '../lib/list.js';

describe('recover', () => {
  let tmpDir, configDir, originalDir, db;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-recover-test-'));
    configDir = path.join(tmpDir, 'config');
    originalDir = path.join(tmpDir, 'home');
    fs.mkdirSync(configDir, { recursive: true });
    fs.mkdirSync(originalDir, { recursive: true });
    db = openDb(configDir);
  });

  afterEach(() => {
    db.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  it('recovers only the local file while preserving another device link and the shared entry', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    const icloudPath = path.join(configDir, '.zshrc');
    const otherDevicePath = path.join(tmpDir, 'other-home', '.zshrc');
    fs.mkdirSync(path.dirname(otherDevicePath));
    fs.writeFileSync(icloudPath, 'export PATH=$PATH');
    // Two home directories model devices whose links share the same cloud file.
    fs.symlinkSync(icloudPath, originalPath);
    fs.symlinkSync(icloudPath, otherDevicePath);
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });
    const entryBefore = getEntry(db, '.zshrc');

    const result = recoverFile(db, { name: '.zshrc', configDir });

    assert.deepEqual(result, { name: '.zshrc', originalPath });
    assert.ok(!fs.lstatSync(originalPath).isSymbolicLink());
    assert.equal(fs.readFileSync(originalPath, 'utf8'), 'export PATH=$PATH');
    assert.equal(fs.readFileSync(icloudPath, 'utf8'), 'export PATH=$PATH');
    assert.deepEqual(getEntry(db, '.zshrc'), entryBefore);
    assert.ok(fs.lstatSync(otherDevicePath).isSymbolicLink());
    assert.equal(fs.readFileSync(otherDevicePath, 'utf8'), 'export PATH=$PATH');

    const bakPath = path.join(configDir, '.bak', '.zshrc');
    assert.equal(fs.readFileSync(bakPath, 'utf8'), 'export PATH=$PATH');

    // Local edits after recovery must not reach the other device or cloud copy.
    fs.writeFileSync(originalPath, 'local-only version');
    assert.equal(fs.readFileSync(otherDevicePath, 'utf8'), 'export PATH=$PATH');
    assert.equal(fs.readFileSync(icloudPath, 'utf8'), 'export PATH=$PATH');
  });

  it('recovers a directory without deleting the cloud directory or shared entry', () => {
    const originalPath = path.join(originalDir, '.ssh');
    const icloudPath = path.join(configDir, '.ssh');
    const otherDevicePath = path.join(tmpDir, 'other-home', '.ssh');
    fs.mkdirSync(path.dirname(otherDevicePath));
    fs.mkdirSync(icloudPath);
    fs.writeFileSync(path.join(icloudPath, 'config'), 'Host *');
    fs.symlinkSync(icloudPath, originalPath);
    fs.symlinkSync(icloudPath, otherDevicePath);
    addEntry(db, { name: '.ssh', originalPath, type: 'directory', status: 'linked' });
    const entryBefore = getEntry(db, '.ssh');

    recoverFile(db, { name: '.ssh', configDir });

    assert.ok(!fs.lstatSync(originalPath).isSymbolicLink());
    assert.equal(fs.readFileSync(path.join(originalPath, 'config'), 'utf8'), 'Host *');
    assert.equal(fs.readFileSync(path.join(icloudPath, 'config'), 'utf8'), 'Host *');
    assert.equal(fs.readFileSync(path.join(otherDevicePath, 'config'), 'utf8'), 'Host *');
    assert.deepEqual(getEntry(db, '.ssh'), entryBefore);
    assert.equal(fs.readFileSync(path.join(configDir, '.bak', '.ssh', 'config'), 'utf8'), 'Host *');

    fs.writeFileSync(path.join(originalPath, 'config'), 'local Host *');
    assert.equal(fs.readFileSync(path.join(otherDevicePath, 'config'), 'utf8'), 'Host *');
  });

  it('shows the recovered file as locally unlinked and allows relinking it', async () => {
    const originalPath = path.join(originalDir, '.zshrc');
    const icloudPath = path.join(configDir, '.zshrc');
    fs.writeFileSync(icloudPath, 'cloud version');
    fs.symlinkSync(icloudPath, originalPath);
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });

    recoverFile(db, { name: '.zshrc', configDir });

    const entries = JSON.parse(await listEntries(db, { json: true, configDir }));
    assert.equal(entries.length, 1);
    assert.equal(entries[0].status, 'unlinked');

    fs.writeFileSync(originalPath, 'local edits');
    relinkFile(db, { name: '.zshrc', configDir });
    assert.ok(fs.lstatSync(originalPath).isSymbolicLink());
    assert.equal(fs.readFileSync(originalPath, 'utf8'), 'cloud version');
    assert.equal(fs.readFileSync(`${originalPath}.bak`, 'utf8'), 'local edits');
  });

  it('throws if entry not found in database', () => {
    assert.throws(
      () => recoverFile(db, { name: 'nonexistent', configDir }),
      { message: /not found/ }
    );
  });

  it('throws if the cloud file is missing and leaves the entry and local link intact', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    const icloudPath = path.join(configDir, '.zshrc');
    fs.symlinkSync(icloudPath, originalPath);
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });
    const entryBefore = getEntry(db, '.zshrc');

    assert.throws(
      () => recoverFile(db, { name: '.zshrc', configDir }),
      { message: /not found in iCloud directory/ }
    );
    assert.ok(fs.lstatSync(originalPath).isSymbolicLink());
    assert.deepEqual(getEntry(db, '.zshrc'), entryBefore);
  });

  it('rejects recovery without an original path and preserves the cloud file and entry', () => {
    const icloudPath = path.join(configDir, 'mystery');
    fs.writeFileSync(icloudPath, 'cloud version');
    addEntry(db, { name: 'mystery', originalPath: null, type: 'file', status: 'unlinked' });
    const entryBefore = getEntry(db, 'mystery');

    assert.throws(
      () => recoverFile(db, { name: 'mystery', configDir }),
      { message: /no original path/ }
    );
    assert.equal(fs.readFileSync(icloudPath, 'utf8'), 'cloud version');
    assert.deepEqual(getEntry(db, 'mystery'), entryBefore);
  });

  it('throws if file conflicts at original path but backup still exists', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    fs.writeFileSync(path.join(configDir, '.zshrc'), 'icloud version');
    fs.writeFileSync(originalPath, 'local version');
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });
    const entryBefore = getEntry(db, '.zshrc');

    assert.throws(
      () => recoverFile(db, { name: '.zshrc', configDir }),
      { message: /already exists at original path/ }
    );

    assert.equal(fs.readFileSync(path.join(configDir, '.bak', '.zshrc'), 'utf8'), 'icloud version');
    assert.equal(fs.readFileSync(path.join(configDir, '.zshrc'), 'utf8'), 'icloud version');
    assert.equal(fs.readFileSync(originalPath, 'utf8'), 'local version');
    assert.deepEqual(getEntry(db, '.zshrc'), entryBefore);
  });

  it('recovers with --force without changing the cloud file or shared entry', () => {
    const originalPath = path.join(originalDir, '.zshrc');
    const icloudPath = path.join(configDir, '.zshrc');
    fs.writeFileSync(icloudPath, 'icloud version');
    fs.writeFileSync(originalPath, 'local version');
    addEntry(db, { name: '.zshrc', originalPath, type: 'file', status: 'linked' });
    const entryBefore = getEntry(db, '.zshrc');

    recoverFile(db, { name: '.zshrc', configDir, force: true });

    assert.equal(fs.readFileSync(originalPath, 'utf8'), 'icloud version');
    assert.equal(fs.readFileSync(icloudPath, 'utf8'), 'icloud version');
    assert.deepEqual(getEntry(db, '.zshrc'), entryBefore);
  });
});
