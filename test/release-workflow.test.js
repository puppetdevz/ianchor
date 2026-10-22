import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';

const root = fileURLToPath(new URL('../', import.meta.url));
const workflow = fs.readFileSync(path.join(root, '.github/workflows/release.yml'), 'utf8');
const templatePath = path.join(root, 'Formula/ianchor.rb');
const sha = createHash('sha256').update('source archive fixture').digest('hex');
const git = spawnSync('which', ['git'], { encoding: 'utf8' }).stdout.trim();
const shasum = spawnSync('which', ['shasum'], { encoding: 'utf8' }).stdout.trim();

// Fail rather than silently testing an empty or truncated YAML step.
export function stepScript(name) {
  const step = workflow.split(`      - name: ${name}\n`)[1]?.split('\n      - ')[0];
  assert.ok(step, `Missing workflow step: ${name}`);
  const run = step.match(/^        run: \|\n([\s\S]*)/m);
  assert.ok(run, `Missing multiline run command: ${name}`);
  assert.ok(run[1].trim(), `Empty workflow step: ${name}`);
  return run[1].split('\n').map(line => line.replace(/^          /, '')).join('\n');
}

function legacy(version = '1.0.0') {
  return `class Ianchor < Formula\n  url "https://github.com/puppetdevz/ianchor/archive/refs/tags/v${version}.tar.gz"\n  sha256 "${sha}"\n  depends_on "node@22"\nend\n`;
}

