#!/usr/bin/env python3
"""Opt-in baseline CLI differential: actual streams, exit and each workflow state.

Uses an isolated legacy export prepared by sqlite-interop.py; never installs an
ianchor binary, invokes real iCloud, or writes to the source checkout.
"""
import argparse
from datetime import datetime, timezone
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import selectors
import shutil
import signal
import sqlite3
import subprocess
import tempfile
import termios
import time
from job_control_probe import foreground_suspend

REPO = Path(__file__).resolve().parents[2]
BASELINE = '941d9336f1c1b097aa752519ead6af5e009d4664'


def norm(value, home, started):
    if isinstance(value, bytes):
        value = value.decode('utf-8')
    if isinstance(value, str):
        value = value.replace(str(home.resolve()), '<HOME>').replace(str(home), '<HOME>')
        def timestamp(match):
            stamp = match[2]
            instant = datetime.strptime(stamp, '%Y-%m-%d %H:%M:%S').replace(tzinfo=timezone.utc).timestamp()
            assert int(started) <= instant <= int(time.time()), stamp
            return match[1] + '<TS>' + match[3]
        return re.sub(r'("(?:created_at|updated_at)":\s*")(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)(")', timestamp, value)
    if isinstance(value, list):
        return [norm(v, home, started) for v in value]
    if isinstance(value, dict):
        return {k: norm(v, home, started) for k, v in value.items()}
    return value


def snapshot(home, started):
    tree = []
    def walk(parent):
        for path in sorted(parent.iterdir()):
            name = str(path.relative_to(home))
            if path.name in ('ianchor.db-wal', 'ianchor.db-shm'):
                continue
            mode = path.lstat().st_mode & 0o777
            if path.is_symlink():
                tree.append([name, 'link', os.readlink(path), mode])
            elif path.is_dir():
                tree.append([name, 'dir', mode])
                walk(path)
            elif path.name == 'ianchor.db':
                # Driver-specific page layout isn't a compatibility contract.
                tree.append([name, 'database', mode])
            else:
                tree.append([name, 'file', hashlib.sha256(path.read_bytes()).hexdigest(), mode])
    walk(home)
    dbpath = home / 'Library/Mobile Documents/com~apple~CloudDocs/ianchor/ianchor.db'
    rows, schema = [], None
    if dbpath.exists():
        db = sqlite3.connect(dbpath.resolve().as_uri() + '?mode=ro', uri=True)
        try:
            db.row_factory = sqlite3.Row
            rows = [dict(row) for row in db.execute('SELECT * FROM entries ORDER BY name')]
            schema = db.execute("SELECT sql FROM sqlite_master WHERE name='entries'").fetchone()[0]
            # Validate each timestamp before normalizing. NULL remains NULL.
            for row in rows:
                for field in ('created_at', 'updated_at'):
                    if row[field] is not None:
                        row[field] = json.loads(norm(json.dumps({field: row[field]}), home, started))[field]
                if row['created_at'] is not None and row['updated_at'] is not None:
                    assert row['created_at'] == row['updated_at'] == '<TS>'
        finally:
            db.close()
    return norm(dict(tree=tree, rows=rows, schema=schema), home, started)


