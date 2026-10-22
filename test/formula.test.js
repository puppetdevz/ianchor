import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';

const root = fileURLToPath(new URL('../', import.meta.url));
const formulaFile = path.join(root, 'Formula/ianchor.rb');
function formula() { return fs.readFileSync(formulaFile, 'utf8'); }
function jsMethod(name) {
  const match = formula().match(new RegExp(`  def ${name}\\n    <<~'JS'\\n([\\s\\S]*?)\\n    JS\\n  end`));
  assert.ok(match, `Missing complete Formula JavaScript method: ${name}`);
  return match[1].replace(/^      /gm, '');
}
function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-prebuilt-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const sqlite = path.join(dir, 'node_modules/better-sqlite3');
  const tool = path.join(dir, 'node_modules/prebuild-install');
  const mocks = path.join(dir, 'mocks');
  for (const p of [sqlite, tool, mocks]) fs.mkdirSync(p, { recursive: true });
  fs.writeFileSync(path.join(dir, 'package-lock.json'), JSON.stringify({ packages: {
    'node_modules/better-sqlite3': { version: '12.10.0' },
    'node_modules/prebuild-install': { version: '7.1.3' },
  } }));
  fs.writeFileSync(path.join(sqlite, 'package.json'), JSON.stringify({ name: 'better-sqlite3', version: '12.10.0', main: 'index.js' }));
  fs.writeFileSync(path.join(sqlite, 'index.js'), `module.exports = function() { if (process.env.LOAD_FAIL) throw Error('ABI fixture mismatch'); return {exec(){}, prepare(){return {get(){return {value:42}}}}, close(){}} };`);
  fs.writeFileSync(path.join(tool, 'package.json'), JSON.stringify({ name: 'prebuild-install', version: '7.1.3' }));
  fs.writeFileSync(path.join(tool, 'bin.js'), `
const fs = require('node:fs');
fs.appendFileSync(process.env.LOG, JSON.stringify({argv:process.argv.slice(2), cwd:process.cwd(), env:process.env, exec:process.execPath}) + '\\n');
if (process.env.PREBUILD_FAIL) process.exit(1);
`);
  for (const name of ['python', 'python3', 'node-gyp', 'cc', 'clang', 'gcc', 'npm']) {
    fs.writeFileSync(path.join(mocks, name), `#!/bin/sh\nprintf 'FORBIDDEN ${name}\\n' >> "$LOG"\nexit 99\n`, { mode: 0o755 });
  }
  const log = path.join(dir, 'calls');
  const run = (extra = {}) => spawnSync(process.execPath, ['--input-type=commonjs', '-e', jsMethod('prebuild_script')], {
    cwd: dir, encoding: 'utf8', env: {
      ...process.env, HOME: dir, LOG: log, PATH: `${mocks}:${process.env.PATH}`,
      npm_config_build_from_source: 'true', NPM_CONFIG_BUILD_FROM_SOURCE: 'true',
      npm_config_target: '0.0.0', npm_config_arch: 'wrong', npm_config_platform: 'wrong',
      ...extra,
    },
  });
  return { dir, sqlite, tool, log, run };
}

