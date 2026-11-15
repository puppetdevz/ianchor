#!/usr/bin/env python3
"""Private acceptance helper: a shell parent keeps the test job non-orphaned."""
import json
import os
import signal
import sys
import termios
import time


def flags():
    mode = termios.tcgetattr(0)[3]
    return dict(canonical=bool(mode & termios.ICANON), echo=bool(mode & termios.ECHO))


def event(**data):
    print(json.dumps(data), file=sys.stderr, flush=True)


def main():
    signal.signal(signal.SIGTTOU, signal.SIG_IGN)
    gate_read, gate_write = os.pipe()
    child = os.fork()
    if child == 0:
        os.close(gate_write)
        os.setpgid(0, 0)
        signal.signal(signal.SIGTTOU, signal.SIG_DFL)
        os.read(gate_read, 1)
        os.close(gate_read)
        os.execvpe(sys.argv[1], sys.argv[1:], os.environ)
    os.close(gate_read)
    finished = False
    try:
        os.setpgid(child, child)
        os.tcsetpgrp(0, child)
        os.write(gate_write, b'1')
        os.close(gate_write)
        event(event='child', pid=child)
        deadline = time.monotonic() + 10
        stopped = False
        while time.monotonic() < deadline:
            pid, status = os.waitpid(child, os.WUNTRACED | os.WNOHANG)
            if not pid:
                time.sleep(.01)
                continue
            if os.WIFSTOPPED(status):
                assert not stopped and os.WSTOPSIG(status) == signal.SIGTSTP, status
                state = flags()
                assert state == dict(canonical=True, echo=True), state
                event(event='stopped', signal=os.WSTOPSIG(status), **state)
                stopped = True
                os.kill(child, signal.SIGCONT)
            else:
                finished = True
                assert stopped and os.WIFEXITED(status) and os.WEXITSTATUS(status) == 0, status
                state = flags()
                assert state == dict(canonical=True, echo=True), state
                event(event='exit', code=0, **state)
                return
        raise AssertionError('foreground job did not stop and exit before deadline')
    finally:
        if not finished:
            os.kill(child, signal.SIGKILL)
            os.waitpid(child, 0)


if __name__ == '__main__':
    main()