def invoke(command, args, home, env, tty_action=None, pipe_input=b'', tty_mode='both'):
    if tty_action is None:
        p = subprocess.run(command + args, input=pipe_input, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                           cwd=home, env=env, timeout=15)
        return dict(code=p.returncode, stdout=p.stdout.decode(), stderr=p.stderr.decode())
    master, slave = pty.openpty()
    attrs = termios.tcgetattr(slave)
    attrs[1] &= ~termios.ONLCR
    termios.tcsetattr(slave, termios.TCSANOW, attrs)
    termios.tcsetwinsize(slave, (24, 120))
    def attach_terminal():
        fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
    controlling = tty_mode == 'in' and tty_action == b'\x03'
    p = subprocess.Popen(command + args,
                         stdin=slave if tty_mode in ('both', 'in') else subprocess.PIPE,
                         stdout=slave if tty_mode in ('both', 'out') else subprocess.PIPE,
                         stderr=subprocess.PIPE, cwd=home, env=env, start_new_session=True,
                         preexec_fn=attach_terminal if controlling else None, pass_fds=(slave,))
    output, error, echo = bytearray(), bytearray(), bytearray()
    selector = selectors.DefaultSelector()
    selector.register(master, selectors.EVENT_READ, output if tty_mode in ('both', 'out') else echo)
    if p.stdout is not None:
        selector.register(p.stdout, selectors.EVENT_READ, output)
    selector.register(p.stderr, selectors.EVENT_READ, error)
    slave_open = True
    restored = None
    sent = False
    suspend_phase = 0
    delayed = None
    deadline = time.monotonic() + 15
    try:
        while selector.get_map():
            if p.poll() is not None and slave_open:
                try:
                    after = termios.tcgetattr(slave)
                    restored = dict(canonical=bool(after[3] & termios.ICANON), echo=bool(after[3] & termios.ECHO))
                except termios.error:
                    # macOS revokes the controlling slave on session exit.
                    # Dedicated non-controlling signal probes verify restoration.
                    assert controlling
                    restored = dict(revoked=True)
                os.close(slave)
                slave_open = False
            assert time.monotonic() < deadline, f'terminal command stalled: {command!r} {args!r} mode={tty_mode} action={tty_action!r} {output!r} {error!r}'
            for key, _ in selector.select(.2):
                try:
                    data = os.read(key.fd, 65536)
                except OSError as exc:
                    if exc.errno != errno.EIO:
                        raise
                    data = b''
                if not data:
                    selector.unregister(key.fileobj)
                else:
                    key.data.extend(data)
            if delayed is not None and time.monotonic() >= delayed[0]:
                if len(delayed) == 3:
                    termios.tcsetwinsize(slave, (24, delayed[2]))
                    p.send_signal(signal.SIGWINCH)
                    delayed = (time.monotonic() + .2, delayed[1])
                else:
                    os.write(master, delayed[1])
                    delayed = None
            if suspend_phase == 1 and termios.tcgetattr(slave)[3] & termios.ICANON:
                p.send_signal(signal.SIGCONT)
                suspend_phase = 2
            elif suspend_phase == 2 and not termios.tcgetattr(slave)[3] & termios.ICANON:
                # Node's promises question remains paused and closes after its
                # SIGCONT redraw. Do not race an extra answer against close.
                suspend_phase = 3
            prompt_seen = b': ' in output or b'[y/N] ' in output
            cursor_ready = (tty_mode == 'in' or env.get('TERM') == 'dumb'
                            or re.search(rb'\x1b\[\d+G$', output) is not None)
            if not sent and prompt_seen and cursor_ready:
                sent = True
                if isinstance(tty_action, dict):
                    os.write(master, tty_action['prefix'])
                    delayed = (time.monotonic() + .8, tty_action['after'])
                    if 'resize' in tty_action:
                        delayed += (tty_action['resize'],)
                elif tty_action == 'SUSPEND':
                    os.write(master, b'\x1a')
                    suspend_phase = 1
                elif tty_action == 'SIGINT':
                    p.send_signal(signal.SIGINT)
                elif tty_action == 'SIGTERM':
                    p.send_signal(signal.SIGTERM)
                elif tty_mode in ('both', 'in'):
                    os.write(master, tty_action)
                else:
                    p.stdin.write(tty_action)
                    p.stdin.close()
        p.wait(timeout=2)
        assert sent, f'prompt not observed: {args!r}'
        if slave_open:
            try:
                after = termios.tcgetattr(slave)
                restored = dict(canonical=bool(after[3] & termios.ICANON), echo=bool(after[3] & termios.ECHO))
            except termios.error:
                assert controlling
                restored = dict(revoked=True)
        return dict(code=p.returncode, stdout=output.decode(), stderr=error.decode(),
                    terminal_echo=echo.decode(), terminal_after=restored)
    finally:
        if p.poll() is None:
            p.kill()
        p.wait()
        selector.close()
        os.close(master)
        if slave_open:
            os.close(slave)
        if p.stdin is not None and not p.stdin.closed:
            p.stdin.close()
        if p.stdout is not None:
            p.stdout.close()
        p.stderr.close()