function fixture(t, options = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-publish-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const source = path.join(dir, 'source');
  const seed = path.join(dir, 'seed');
  const remote = path.join(dir, 'remote.git');
  const mocks = path.join(dir, 'mocks');
  const tmp = path.join(dir, 'tmp');
  for (const location of [source, seed, mocks, tmp]) fs.mkdirSync(location);
  fs.mkdirSync(path.join(source, 'Formula'));
  fs.copyFileSync(templatePath, path.join(source, 'Formula/ianchor.rb'));
  fs.writeFileSync(path.join(source, 'package.json'), JSON.stringify({ version: '9.9.9' }));
  fs.writeFileSync(path.join(seed, 'README.md'), 'unchanged tap readme\n');
  fs.writeFileSync(path.join(seed, 'unrelated.txt'), 'unchanged root file\n');
  if (!options.noDirectory) {
    fs.mkdirSync(path.join(seed, 'Formula'));
    fs.writeFileSync(path.join(seed, 'Formula/other.rb'), 'unchanged other formula\n');
    if (options.old !== null) fs.writeFileSync(path.join(seed, 'Formula/ianchor.rb'), options.old ?? legacy());
  }
  const env = {
    ...process.env,
    HOME: dir,
    TMPDIR: `${tmp}/`,
    GIT_CONFIG_NOSYSTEM: '1',
    GIT_CONFIG_GLOBAL: '/dev/null',
    GIT_TERMINAL_PROMPT: '0',
    GIT_AUTHOR_NAME: 'fixture', GIT_AUTHOR_EMAIL: 'fixture@example.invalid',
    GIT_COMMITTER_NAME: 'fixture', GIT_COMMITTER_EMAIL: 'fixture@example.invalid',
    TAG: 'v9.9.9', TAP_TOKEN: 'tap-secret-must-not-leak', SOURCE_TOKEN: 'source-secret-must-not-leak',
    REMOTE: remote, LOG: path.join(dir, 'commands.log'), REAL_GIT: git, REAL_SHASUM: shasum,
    RELEASE_STATE: path.join(dir, 'release-state'), SHA: sha,
  };
  const realGit = (args, cwd = seed) => {
    const result = spawnSync(git, args, { cwd, encoding: 'utf8', env });
    assert.equal(result.status, 0, `${args.join(' ')}: ${result.stderr}`);
    return result.stdout.trim();
  };
  realGit(['init', '-b', options.branch ?? 'main']);
  realGit(['add', '.']);
  realGit(['commit', '-m', 'seed']);
  realGit(['init', '--bare', remote]);
  realGit(['remote', 'add', 'origin', remote]);
  realGit(['push', 'origin', options.branch ?? 'main']);
  const executable = (name, body) => fs.writeFileSync(path.join(mocks, name), `#!${process.execPath}\n${body}`, { mode: 0o755 });
  executable('git', `
const fs = require('node:fs'); const {spawnSync} = require('node:child_process');
const args = process.argv.slice(2), e = process.env;
fs.appendFileSync(e.LOG, 'git ' + args.join(' ') + '\\n');
if (args[0] === 'clone') {
  if (e.CLONE_FAIL) process.exit(128);
  const url = args.indexOf('https://github.com/puppetdevz/homebrew-puppetdevz.git');
  if (url < 0) process.exit(90);
  args[url] = e.REMOTE;
}
if (args[0] === 'push' && e.PUSH_FAIL) process.exit(1);
if (args[0] === 'diff' && e.DIFF_FAIL) process.exit(128);
if (args[0] === 'ls-remote' && e.RACE) {
  // A genuine competing commit, after clone and version comparison.
  const seed = e.SEED;
  fs.appendFileSync(seed + '/unrelated.txt', 'competitor\\n');
  for (const a of [['add', 'unrelated.txt'], ['commit', '-m', 'competitor'], ['push', 'origin', 'main']]) {
    const r = spawnSync(e.REAL_GIT, a, {cwd: seed, env: e, encoding: 'utf8'});
    if (r.status !== 0) process.exit(92);
  }
}
const r = spawnSync(e.REAL_GIT, args, {env: e, encoding: 'utf8'});
process.stdout.write(r.stdout || ''); process.stderr.write(r.stderr || '');
if (r.status === 0 && args[0] === 'clone') {
  const dest = args.at(-1), parent = dest + '/Formula', leaf = parent + '/ianchor.rb';
  if (e.SYMLINK) {
    fs.rmSync(parent, {recursive:true, force:true}); fs.symlinkSync(e.SEED, parent);
  } else if (e.PATH_TYPE === 'parent-file') {
    fs.rmSync(parent, {recursive:true, force:true}); fs.writeFileSync(parent, 'not a directory');
  } else if (e.PATH_TYPE) {
    fs.rmSync(leaf, {recursive:true, force:true});
    if (e.PATH_TYPE === 'leaf-directory') fs.mkdirSync(leaf);
    else fs.symlinkSync(e.SEED + '/README.md', leaf);
  }
}
process.exit(r.status ?? 93);
`);
  executable('curl', `
const fs = require('node:fs'), a = process.argv.slice(2), e = process.env;
fs.appendFileSync(e.LOG, 'curl ' + a.join(' ') + '\\n');
if (e.DOWNLOAD_FAIL) process.exit(22);
if (!a.includes('--fail') || !a.includes('--location')) process.exit(91);
fs.writeFileSync(a[a.indexOf('--output') + 1], 'source archive fixture');
`);
  executable('shasum', `
const fs = require('node:fs'), a = process.argv.slice(2), e = process.env;
fs.appendFileSync(e.LOG, 'shasum ' + a.join(' ') + '\\n');
if (a[0] !== '-a' || a[1] !== '256' || fs.readFileSync(a.at(-1), 'utf8') !== 'source archive fixture') process.exit(91);
if (e.SHA === '${sha}') {
  const r = require('node:child_process').spawnSync(e.REAL_SHASUM, a, {encoding:'utf8'});
  process.stdout.write(r.stdout || ''); process.exit(r.status ?? 93);
}
process.stdout.write(e.SHA + '  ' + a.at(-1) + '\\n');
`);
  executable('gh', `
const fs = require('node:fs'), a = process.argv.slice(2), e = process.env;
fs.appendFileSync(e.LOG, 'gh ' + a.join(' ') + '\\n');
if (a[0] === 'api' && !a.includes('--include')) process.exit(Number(e.REPO_FAIL || 0));
if (a[0] === 'api' && a.includes('--include')) {
  if (e.QUERY_TIMEOUT) { process.stderr.write(e.QUERY_ERROR || 'request timed out'); process.exit(1); }
  const status = Number(e.HTTP_STATUS || (fs.existsSync(e.RELEASE_STATE) ? 200 : 404));
  const body = e.BAD_RESPONSE ? 'not json' : JSON.stringify({tag_name:e.WRONG_RELEASE ? 'v0.0.0' : e.TAG});
  process.stdout.write('HTTP/2.0 ' + status + ' fixture\\r\\n\\r\\n' + body + '\\n');
  process.exit(status === 200 ? 0 : 1);
}
if (a[0] === 'release' && a[1] === 'create') {
  if (e.CREATE_FAIL) process.exit(7);
  fs.writeFileSync(e.RELEASE_STATE, e.TAG); process.exit(0);
}
process.exit(90);
`);
  // No fallback command may accidentally reach the host, even in failure cases.
  for (const name of ['python', 'python3', 'node-gyp', 'cc', 'gcc', 'clang', 'npm']) {
    executable(name, `require('node:fs').appendFileSync(process.env.LOG, 'FORBIDDEN ${name}\\n'); process.exit(99);`);
  }
  const run = (extra = {}) => {
    fs.writeFileSync(env.LOG, '');
    const result = spawnSync('/bin/bash', ['-e', '-o', 'pipefail', '-c', stepScript('Publish release and formula')], {
      cwd: source, encoding: 'utf8', env: { ...env, SEED: seed, PATH: `${mocks}:${process.env.PATH}`, ...extra },
    });
    assert.ifError(result.error);
    const log = fs.readFileSync(env.LOG, 'utf8');
    assert.doesNotMatch(log + result.stdout + result.stderr, /tap-secret-must-not-leak|source-secret-must-not-leak|FORBIDDEN/);
    assert.deepEqual(fs.readdirSync(tmp), [], 'temporary files must be cleaned up');
    return { ...result, log };
  };
  const count = () => Number(realGit(['--git-dir', remote, 'rev-list', '--count', 'main']));
  const read = (name) => realGit(['--git-dir', remote, 'show', `main:${name}`]);
  const readBytes = name => {
    const r = spawnSync(git, ['--git-dir', remote, 'show', `main:${name}`], { env });
    assert.equal(r.status, 0, r.stderr?.toString());
    return r.stdout;
  };
  return { dir, source, seed, remote, env, realGit, run, count, read, readBytes };
}