describe('Formula prebuilt-only installation', () => {
  it('has unique template fields and only the node direct dependency', () => {
    const s = formula();
    for (const key of ['VERSION', 'URL', 'SHA256']) assert.equal(s.split(`__IANCHOR_${key}__`).length - 1, 1);
    assert.deepEqual([...s.matchAll(/^\s*depends_on (.+)$/gm)].map(x => x[1]), ['"node"']);
    assert.match(s, /ci.*--ignore-scripts.*--omit=dev/);
    assert.doesNotMatch(s, /std_npm_args|node-gyp|npm rebuild|python|setup-node/);
    assert.match(s, /write_env_script/);
    assert.match(s, /Formula\["node"\]\.opt_bin\/"node"/);
    assert.match(s, /libexec.install "bin", "lib", "package.json", "package-lock.json", "node_modules"/);
    assert.doesNotMatch(s, /ianchor init|Mobile Documents|\.config\/ianchor/);
  });
  it('installs zsh and bash completions using the installed launcher', () => {
    assert.match(formula(), /generate_completions_from_executable\(bin\/"ianchor", "completion", shells: \[:zsh, :bash\]\)/);
  });
  it('is valid Ruby before and after rendering', () => {
    for (const s of [formula(), formula().replace('__IANCHOR_VERSION__', '9.9.9').replace('__IANCHOR_URL__', 'https://example.invalid/v9.9.9.tar.gz').replace('__IANCHOR_SHA256__', 'a'.repeat(64))]) {
      const r = spawnSync('ruby', ['-c'], { input: s, encoding: 'utf8' });
      assert.equal(r.status, 0, r.stderr);
    }
  });
  it('uses exact current ABI/arch/platform, package cwd, explicit URL and boolean no-build flag', t => {
    const f = fixture(t); const r = f.run(); assert.equal(r.status, 0, r.stderr);
    const calls = fs.readFileSync(f.log, 'utf8'); assert.doesNotMatch(calls, /FORBIDDEN/);
    const call = JSON.parse(calls.trim());
    assert.equal(call.cwd, fs.realpathSync(f.sqlite)); assert.equal(call.exec, fs.realpathSync(process.execPath));
    for (const arg of ['--no-build-from-source', '--no-force', '--no-debug', '--runtime=node', '--path=.', `--target=${process.versions.node}`, `--platform=${process.platform}`, `--arch=${process.arch}`]) assert.ok(call.argv.includes(arg), arg);
    assert.ok(call.argv.includes(`--download=https://github.com/WiseLibs/better-sqlite3/releases/download/v12.10.0/better-sqlite3-v12.10.0-node-v${process.versions.modules}-${process.platform}-${process.arch}.tar.gz`));
    assert.equal(call.env.npm_config_build_from_source, undefined);
    assert.equal(call.env.NPM_CONFIG_BUILD_FROM_SOURCE, undefined);
    assert.doesNotMatch(call.argv.join(' '), /--build-from-source=false/);
  });
  for (const extra of [{ PREBUILD_FAIL: '1' }, { LOAD_FAIL: '1' }]) it(`fails without any compilation fallback: ${JSON.stringify(extra)}`, t => {
    const f = fixture(t); const r = f.run(extra);
    assert.notEqual(r.status, 0); assert.match(r.stderr, /SQLite.*stage.*node=.*abi=.*platform=.*arch=/);
    assert.doesNotMatch(fs.readFileSync(f.log, 'utf8'), /FORBIDDEN/);
  });
  for (const pkg of ['better-sqlite3', 'prebuild-install']) it(`rejects installed ${pkg} differing from lock before download`, t => {
    const f = fixture(t); const p = path.join(f.dir, 'node_modules', pkg, 'package.json');
    const json = JSON.parse(fs.readFileSync(p)); json.version = '99.0.0'; fs.writeFileSync(p, JSON.stringify(json));
    const r = f.run(); assert.notEqual(r.status, 0); assert.equal(fs.existsSync(f.log), false);
  });
  for (const [fail, version] of [[false, '1.0.0'], [false, '2.3.4'], [true, '1.0.0']]) it(`executes Formula.install/test with mocked npm/download and real SQLite (download fails=${fail}, version=${version})`, t => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-formula-dsl-'));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    const build = path.join(dir, 'build');
    const prefix = path.join(dir, 'installed with spaces');
    const brewNode = path.join(dir, 'brew-node');
    const mocks = path.join(dir, 'path-node');
    for (const p of [build, path.join(brewNode, 'bin'), path.join(brewNode, 'libexec/bin'), mocks]) fs.mkdirSync(p, { recursive: true });
    for (const p of ['bin', 'lib']) fs.cpSync(path.join(root, p), path.join(build, p), { recursive: true });
    const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
    fs.writeFileSync(path.join(build, 'package.json'), JSON.stringify({ ...pkg, version }));
    fs.symlinkSync(fs.realpathSync(path.join(root, 'node_modules')), path.join(build, 'node_modules'));
    const require = createRequire(import.meta.url);
    const fromSqlite = createRequire(require.resolve('better-sqlite3/package.json'));
    fs.writeFileSync(path.join(build, 'package-lock.json'), JSON.stringify({ packages: {
      'node_modules/better-sqlite3': { version: require('better-sqlite3/package.json').version },
      'node_modules/prebuild-install': { version: fromSqlite('prebuild-install/package.json').version },
    } }));
    fs.symlinkSync(process.execPath, path.join(brewNode, 'bin/node'));
    fs.writeFileSync(path.join(brewNode, 'libexec/bin/npm'), '// mocked npm only');
    fs.writeFileSync(path.join(mocks, 'node'), '#!/bin/sh\nexit 99\n', { mode: 0o755 });
    const hook = path.join(dir, 'hook.cjs');
    fs.writeFileSync(hook, `const cp=require('node:child_process'); const old=cp.spawnSync; cp.spawnSync=(exe,args,opts)=> { if(args[0].includes('prebuild-install')) return {status:process.env.PREBUILD_FAIL ? 1 : 0}; return old(exe,args,opts); };`);
    const shim = `
require "pathname"
require "fileutils"
require "json"
class << ENV
  def prepend_path(key, value)
    self[key] = "#{value}:#{self[key]}"
  end
end
class Pathname
  def install(*names)
    FileUtils.mkdir_p(to_s)
    names.each { |name| FileUtils.mv(name, self/name) }
  end
  def write_env_script(target, args, env)
    raise "launcher must bind exact Homebrew Node" unless target.to_s == ENV.fetch("BREW_NODE") + "/bin/node"
    raise "entry argument must be quoted" unless args == ["\\\"#{ENV.fetch('PREFIX')}/libexec/bin/ianchor.js\\\""]
    FileUtils.mkdir_p(dirname)
    write("#!/bin/sh\\nexec \\\"#{target}\\\" #{args.join(' ')} \\\"$@\\\"\\n")
    chmod(0555)
  end
end
class Formula
  def self.[](_name)
    Struct.new(:opt_bin, :opt_libexec).new(Pathname(ENV.fetch("BREW_NODE"))/"bin", Pathname(ENV.fetch("BREW_NODE"))/"libexec")
  end
  def self.desc(*) = nil
  def self.homepage(*) = nil
  def self.url(*) = nil
  def self.sha256(*) = nil
  def self.license(*) = nil
  def self.depends_on(*) = nil
  def self.version(value) = @version = value
  def self.test(&block) = @test_block = block
  def self.test_block = @test_block
  def version = ENV.fetch("PACKAGE_VERSION")
  def libexec = Pathname(ENV.fetch("PREFIX"))/"libexec"
  def bin = Pathname(ENV.fetch("PREFIX"))/"bin"
  def generate_completions_from_executable(executable, command, shells:)
    raise "must generate through installed launcher" unless executable == bin/"ianchor"
    shells.each do |shell|
      destination = Pathname(ENV.fetch("PREFIX"))/"share"/(shell == :zsh ? "zsh/site-functions/_ianchor" : "bash-completion/completions/ianchor")
      FileUtils.mkdir_p(destination.dirname)
      output = IO.popen([executable.to_s, command, shell.to_s], &:read)
      raise "completion generation failed" unless $?.success?
      destination.write(output)
    end
  end
  def buildpath = Pathname.pwd
  def testpath = Pathname(ENV.fetch("PREFIX"))/"test"
  def ohai(*) = nil
  def system(*args)
    if args[1].to_s.end_with?("/bin/npm")
      raise "must use Homebrew Node" unless args[0].to_s == ENV.fetch("BREW_NODE") + "/bin/node"
      expected = ["ci", "--ignore-scripts", "--omit=dev", "--engine-strict", "--no-audit", "--no-fund"]
      raise "unlocked or script-enabled npm" unless args.drop(2) == expected
      File.write(ENV.fetch("CALLS"), "locked npm; scripts disabled\\n")
      return true
    end
    ok = Kernel.system({"NODE_OPTIONS" => "--require #{ENV.fetch('HOOK')}"}, *args.map(&:to_s))
    raise "Formula subprocess failed" unless ok
  end
  def shell_output(command)
    output = IO.popen(command, &:read)
    raise "launcher failed" unless $?.success?
    output
  end
  def assert_equal(expected, actual)
    raise "mismatch #{expected.inspect} != #{actual.inspect}" unless expected == actual
  end
end
load ENV.fetch("FORMULA")
f = Ianchor.new
f.install
f.instance_eval(&Ianchor.test_block)
`;
    const r = spawnSync('ruby', ['-e', shim], { cwd: build, encoding: 'utf8', env: {
      ...process.env, HOME: dir, PATH: `${mocks}:${process.env.PATH}`, PREFIX: prefix,
      BREW_NODE: brewNode, FORMULA: formulaFile, HOOK: hook, CALLS: path.join(dir, 'calls'), PACKAGE_VERSION: version,
      ...(fail ? { PREBUILD_FAIL: '1' } : {}),
    } });
    assert.equal(fs.readFileSync(path.join(dir, 'calls'), 'utf8'), 'locked npm; scripts disabled\n');
    if (fail) {
      assert.notEqual(r.status, 0); assert.equal(fs.existsSync(prefix), false);
    } else {
      assert.equal(r.status, 0, r.stdout + r.stderr);
      assert.ok(fs.existsSync(path.join(prefix, 'test/sqlite-check/ianchor.db')));
      assert.ok(fs.existsSync(path.join(prefix, 'libexec/package.json')));
      assert.match(fs.readFileSync(path.join(prefix, 'share/zsh/site-functions/_ianchor'), 'utf8'), /^#compdef ianchor/);
      assert.match(fs.readFileSync(path.join(prefix, 'share/bash-completion/completions/ianchor'), 'utf8'), /complete .*ianchor/);
      // Even when PATH starts with another node, the installed launcher uses its bound interpreter.
      const launched = spawnSync(path.join(prefix, 'bin/ianchor'), ['--version'], { encoding: 'utf8', env: { ...process.env, PATH: mocks } });
      assert.equal(launched.status, 0, launched.stderr); assert.equal(launched.stdout.trim(), version);
    }
    assert.equal(fs.existsSync(path.join(dir, '.config')), false);
    assert.equal(fs.existsSync(path.join(dir, 'Library')), false);
  });
  it('runs the real application SQLite probe in a temporary directory without iCloud', t => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-sqlite-probe-'));
    t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
    const r = spawnSync(process.execPath, ['--input-type=module', '-e', jsMethod('sqlite_probe'), path.join(root, 'lib/db.js'), path.join(dir, 'data')], { encoding: 'utf8', env: { ...process.env, HOME: dir } });
    assert.equal(r.status, 0, r.stderr);
    assert.ok(fs.existsSync(path.join(dir, 'data/ianchor.db')));
    assert.equal(fs.existsSync(path.join(dir, '.config')), false);
    assert.equal(fs.existsSync(path.join(dir, 'Library')), false);
  });
});