def signal_window(command, home, env, signum, trials=12):
    for attempt in range(trials):
        master, slave = pty.openpty()
        original = termios.tcgetattr(slave)
        p = subprocess.Popen(command + ['init'], stdin=slave, stdout=slave,
                             stderr=subprocess.PIPE, cwd=home, env=env, start_new_session=True)
        try:
            sent = False
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline and p.poll() is None:
                if not termios.tcgetattr(slave)[3] & termios.ICANON:
                    p.send_signal(signum)
                    sent = True
                    break
            _, error = p.communicate(timeout=3)
            after = termios.tcgetattr(slave)
            flags = termios.ICANON | termios.ECHO
            assert sent and p.returncode == -signum and after[3] & flags == original[3] & flags, (
                command, signum, attempt, p.returncode, original, after, error)
        finally:
            if p.poll() is None:
                p.kill()
            p.wait()
            p.stderr.close()
            os.close(slave)
            os.close(master)
    return trials


def reset_home(home):
    if home.exists():
        shutil.rmtree(home)
    home.mkdir(mode=0o700)
    (home / 'Library/Mobile Documents/com~apple~CloudDocs').mkdir(parents=True)
    (home / '.config').mkdir()
    (home / 'plain').write_text('plain data\n')
    (home / 'other').write_text('other data\n')
    (home / 'directory').mkdir()
    (home / 'directory/child').write_text('nested data\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--legacy-dir', type=Path, required=True)
    parser.add_argument('--go', default='go')
    parser.add_argument('--node', default='node')
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    legacy = args.legacy_dir.resolve()
    paths = subprocess.check_output(['git', 'ls-tree', '-r', '--name-only', BASELINE, '--', 'bin', 'lib', 'package.json', 'package-lock.json'], cwd=REPO).decode().splitlines()
    for name in paths:
        assert (legacy / name).read_bytes() == subprocess.check_output(['git', 'show', BASELINE + ':' + name], cwd=REPO), name
    assert subprocess.check_output([args.go, 'version']).decode().startswith('go version go1.27.0 ')
    with tempfile.TemporaryDirectory(prefix='ianchor-cli-oracle-') as temporary:
        root = Path(temporary)
        binary = root / 'ianchor'
        buildenv = dict(os.environ, CGO_ENABLED='0', GOTOOLCHAIN='local')
        subprocess.run([args.go, 'build', '-o', str(binary), './cmd/ianchor'], cwd=REPO, env=buildenv, check=True)
        home = root / 'home'
        commands = {'node': [args.node, str(legacy / 'bin/ianchor.js')], 'go': [str(binary)]}
        report = dict(baseline=BASELINE, comparisons=[])
        # Capture state after every step, not only the final tree.
        steps = [(['init', '--dir', 'ianchor'], None), (['add', 'plain'], None),
                 (['add', 'directory'], None), (['list'], None), (['list', '--json'], None),
                 (['recover', 'plain'], None), (['recover', 'plain'], None),
                 (['recover', 'plain', '--force'], None), (['relink', 'plain'], None),
                 (['relink', 'plain'], None), (['recover', 'directory'], None),
                 (['relink', 'directory'], None), (['remove', 'plain', '-y'], None),
                 (['remove', 'directory', '--yes'], None), (['list', '--json'], None)]
        for colors in ('0', '1'):
            results = {}
            for kind, command in commands.items():
                reset_home(home)
                started = time.time()
                env = dict(os.environ, HOME=str(home), PWD=str(home.resolve()), TERM='xterm-256color',
                           LANG='C', LC_ALL='C', FORCE_COLOR=colors)
                for key in ('NO_COLOR', 'CLICOLOR_FORCE', 'CLICOLOR'):
                    env.pop(key, None)
                results[kind] = []
                for arguments, action in steps:
                    result = invoke(command, arguments, home, env, action)
                    results[kind].append(dict(args=arguments, result=norm(result, home, started),
                                              state=snapshot(home, started)))
            for node, go in zip(results['node'], results['go']):
                assert node == go, json.dumps(dict(colors=colors, node=node, go=go), ensure_ascii=False, indent=2)
            report['comparisons'].append(dict(colors=colors, workflow_steps=len(steps), status='passed'))
        # Terminal question input, EOF, and signals are measured independently.
        for term in ('dumb', 'xterm-256color'):
            for action in (b'\n', b'\x04', b'\x03', b'abc\n', b'abc\x7fd\n', 'SIGINT', 'SIGTERM'):
                results = {}
                for kind, command in commands.items():
                    reset_home(home)
                    started = time.time()
                    env = dict(os.environ, HOME=str(home), TERM=term, NO_COLOR='1')
                    env.pop('FORCE_COLOR', None)
                    result = invoke(command, ['init'], home, env, action)
                    results[kind] = dict(result=norm(result, home, started), state=snapshot(home, started))
                assert results['node'] == results['go'], repr((term, action, results))
                report['comparisons'].append(dict(term=term, tty_action=str(action), status='passed'))
        for data in (b'', b'abc', b'\x03', b'\x04', b'abc\n', b'abc\r\n', b'abc\r'):
            results = {}
            for kind, command in commands.items():
                reset_home(home)
                started = time.time()
                env = dict(os.environ, HOME=str(home), TERM='dumb', FORCE_COLOR='0')
                result = invoke(command, ['init'], home, env, pipe_input=data)
                results[kind] = dict(result=norm(result, home, started), state=snapshot(home, started))
            assert results['node'] == results['go'], repr((data, results))
            report['comparisons'].append(dict(pipe_input=str(data), status='passed'))
        for mode in ('both', 'in', 'out'):
            for action in (b'abc\n', b'\x04', b'\x03', 'SIGTERM'):
                results = {}
                for kind, command in commands.items():
                    reset_home(home)
                    started = time.time()
                    env = dict(os.environ, HOME=str(home), TERM='xterm-256color', FORCE_COLOR='0')
                    result = invoke(command, ['init'], home, env, action, tty_mode=mode)
                    results[kind] = dict(result=norm(result, home, started), state=snapshot(home, started))
                assert results['node'] == results['go'], repr((mode, action, results))
                report['comparisons'].append(dict(tty_mode=mode, action=str(action), status='passed'))
        for scenario in ('remove', 'conflict'):
            for terminal, data in ((False, b''), (False, b'y'), (False, b'y\r'),
                                   (True, b'\x04'), (True, b'\x03')):
                results = {}
                for kind, command in commands.items():
                    reset_home(home)
                    started = time.time()
                    env = dict(os.environ, HOME=str(home), TERM='xterm-256color', FORCE_COLOR='0')
                    for preparation in (['init', '--dir', 'ianchor'], ['add', 'plain']):
                        assert invoke(command, preparation, home, env)['code'] == 0
                    arguments = ['remove', 'plain'] if scenario == 'remove' else ['add', 'other', '--name', 'plain']
                    result = invoke(command, arguments, home, env,
                                    tty_action=data if terminal else None,
                                    pipe_input=data if not terminal else b'')
                    results[kind] = dict(result=norm(result, home, started), state=snapshot(home, started))
                assert results['node'] == results['go'], repr((scenario, terminal, data, results))
                report['comparisons'].append(dict(prompt=scenario, terminal=terminal, data=str(data), status='passed'))
        color_environment = ('FORCE_COLOR', 'NO_COLOR', 'CI', 'CI_NAME', 'GITHUB_ACTIONS', 'GITEA_ACTIONS',
                             'CIRCLECI', 'TRAVIS', 'APPVEYOR', 'GITLAB_CI', 'BUILDKITE', 'DRONE',
                             'TERM_PROGRAM', 'TERM_PROGRAM_VERSION', 'COLORTERM', 'TEAMCITY_VERSION',
                             'TF_BUILD', 'AGENT_NAME', 'CLICOLOR', 'CLICOLOR_FORCE', 'TERM')
        for terminal, patch in ((True, {'TERM': 'xterm-256color', 'CI': '1'}),
                                (True, {}), (True, {'TERM': 'unknown'}),
                                (True, {'TERM': 'xterm-256color', 'NO_COLOR': '1'}),
                                (False, {'TF_BUILD': '1', 'AGENT_NAME': 'test'}),
                                (False, {'FORCE_COLOR': '00'}), (False, {'FORCE_COLOR': '0junk'})):
            results = {}
            for kind, command in commands.items():
                reset_home(home)
                started = time.time()
                env = {key: value for key, value in os.environ.items() if key not in color_environment}
                env.update(HOME=str(home), **patch)
                arguments = ['init'] if terminal else ['init', '--dir', 'ianchor']
                result = invoke(command, arguments, home, env, b'\n' if terminal else None)
                results[kind] = dict(result=norm(result, home, started), state=snapshot(home, started))
            assert results['node'] == results['go'], repr((terminal, patch, results))
            report['comparisons'].append(dict(auto_color=patch, terminal=terminal, status='passed'))
        editor_cases = {
            'suspend-resume': 'SUSPEND',
            'resize': dict(prefix=b'abcdefghijklmnopqrst', resize=60, after=b'\x01X\n'),
            'escape-enter': dict(prefix=b'abc\x1b', after=b'\n'),
            'escape-cancel': dict(prefix=b'abc\x1b', after=b'\x03'),
            'escape-arrow': dict(prefix=b'abc\x1b', after=b'\x1b[DX\n'),
            'old-control-arrow': b'abc def\x1b[5DX\n',
            'ss3-control-arrow': b'abc def\x1bOdX\n',
            'control-shift-arrow': b'abc def\x1b[1;6DX\n',
            'wide-watch': 'a⌚b\x02\x02X\n'.encode(),
            'wide-melting': 'a🫠b\x02\x02X\n'.encode(),
            'regional-indicator': 'a🇦b\x02\x02X\n'.encode(),
            'delete': b'abc\x01\x1b[3~\n',
            'word-left': b'abc.def\x1bbX\n',
            'word-delete': b'abc.def\x17\n',
            'kill-yank': b'abc\x15\x19\n',
            'tab': b'ab\tcd\n',
            'middle-insert': b'abc\x1b[DX\n',
            'word-right': b'abc.def ghi\x01\x1bfX\n',
            'delete-word-right': b'abc.def ghi\x01\x1bd\n',
            'kill-right-yank': b'abcdef\x02\x02\x0b\x19\n',
            'yank-pop': b'abc\x15def\x15\x19\x1by\n',
            'home-end': b'abc\x1b[HX\x1b[FY\n',
            'control-delete': b'abc.def\x01\x1b[3;5~\n',
            'undo-redo': b'abc\x1f\x1e\n',
            'unicode-cursor': '中文abc\x02X\n'.encode(),
        }
        for name, action in editor_cases.items():
            results = {}
            for kind, command in commands.items():
                reset_home(home)
                started = time.time()
                env = dict(os.environ, HOME=str(home), TERM='xterm-256color', FORCE_COLOR='0')
                result = invoke(command, ['init'], home, env, action)
                results[kind] = dict(result=norm(result, home, started), state=snapshot(home, started))
            assert results['node'] == results['go'], repr((name, results))
            report['comparisons'].append(dict(editor=name, status='passed'))
        for signum in (signal.SIGINT, signal.SIGTERM):
            for kind, command in commands.items():
                reset_home(home)
                env = dict(os.environ, HOME=str(home), TERM='xterm-256color', FORCE_COLOR='0')
                trials = signal_window(command, home, env, signum)
                report['comparisons'].append(dict(signal_window=signum.name, implementation=kind,
                                                  trials=trials, status='passed'))
        for kind, command in commands.items():
            reset_home(home)
            env = dict(os.environ, HOME=str(home), TERM='xterm-256color', FORCE_COLOR='0')
            foreground = foreground_suspend(command, home, env)
            assert b'\x1b[1G\x1b[0J' in foreground['stdout'].encode()  # Continue redraws.
            report['comparisons'].append(dict(foreground_suspend=kind, status='passed',
                                               events=foreground['events']))
        encoded = json.dumps(report, ensure_ascii=False, indent=2) + '\n'
        if args.report:
            args.report.write_text(encoded)
        print(encoded, end='')


if __name__ == '__main__':
    main()