function noPublish(result) {
  assert.notEqual(result.status, 0, result.stdout + result.stderr);
  assert.doesNotMatch(result.log, /gh release create|git (add|commit|push) /);
}

function succeeded(result) { assert.equal(result.status, 0, result.stdout + result.stderr); }

function unchangedOthers(f) {
  for (const name of ['README.md', 'unrelated.txt', 'Formula/other.rb']) {
    assert.deepEqual(f.readBytes(name), fs.readFileSync(path.join(f.seed, name)), `${name} must remain byte-for-byte identical`);
  }
}

describe('release workflow: real isolated Git index and mocked network', () => {
  it('uses Homebrew node, tag env, non-persisted credentials and serialized main publishing', () => {
    assert.doesNotMatch(workflow, /setup-node|node-version:|run: npm install|TAG="\$\{\{/);
    assert.match(workflow, /persist-credentials: false/);
    assert.match(workflow, /group: homebrew-puppetdevz-ianchor/);
    assert.match(workflow, /cancel-in-progress: false/);
    assert.match(workflow, /TAG: \$\{\{ github.ref_name \}\}/);
    assert.match(stepScript('Use Homebrew Node'), /brew --prefix node/);
    assert.match(stepScript('Install precompiled dependencies'), /ci.*--ignore-scripts/);
    assert.match(stepScript('Run tests'), /npm test/);
  });

  for (const noDirectory of [false, true]) it(`creates first formula with real staged diff (directory absent=${noDirectory})`, t => {
    const f = fixture(t, { old: null, noDirectory });
    const r = f.run(); succeeded(r);
    assert.equal(f.count(), 2);
    assert.equal(f.realGit(['--git-dir', f.remote, 'diff-tree', '--no-commit-id', '--name-only', '-r', 'main']), 'Formula/ianchor.rb');
    assert.match(f.read('Formula/ianchor.rb'), /version "9\.9\.9"/);
    assert.ok(f.read('Formula/ianchor.rb').includes(`sha256 "${sha}"`));
    assert.ok(f.read('Formula/ianchor.rb').includes('url "https://github.com/puppetdevz/ianchor/archive/refs/tags/v9.9.9.tar.gz"'));
    assert.match(r.log, /git clone --branch main --single-branch https:\/\/github.com\/puppetdevz\/homebrew-puppetdevz.git/);
    assert.match(r.log, /git add -- Formula\/ianchor.rb/);
    assert.match(r.log, /git push origin HEAD:main/);
    assert.equal((r.log.match(/gh release create/g) ?? []).length, 1);
    if (!noDirectory) unchangedOthers(f);
  });

  it('fully replaces legacy template, updates only ianchor, reruns as zero-commit no-op', t => {
    const f = fixture(t);
    succeeded(f.run()); unchangedOthers(f);
    assert.doesNotMatch(f.read('Formula/ianchor.rb'), /node@22|__IANCHOR_/);
    const r = f.run(); succeeded(r);
    assert.equal(f.count(), 2);
    assert.doesNotMatch(r.log, /git commit|git push|gh release create/);
  });

  it('repairs changed template at same version without recreating Release', t => {
    const f = fixture(t); succeeded(f.run());
    fs.appendFileSync(path.join(f.source, 'Formula/ianchor.rb'), '# template improvement\n');
    const r = f.run(); succeeded(r);
    assert.equal(f.count(), 3);
    assert.match(f.read('Formula/ianchor.rb'), /template improvement/);
    assert.doesNotMatch(r.log, /gh release create/);
  });

  for (const tag of ['v1.0.0-beta.1', 'v01.0.0', '1.0.0', 'v1.0.0', 'v9.9.9$(touch injected)']) {
    it(`rejects invalid/mismatching tag safely: ${tag}`, t => {
      const f = fixture(t); noPublish(f.run({ TAG: tag }));
      assert.equal(fs.existsSync(path.join(f.source, 'injected')), false);
    });
  }
  for (const old of [legacy('10.0.0'), legacy('9.10.0'), 'broken formula\n', legacy().replace('end\n', '  version "2.0.0"\nend\n'), legacy().replace('end\n', '  version "10.0.0" # explicit override\nend\n'), legacy().replace('end\n', '  version("10.0.0")\nend\n')]) {
    it(`refuses downgrade or damaged/conflicting version: ${old.slice(0, 60)}`, t => {
      const f = fixture(t, { old }); noPublish(f.run()); assert.equal(f.count(), 1);
    });
  }
  for (const declaration of ['self.version "10.0.0"', 'desc "legacy"; version "10.0.0"', 'send(:version, "10.0.0")', 'method(:version).call("10.0.0")']) {
    it(`rejects conflicting Ruby metadata outside line-based declarations: ${declaration}`, t => {
      const old = legacy().replace('end\n', `  ${declaration}\nend\n`);
      const f = fixture(t, { old }); noPublish(f.run()); assert.equal(f.count(), 1);
    });
  }
  it('ignores version-like text in Ruby comments and heredocs without executing tap code', t => {
    const f = fixture(t, { old: legacy().replace('end\n', '=begin\n  version "10.0.0"\n=end\n  def unused\n    <<~TEXT\n  version "10.0.0"\n    TEXT\n    raise "tap code must never execute"\n  end\nend\n') });
    succeeded(f.run()); assert.equal(f.count(), 2);
  });
  it('compares numeric version segments rather than lexicographically', t => {
    const f = fixture(t, { old: legacy('9.9.10') }); noPublish(f.run());
  });
  for (const fields of [{ TAP_TOKEN: '' }, { SOURCE_TOKEN: '' }, { CLONE_FAIL: '1' }, { SYMLINK: '1' }, { PATH_TYPE: 'parent-file' }, { PATH_TYPE: 'leaf-directory' }, { PATH_TYPE: 'leaf-symlink' }]) {
    it(`rejects missing credentials, clone failure or symlink: ${JSON.stringify(fields)}`, t => noPublish(fixture(t).run(fields)));
  }
  it('requires initialized main even if another default branch exists', t => noPublish(fixture(t, { branch: 'other' }).run()));
  for (const mutation of [s => s.replace('__IANCHOR_URL__', ''), s => s + '# __IANCHOR_SHA256__\n', s => s + '# __IANCHOR_UNKNOWN__\n', s => s + 'not valid ruby !\n', s => s.replace('depends_on "node"', 'depends_on "python"'), s => s.replace('version "__IANCHOR_VERSION__"', '# __IANCHOR_VERSION__'), s => s.replace('depends_on "node"', 'depends_on "node"\n  depends_on("python")'), s => s + '# __IANCHOR_unfinished\n', s => s + 'version "10.0.0" # override\n']) {
    it('rejects malformed, ambiguous or non-installable rendered template before Release', t => {
      const f = fixture(t); const p = path.join(f.source, 'Formula/ianchor.rb');
      fs.writeFileSync(p, mutation(fs.readFileSync(p, 'utf8'))); noPublish(f.run());
    });
  }
  for (const declaration of [/  url [\s\S]*?  sha256 [^\n]+/, /  depends_on "node"/]) {
    it(`rejects rendered declarations hidden inside a Ruby block comment: ${declaration}`, t => {
      const f = fixture(t); const p = path.join(f.source, 'Formula/ianchor.rb');
      const source = fs.readFileSync(p, 'utf8').replace(declaration, '=begin\n$&\n=end');
      fs.writeFileSync(p, source); noPublish(f.run()); assert.equal(f.count(), 1);
    });
  }
  for (const wrapper of ['if false', 'def deferred_dependency']) {
    it(`rejects a Node dependency that is not an unconditional class declaration: ${wrapper}`, t => {
      const f = fixture(t); const p = path.join(f.source, 'Formula/ianchor.rb');
      fs.writeFileSync(p, fs.readFileSync(p, 'utf8').replace('  depends_on "node"', `  ${wrapper}\n  depends_on "node"\n  end`));
      noPublish(f.run()); assert.equal(f.count(), 1);
    });
  }
  for (const fields of [{ DOWNLOAD_FAIL: '1' }, { SHA: '' }, { SHA: 'invalid' }, { SHA: 'a'.repeat(63) }]) {
    it(`fails download/checksum before publishing: ${JSON.stringify(fields)}`, t => {
      const r = fixture(t).run(fields); noPublish(r);
      if (fields.DOWNLOAD_FAIL) assert.doesNotMatch(r.log, /shasum/);
    });
  }
  for (const fields of [{ REPO_FAIL: '1' }, { HTTP_STATUS: '401' }, { HTTP_STATUS: '403' }, { HTTP_STATUS: '429' }, { HTTP_STATUS: '500' }, { QUERY_TIMEOUT: '1' }, { HTTP_STATUS: '200', BAD_RESPONSE: '1' }, { HTTP_STATUS: '200', WRONG_RELEASE: '1' }]) {
    it(`does not mistake query error for missing Release: ${JSON.stringify(fields)}`, t => noPublish(fixture(t).run(fields)));
  }
  for (const [error, category, guidance] of [
    ['dial tcp: lookup api.github.com: no such host', 'DNS', /DNS|resolver/i],
    ['x509: certificate signed by unknown authority', 'TLS', /certificate|TLS/i],
    ['request timed out', 'network', /proxy|connectivity/i],
  ]) it(`reports a safe Release query diagnosis and recovery guidance: ${category}`, t => {
    const f = fixture(t);
    const r = f.run({ QUERY_TIMEOUT: '1', QUERY_ERROR: `${error}; ${f.env.TAP_TOKEN}; ${f.env.SOURCE_TOKEN}` });
    noPublish(r);
    assert.match(r.stderr, new RegExp(`Release query.*${category}`, 'i'));
    assert.match(r.stderr, guidance);
    assert.match(r.stderr, /rerun the same tag/i);
  });
  it('skips existing Release but still updates tap', t => {
    const f = fixture(t); fs.writeFileSync(f.env.RELEASE_STATE, 'v9.9.9');
    const r = f.run(); succeeded(r); assert.doesNotMatch(r.log, /gh release create/); assert.equal(f.count(), 2);
  });
  it('propagates Release creation failure without committing tap', t => {
    const f = fixture(t); const r = f.run({ CREATE_FAIL: '1' });
    assert.notEqual(r.status, 0); assert.doesNotMatch(r.log, /git add|git commit|git push/);
    assert.equal(f.count(), 1);
  });
  it('keeps Release after failed push, then repairs without recreating it', t => {
    const f = fixture(t); const r = f.run({ PUSH_FAIL: '1' });
    assert.notEqual(r.status, 0); assert.equal(f.count(), 1);
    assert.ok(fs.existsSync(f.env.RELEASE_STATE)); assert.doesNotMatch(r.log, /release delete|--force/);
    const second = f.run(); succeeded(second); assert.equal(f.count(), 2);
    assert.doesNotMatch(second.log, /gh release create/);
  });
  it('propagates real remote push rejection and repairs on rerun', t => {
    const f = fixture(t);
    const hook = path.join(f.remote, 'hooks/pre-receive');
    fs.writeFileSync(hook, '#!/bin/sh\nexit 1\n', { mode: 0o755 });
    const r = f.run(); assert.notEqual(r.status, 0); assert.equal(f.count(), 1);
    assert.match(r.stderr, /pre-receive hook declined/); assert.doesNotMatch(r.log, /--force/);
    assert.ok(fs.existsSync(f.env.RELEASE_STATE));
    fs.rmSync(hook); const repaired = f.run(); succeeded(repaired);
    assert.equal(f.count(), 2); assert.doesNotMatch(repaired.log, /gh release create/);
  });
  it('checks remote advancement even when the candidate Formula is unchanged', t => {
    const rendered = fs.readFileSync(templatePath, 'utf8').replace('__IANCHOR_VERSION__', '9.9.9').replace('__IANCHOR_URL__', 'https://github.com/puppetdevz/ianchor/archive/refs/tags/v9.9.9.tar.gz').replace('__IANCHOR_SHA256__', sha);
    const f = fixture(t, { old: rendered });
    const r = f.run({ RACE: '1' }); assert.notEqual(r.status, 0);
    assert.equal(f.count(), 2); assert.doesNotMatch(r.log, /git commit|git push/);
  });
  it('detects genuine remote advancement instead of overwriting it', t => {
    const f = fixture(t); const r = f.run({ RACE: '1' });
    assert.notEqual(r.status, 0); assert.equal(f.count(), 2);
    assert.doesNotMatch(r.log, /git push|--force/);
    assert.equal(f.read('unrelated.txt'), 'unchanged root file\ncompetitor');
  });
  it('propagates index/diff errors rather than treating them as no-op', t => {
    const r = fixture(t).run({ DIFF_FAIL: '1' }); assert.notEqual(r.status, 0);
    assert.doesNotMatch(r.log, /git commit|git push/);
  });
  it('documents template ownership, precompiled-only constraints, prerequisites and migration bilingually', () => {
    for (const name of ['README.md', 'README.en.md']) {
      const s = fs.readFileSync(path.join(root, name), 'utf8');
      for (const text of ['brew tap puppetdevz/puppetdevz', 'brew install puppetdevz/puppetdevz/ianchor', 'brew upgrade puppetdevz/puppetdevz/ianchor', 'brew uninstall ianchor', 'brew untap vissong/ianchor', 'Formula/ianchor.rb', 'HOMEBREW_TAP_TOKEN', 'better-sqlite3']) assert.ok(s.includes(text), `${name}: ${text}`);
      assert.doesNotMatch(s, /brew (install|upgrade) puppetdevz\/ianchor\/ianchor/);
    }
    const instructions = fs.readFileSync(path.join(root, 'AGENTS.md'), 'utf8');
    assert.match(instructions, /Formula\/ianchor\.rb/);
    assert.doesNotMatch(instructions, /puppetdevz\/homebrew-ianchor/);
  });
});
