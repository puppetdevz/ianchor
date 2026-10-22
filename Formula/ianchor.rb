class Ianchor < Formula
  desc "Sync config files to iCloud Drive via symlinks"
  homepage "https://github.com/puppetdevz/ianchor"
  url "__IANCHOR_URL__"
  version "__IANCHOR_VERSION__"
  sha256 "__IANCHOR_SHA256__"
  license "MIT"

  depends_on "node"

  def install
    node = Formula["node"].opt_bin/"node"
    npm = Formula["node"].opt_libexec/"bin/npm"
    ENV.prepend_path "PATH", Formula["node"].opt_bin

    ohai "Locked dependencies; lifecycle scripts disabled"
    system node, npm, "ci", "--ignore-scripts", "--omit=dev", "--engine-strict", "--no-audit", "--no-fund"
    system node, "--input-type=commonjs", "-e", prebuild_script

    libexec.install "bin", "lib", "package.json", "package-lock.json", "node_modules"
    (bin/"ianchor").write_env_script(node, ["\"#{libexec}/bin/ianchor.js\""], {})
    system node, "--input-type=module", "-e", sqlite_probe, libexec/"lib/db.js", buildpath/"sqlite-install-check"
  end

  # Shared verbatim with CI: never run the dependency's default install script.
  def prebuild_script
    <<~'JS'
      const assert = require('node:assert/strict');
      const fs = require('node:fs');
      const os = require('node:os');
      const path = require('node:path');
      const { createRequire } = require('node:module');
      const { spawnSync } = require('node:child_process');
      const runtime = `node=${process.version}, abi=${process.versions.modules}, platform=${process.platform}, arch=${process.arch}`;
      console.log(`SQLite prebuilt-only installation: ${runtime}, executable=${process.execPath}`);
      let stage = 'locked dependency validation';
      let cache;
      try {
        const lock = require('./package-lock.json').packages;
        const sqliteFile = require.resolve('better-sqlite3/package.json');
        const sqlite = require(sqliteFile);
        const fromSqlite = createRequire(sqliteFile);
        const tool = fromSqlite.resolve('prebuild-install/bin.js');
        assert.equal(sqlite.version, lock['node_modules/better-sqlite3'].version);
        assert.equal(fromSqlite('prebuild-install/package.json').version, lock['node_modules/prebuild-install'].version);
        cache = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-prebuild-cache-'));
        const env = { ...process.env, npm_config_cache: cache, NPM_CONFIG_CACHE: cache };
        for (const key of Object.keys(env)) {
          if (/^npm_config_build_from_source$/i.test(key)) delete env[key];
        }
        const asset = `better-sqlite3-v${sqlite.version}-node-v${process.versions.modules}-${process.platform}-${process.arch}.tar.gz`;
        const url = `https://github.com/WiseLibs/better-sqlite3/releases/download/v${sqlite.version}/${asset}`;
        stage = 'prebuild download';
        const result = spawnSync(process.execPath, [
          tool, '--path=.', '--runtime=node', `--target=${process.versions.node}`,
          `--platform=${process.platform}`, `--arch=${process.arch}`, `--download=${url}`,
          '--tag-prefix=v', '--no-build-from-source', '--no-force', '--no-debug', '--nolocal', '--verbose',
        ], { cwd: path.dirname(sqliteFile), env, stdio: 'inherit' });
        if (result.error) throw result.error;
        assert.equal(result.status, 0, `download status=${result.status}, signal=${result.signal}`);
        stage = 'SQLite load/read/write';
        const db = new (require('better-sqlite3'))(':memory:');
        try {
          db.exec('CREATE TABLE smoke (value INTEGER); INSERT INTO smoke VALUES (42)');
          assert.equal(db.prepare('SELECT value FROM smoke').get().value, 42);
        } finally {
          db.close();
        }
      } catch (error) {
        console.error(`SQLite ${stage} stage failed: ${runtime}; ${error.message}`);
        process.exitCode = 1;
      } finally {
        if (cache) fs.rmSync(cache, { recursive: true, force: true });
      }
    JS
  end

  def sqlite_probe
    <<~'JS'
      import assert from 'node:assert/strict';
      import fs from 'node:fs';
      import path from 'node:path';
      import { pathToFileURL } from 'node:url';
      const [modulePath, dir] = process.argv.slice(1);
      fs.mkdirSync(dir, { recursive: true });
      const { openDb, addEntry, getEntry } = await import(pathToFileURL(modulePath).href);
      const db = openDb(dir);
      try {
        assert.equal(db.pragma('journal_mode', { simple: true }), 'wal');
        addEntry(db, { name: 'homebrew-test', originalPath: path.join(dir, 'fixture'), type: 'file' });
        const entry = getEntry(db, 'homebrew-test');
        assert.equal(entry.name, 'homebrew-test');
        assert.equal(entry.status, 'linked');
        assert.equal(entry.original_path, path.join(dir, 'fixture'));
      } finally {
        db.close();
      }
    JS
  end

  test do
    assert_equal version.to_s, shell_output("\"#{bin}/ianchor\" --version").strip
    system Formula["node"].opt_bin/"node", "--input-type=module", "-e", sqlite_probe,
           libexec/"lib/db.js", testpath/"sqlite-check"
  end
end
