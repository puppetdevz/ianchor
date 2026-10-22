import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { stripVTControlCharacters } from 'node:util';
import { openDb, addEntry } from '../lib/db.js';

const root = fileURLToPath(new URL('../', import.meta.url));
const cli = path.join(root, 'bin/ianchor.js');
const quote = value => `'${value.replaceAll("'", "'\\''")}'`;
const hasShell = shell => spawnSync(shell, ['-c', 'exit 0']).status === 0;

function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-completion-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const bin = path.join(dir, 'bin');
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, 'ianchor'), `#!/bin/sh\nexec ${quote(process.execPath)} ${quote(cli)} "$@"\n`, { mode: 0o755 });
  const configDir = path.join(dir, 'Library/Mobile Documents/com~apple~CloudDocs/ianchor');
  fs.mkdirSync(configDir, { recursive: true });
  const db = openDb(configDir);
  for (const name of ['zshrc', 'app config', 'colon:name', 'back\\slash', 'wild*card', '$(touch COMPLETION_EXECUTED)']) {
    addEntry(db, { name, originalPath: path.join(dir, name), type: 'file' });
  }
  db.close();
  fs.writeFileSync(path.join(dir, 'sample.txt'), 'fixture');
  const env = { ...process.env, HOME: dir, ZDOTDIR: dir, PATH: `${bin}:${process.env.PATH}`, TERM: 'xterm', LC_ALL: 'C' };
  for (const shell of ['zsh', 'bash']) {
    const result = spawnSync(process.execPath, [cli, 'completion', shell], { env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    fs.writeFileSync(path.join(dir, `completion.${shell}`), result.stdout);
  }
  return { dir, env };
}

function zshTab(f, line, mode = 'source') {
  const site = path.join(f.dir, 'site-functions');
  fs.mkdirSync(site, { recursive: true });
  fs.copyFileSync(path.join(f.dir, 'completion.zsh'), path.join(site, '_ianchor'));
  fs.writeFileSync(path.join(f.dir, '.zshrc'), `
${mode === 'autoload' ? `fpath=(${quote(site)} $fpath)` : ''}
autoload -Uz compinit
compinit -D -i
${mode === 'source' ? `source ${quote(path.join(f.dir, 'completion.zsh'))}` : mode === 'eval' ? 'eval "$(ianchor completion zsh)"' : ''}
bindkey -e
setopt AUTO_LIST
unsetopt MENU_COMPLETE AUTO_MENU
PROMPT='__READY__ '
RPROMPT=''
_capture_completion() {
  zle complete-word
  print -r -- ""
  print -r -- "__BUFFER__\${BUFFER}__DONE__"
}
zle -N _capture_completion
bindkey '^I' _capture_completion
`);
  return spawnSync('zsh', ['-df', '-c', `
zmodload zsh/zpty || exit 77
zpty shell zsh -d
zpty -r shell reply '*__READY__ *'
for input in "$@"; do
  zpty -w -n shell "$input"
  zpty -r shell reply '*__DONE__*'
  print -r -- "$reply"
done
zpty -d shell
`, '--', ...(Array.isArray(line) ? line : [line])], { cwd: f.dir, env: f.env, encoding: 'utf8', timeout: 10000 });
}

function bashTab(f, line) {
  const rc = path.join(f.dir, 'bashrc');
  fs.writeFileSync(rc, `source ${quote(path.join(f.dir, 'completion.bash'))}\nPS1='__READY__ '\nPS2=''\nbind 'set bell-style none'\nbind '\"\\\\C-i\": complete'\n`);
  return spawnSync('zsh', ['-df', '-c', `
zmodload zsh/zpty || exit 77
zpty shell /bin/bash --noprofile --rcfile "$1" -i
zpty -r shell reply '*__READY__ *'
zpty -w -n shell "$2"
# Prefix the completed command with printf, then execute only that builtin.
zpty -w -n shell $'\\x01'"printf '__ARG__%s\\\\n' "$'\\x05'"; printf '__DONE__\\\\n'"$'\\n'
zpty -r shell reply $'*\\r\\n__DONE__\\r\\n*'
print -r -- "$reply"
zpty -d shell
`, '--', rc, line], { cwd: f.dir, env: f.env, encoding: 'utf8', timeout: 10000 });
}

function bashComplete(f, words, shell) {
  const cword = words.length - 1;
  return spawnSync(shell, ['--noprofile', '--norc', '-c', `
source ${quote(path.join(f.dir, 'completion.bash'))}
COMP_WORDS=(${words.map(quote).join(' ')})
COMP_CWORD=${cword}
_ianchor_complete
printf '%s\\n' "\${COMPREPLY[@]}"
`], { cwd: f.dir, env: f.env, encoding: 'utf8', timeout: 10000 });
}

describe('shell completion', () => {
  it('rejects unsupported shells instead of returning a bash script', () => {
    const result = spawnSync(process.execPath, [cli, 'completion', 'fish'], { encoding: 'utf8' });
    assert.notEqual(result.status, 0);
    assert.equal(result.stdout, '');
    assert.match(result.stderr, /Unsupported shell/);
  });

  it('__complete does not initialize a missing database', t => {
    const f = fixture(t);
    const configDir = path.join(f.dir, 'Library/Mobile Documents/com~apple~CloudDocs/ianchor');
    fs.rmSync(configDir, { recursive: true });
    fs.mkdirSync(configDir);
    const result = spawnSync(process.execPath, [cli, '__complete'], { env: f.env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, '');
    assert.equal(result.stderr, '');
    assert.deepEqual(fs.readdirSync(configDir), []);
  });

  for (const contents of [null, 'not a SQLite database']) {
    it(`__complete is silent and leaves an uninitialized config untouched (${contents === null ? 'missing directory' : 'corrupt database'})`, t => {
      const f = fixture(t);
      const configDir = path.join(f.dir, 'Library/Mobile Documents/com~apple~CloudDocs/ianchor');
      fs.rmSync(configDir, { recursive: true });
      if (contents !== null) {
        fs.mkdirSync(configDir);
        fs.writeFileSync(path.join(configDir, 'ianchor.db'), contents);
      }
      const result = spawnSync(process.execPath, [cli, '__complete'], { env: f.env, encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr);
      assert.equal(result.stdout, '');
      assert.equal(result.stderr, '');
      if (contents === null) assert.equal(fs.existsSync(configDir), false);
      else {
        assert.deepEqual(fs.readdirSync(configDir), ['ianchor.db']);
        assert.equal(fs.readFileSync(path.join(configDir, 'ianchor.db'), 'utf8'), contents);
      }
    });
  }

  it('sources zsh completion without executing completion widgets and registers the command', { skip: !hasShell('zsh') }, t => {
    const f = fixture(t);
    const result = spawnSync('zsh', ['-df', '-c', `autoload -Uz compinit; compinit -D -i; source ${quote(path.join(f.dir, 'completion.zsh'))}; print -r -- "\${_comps[ianchor]-missing}"`], { env: f.env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stderr, '');
    assert.equal(result.stdout.trim(), '_ianchor');
  });

  for (const mode of ['source', 'eval', 'autoload']) {
    it(`zsh ${mode}: Tab lists commands and survives another completion`, { skip: !hasShell('zsh') }, t => {
      const result = zshTab(fixture(t), ['ianchor \t', '\u0015ianchor ini\t'], mode);
      assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
      for (const command of ['init', 'list', 'add', 'recover', 'relink', 'remove']) {
        assert.match(stripVTControlCharacters(result.stdout), new RegExp(`\\b${command}\\b`));
      }
      assert.ok(result.stdout.includes('__BUFFER__ianchor init __DONE__'), result.stdout);
    });
    for (const [line, expected] of [
      ['ianchor ini\t', 'ianchor init '],
      ['ianchor recover zs\t', 'ianchor recover zshrc '],
      ['ianchor recover --force zs\t', 'ianchor recover --force zshrc '],
      ['ianchor remove -y zs\t', 'ianchor remove -y zshrc '],
      ['ianchor add sam\t', 'ianchor add sample.txt '],
      ['ianchor list --j\t', 'ianchor list --json '],
      ['ianchor recover app\t', 'ianchor recover app\\ config '],
      ['ianchor recover col\t', 'ianchor recover colon:name '],
    ]) it(`zsh ${mode}: Tab completes ${line.replace('\t', '<Tab>')}`, { skip: !hasShell('zsh') }, t => {
      const result = zshTab(fixture(t), line, mode);
      assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
      assert.ok(result.stdout.includes(`__BUFFER__${expected}__DONE__`), result.stdout);
    });
  }

  for (const [line, name] of [
    ['ianchor recover col\\t', 'colon:name'],
    ['ianchor recover colon:n\\t', 'colon:name'],
    ['ianchor recover zs\\t', 'zshrc'],
    ['ianchor recover app\\t', 'app config'],
  ]) it(`/bin/bash: actual Tab inserts the literal entry ${name}`, { skip: !hasShell('zsh') || !hasShell('/bin/bash') }, t => {
    const f = fixture(t);
    fs.mkdirSync(path.join(f.dir, 'zshrc'));
    const result = bashTab(f, line.replaceAll('\\t', '\t'));
    assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
    assert.match(result.stdout, new RegExp(`__ARG__${name}\\r?\\n`));
  });

  // macOS /bin/bash is 3.2; PATH may select a newer Homebrew bash.
  for (const shell of ['bash', '/bin/bash']) {
    for (const [words, expected] of [
      [['ianchor', 're'], ['recover ', 'relink ', 'remove ']],
      [['ianchor', 'recover', 'zs'], ['zshrc ']],
      [['ianchor', 'recover', '--force', 'zs'], ['zshrc ']],
      [['ianchor', 'recover', 'app'], ['app\\ config ']],
      [['ianchor', 'add', 'sam'], ['sample.txt ']],
      [['ianchor', 'list', '--j'], ['--json ']],
      [['ianchor', 'recover', 'back'], ['back\\\\slash ']],
      [['ianchor', 'recover', 'wild'], ['wild\\*card ']],
      [['ianchor', 'recover', '$'], ['\\$\\(touch\\ COMPLETION_EXECUTED\\) ']],
      [['ianchor', 'remove', '-y', 'zs'], ['zshrc ']],
      [['ianchor', 'recover', 'zshrc', ''], []],
      [['ianchor', 'add', '--name', 'sam'], []],
    ]) it(`${shell} completes ${words.join(' ')}`, { skip: !hasShell(shell) }, t => {
      const f = fixture(t);
      const result = bashComplete(f, words, shell);
      assert.equal(result.status, 0, result.stderr);
      const actual = result.stdout === '\n' || result.stdout === '' ? [] : result.stdout.split('\n');
      if (actual.at(-1) === '') actual.pop();
      assert.deepEqual(actual, expected);
      assert.equal(fs.existsSync(path.join(f.dir, 'COMPLETION_EXECUTED')), false);
    });
  }
});
