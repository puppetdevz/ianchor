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
const hasPty = spawnSync('python3', ['-c', 'import pty, fcntl, termios']).status === 0;

function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ianchor-completion-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const bin = path.join(dir, 'bin');
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, 'ianchor'), `#!/bin/sh\nexec ${quote(process.execPath)} ${quote(cli)} "$@"\n`, { mode: 0o755 });
  const configDir = path.join(dir, 'Library/Mobile Documents/com~apple~CloudDocs/ianchor');
  fs.mkdirSync(configDir, { recursive: true });
  const db = openDb(configDir);
  for (const name of ['zshrc', 'app config', 'colon:name', 'colon:a:b', 'back\\slash', 'wild*card', '$(touch COMPLETION_EXECUTED)', 'line\nbreak', 'line\rbreak', '-n', '--help', 'dollar$(touch COMPLETION_EXECUTED)', `quote's"name`, '!bang']) {
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

function bashTab(f, line, shell = '/bin/bash') {
  const rc = path.join(f.dir, 'bashrc');
  const inputrc = path.join(f.dir, 'inputrc');
  fs.writeFileSync(inputrc, 'set bell-style none\nset show-all-if-ambiguous on\n');
  fs.writeFileSync(rc, `source ${quote(path.join(f.dir, 'completion.bash'))}\nPS1='__READY__ '\nPS2=''\nbind 'set bell-style none'\nbind '\"\\\\C-i\": complete'\n`);
  const driver = path.join(f.dir, 'bash-tab.py');
  fs.writeFileSync(driver, `import os, pty, select, time, sys, signal, fcntl, termios, struct
pid, fd = pty.fork()
if pid == 0:
    os.chdir(sys.argv[1])
    os.execvpe(sys.argv[2], [sys.argv[2], '--noprofile', '--rcfile', sys.argv[3], '-i'], os.environ)
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 240, 0, 0))
buf = b''
def drain(timeout):
    global buf
    while select.select([fd], [], [], timeout)[0]:
        chunk = os.read(fd, 65536)
        if not chunk:
            break
        buf += chunk
try:
    deadline = time.monotonic() + 5
    while b'__READY__ ' not in buf and time.monotonic() < deadline:
        drain(0.1)
    if b'__READY__ ' not in buf:
        raise RuntimeError('bash prompt not ready')
    os.write(fd, sys.argv[4].encode())
    drain(0.7)
    # Force a full redraw: assert on the final buffer, never on echoed input.
    os.write(fd, b'\\x0c')
    drain(0.3)
finally:
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    os.close(fd)
sys.stdout.buffer.write(buf)
`);
  const result = spawnSync('python3', [driver, f.dir, shell, rc, line], { cwd: f.dir, env: { ...f.env, INPUTRC: inputrc }, encoding: 'utf8', timeout: 10000 });
  result.buffer = stripVTControlCharacters(result.stdout || '').split('__READY__ ').at(-1).replaceAll('\r', '').replaceAll('\n', '');
  return result;
}

function bashComplete(f, words, shell) {
  const cword = words.length - 1;
  return spawnSync(shell, ['--noprofile', '--norc', '-c', `
before=$COMP_WORDBREAKS
source ${quote(path.join(f.dir, 'completion.bash'))}
source ${quote(path.join(f.dir, 'completion.bash'))}
[[ "$COMP_WORDBREAKS" == "$before" ]] || exit 91
COMP_WORDS=(${words.map(quote).join(' ')})
COMP_CWORD=${cword}
_ianchor_complete
[[ "$COMP_WORDBREAKS" == "$before" ]] || exit 92
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

  it('__complete emits literal NUL-delimited names without changing database contents', t => {
    const f = fixture(t);
    const dbPath = path.join(f.dir, 'Library/Mobile Documents/com~apple~CloudDocs/ianchor/ianchor.db');
    const before = fs.readFileSync(dbPath);
    const result = spawnSync(process.execPath, [cli, '__complete'], { env: f.env, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stderr, '');
    assert.ok(result.stdout.endsWith('\0'));
    const names = result.stdout.split('\0').slice(0, -1);
    assert.ok(names.includes('app config'));
    assert.ok(names.includes('$(touch COMPLETION_EXECUTED)'));
    assert.ok(names.includes('--help'));
    assert.ok(!names.includes('line'));
    assert.ok(!names.includes('break'));
    assert.deepEqual(fs.readFileSync(dbPath), before);
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
      ['ianchor recover colon:a\t', 'ianchor recover colon:a:b '],
      ['ianchor recover -- -n\t', 'ianchor recover -- -n '],
      ['ianchor recover -- --h\t', 'ianchor recover -- --help '],
      ['ianchor recover back\t', 'ianchor recover back\\\\slash '],
      ['ianchor recover wild\t', 'ianchor recover wild\\*card '],
      ['ianchor recover col\t', 'ianchor recover colon:'],
    ]) it(`zsh ${mode}: Tab completes ${line.replace('\t', '<Tab>')}`, { skip: !hasShell('zsh') }, t => {
      const result = zshTab(fixture(t), line, mode);
      assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
      assert.ok(result.stdout.includes(`__BUFFER__${expected}__DONE__`), result.stdout);
    });
  }

  for (const shell of ['bash', '/bin/bash']) {
    it(`${shell}: one Tab lists commands`, { skip: !hasShell(shell) || !hasPty }, t => {
      const result = bashTab(fixture(t), 'ianchor \t', shell);
      assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
      for (const name of ['init', 'list', 'add', 'recover', 'relink', 'remove']) {
        assert.match(stripVTControlCharacters(result.stdout), new RegExp(`\\b${name}\\b`));
      }
      assert.equal(result.buffer, 'ianchor ');
    });
    for (const [line, expected] of [
      ['ianchor recover col\t', 'ianchor recover colon:'],
      ['ianchor recover colon:n\t', 'ianchor recover colon:name '],
      ['ianchor recover colon\\:n\t', 'ianchor recover colon:name '],
      ['ianchor recover colon\\:a:\t', 'ianchor recover colon\\:a:b '],
      ['ianchor recover zs\t', 'ianchor recover zshrc '],
      ['ianchor recover app\t', 'ianchor recover app\\ config '],
      ['ianchor recover colon:a\t', 'ianchor recover colon:a:b '],
      ['ianchor recover zs\t\u0015ianchor recover colon:a\t', 'ianchor recover colon:a:b '],
      ['ianchor recover -- -n\t', 'ianchor recover -- -n '],
      ['ianchor recover -- --h\t', 'ianchor recover -- --help '],
      ['ianchor recover line\t', 'ianchor recover line'],
      ['ianchor recover --force zs\t', 'ianchor recover --force zshrc '],
      ['ianchor remove -y zs\t', 'ianchor remove -y zshrc '],
      ['ianchor recover back\t', 'ianchor recover back\\\\slash '],
      ['ianchor recover wild\t', 'ianchor recover wild\\*card '],
    ]) it(`${shell}: actual Tab inserts ${JSON.stringify(expected)}`, { skip: !hasShell(shell) || !hasPty }, t => {
      const f = fixture(t);
      fs.mkdirSync(path.join(f.dir, 'zshrc'));
      const result = bashTab(f, line, shell);
      assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
      assert.equal(result.buffer, expected, result.stdout);
      assert.equal(fs.existsSync(path.join(f.dir, 'COMPLETION_EXECUTED')), false);
    });
  }

  for (const shell of ['zsh', 'bash', '/bin/bash']) {
    for (const [prefix, name] of [['dollar', 'dollar$(touch COMPLETION_EXECUTED)'], ['quote', `quote's"name`], ['!b', '!bang']]) {
      it(`${shell}: completed ${name} remains one literal argument`, { skip: !hasShell(shell) || (shell !== 'zsh' && !hasPty) }, t => {
        const f = fixture(t);
        // Begin with a safe prefix. An unquoted $() typed by a user is shell
        // syntax before programmable completion, not a literal entry prefix.
        const input = `ianchor recover ${prefix === '!b' ? '\\!b' : prefix}\t`;
        const result = shell === 'zsh' ? zshTab(f, input) : bashTab(f, input, shell);
        assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
        const buffer = shell === 'zsh' ? result.stdout.match(/__BUFFER__(.*?)__DONE__/s)?.[1] : result.buffer;
        assert.ok(buffer, result.stdout);
        // Only set/printf run here; executing a bad completion can at most
        // touch the fixture's marker, which must remain absent.
        const parsed = spawnSync(shell, ['-c', `set -- ${buffer}; printf '%s\\0' "$@"`], { cwd: f.dir, env: f.env, encoding: 'utf8' });
        assert.equal(parsed.status, 0, parsed.stderr);
        assert.deepEqual(parsed.stdout.split('\0').slice(0, -1), ['ianchor', 'recover', name]);
        assert.equal(fs.existsSync(path.join(f.dir, 'COMPLETION_EXECUTED')), false);
      });
    }
  }

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
      [['ianchor', '-h'], ['-h ']],
      [['ianchor', '-V'], ['-V ']],
      [['ianchor', 'recover', '--', '-n'], ['-n ']],
      [['ianchor', 'recover', '--', '--h'], ['--help ']],
      [['ianchor', 'recover', 'line'], []],
      [['ianchor', 'recover', 'colon:a'], ['a:b ']],
      [['ianchor', 'recover', 'colon\\:n'], ['colon:name ']],
      [['ianchor', 'recover', 'colon', ':', 'a'], ['a:b ']],
      [['ianchor', 'recover', 'colon', ':', 'a', ':', ''], ['b ']],
      [['ianchor', 'recover', 'app\\ c'], ['app\\ config ']],
      [['ianchor', 'recover', ''], null],
      [['ianchor', 'remove', '-y', 'zs'], ['zshrc ']],
      [['ianchor', 'recover', 'zshrc', ''], []],
      [['ianchor', 'add', '--name', 'sam'], []],
    ]) it(`${shell} completes ${words.join(' ')}`, { skip: !hasShell(shell) }, t => {
      const f = fixture(t);
      const result = bashComplete(f, words, shell);
      assert.equal(result.status, 0, result.stderr);
      const actual = result.stdout === '\n' || result.stdout === '' ? [] : result.stdout.split('\n');
      if (actual.at(-1) === '') actual.pop();
      if (expected === null) {
        assert.ok(actual.includes('\\$\\(touch\\ COMPLETION_EXECUTED\\) '));
        assert.ok(!actual.includes('-n '));
        assert.ok(!actual.includes('--help '));
      } else assert.deepEqual(actual, expected);
      assert.equal(fs.existsSync(path.join(f.dir, 'COMPLETION_EXECUTED')), false);
    });
  }
});
