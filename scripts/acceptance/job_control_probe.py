"""Opt-in PTY probe with a controlling shell parent and a foreground child job."""
import errno
import fcntl
import json
import os
from pathlib import Path
import pty
import selectors
import signal
import subprocess
import sys
import termios
import time


def foreground_suspend(command, home, env):
    master, slave = pty.openpty()
    termios.tcsetwinsize(slave, (24, 120))

    def attach():
        fcntl.ioctl(slave, termios.TIOCSCTTY, 0)

    parent = subprocess.Popen(
        [sys.executable, str(Path(__file__).with_name('job-control.py')), *command, 'init'],
        stdin=slave, stdout=slave, stderr=subprocess.PIPE, cwd=home, env=env,
        start_new_session=True, preexec_fn=attach)
    selector = selectors.DefaultSelector()
    selector.register(master, selectors.EVENT_READ)
    selector.register(parent.stderr, selectors.EVENT_READ)
    output, errors = bytearray(), bytearray()
    sent = False
    deadline = time.monotonic() + 12
    try:
        while selector.get_map() and time.monotonic() < deadline:
            for key, _ in selector.select(.05):
                fd = key.fileobj if isinstance(key.fileobj, int) else key.fileobj.fileno()
                try:
                    data = os.read(fd, 65536)
                except OSError as exc:
                    if exc.errno != errno.EIO:
                        raise
                    data = b''
                if not data:
                    selector.unregister(key.fileobj)
                elif key.fileobj == master:
                    output.extend(data)
                else:
                    errors.extend(data)
            if not sent and b'\x1b[50G' in output:
                os.write(master, b'abc\x1a')
                sent = True
        assert sent, repr(bytes(output))
        assert parent.wait(timeout=2) == 0, errors.decode(errors='replace')
        events = [json.loads(line) for line in errors.splitlines()]
        assert events[0]['event'] == 'child', events
        events.pop(0)  # PID is private harness state, not a command output.
        assert events == [dict(event='stopped', signal=signal.SIGTSTP, canonical=True, echo=True),
                          dict(event='exit', code=0, canonical=True, echo=True)], events
        assert not (home / '.config' / 'ianchor.json').exists()
        return dict(stdout=output.decode(), events=events)
    finally:
        if parent.poll() is None:
            # Only the helper's explicitly reported child process group is ours.
            for line in errors.splitlines():
                try:
                    event = json.loads(line)
                    if event.get('event') == 'child':
                        os.killpg(event['pid'], signal.SIGKILL)
                except (ValueError, ProcessLookupError):
                    pass
            parent.kill()
            parent.wait()
        selector.close()
        parent.stderr.close()
        os.close(master)
        os.close(slave)
