#!/usr/bin/env python3
"""Native PTY smoke for resize, exit/re-entry, and signal cleanup."""

import fcntl
import os
import pty
import select
import shutil
import signal
import struct
import sys
import tempfile
import time
import termios


def set_size(fd, rows, columns):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))


def read_until(fd, wanted, timeout=10):
    output = bytearray()
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.1)
        if not ready:
            continue
        try:
            chunk = os.read(fd, 65536)
        except OSError:
            break
        if not chunk:
            break
        output.extend(chunk)
        if wanted in output:
            return bytes(output)
    raise RuntimeError("PTY output did not contain %r; tail=%r" % (wanted, bytes(output[-2000:])))


def wait_child(pid, fd, timeout=10):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.05)
        if ready:
            try:
                os.read(fd, 65536)
            except OSError:
                pass
        waited, status = os.waitpid(pid, os.WNOHANG)
        if waited == pid:
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise RuntimeError("PTY child %d exited with status %d" % (pid, status))
            return
        time.sleep(0.01)
    os.kill(pid, signal.SIGKILL)
    kill_deadline = time.monotonic() + 5
    while time.monotonic() < kill_deadline:
        ready, _, _ = select.select([fd], [], [], 0.05)
        if ready:
            try:
                os.read(fd, 65536)
            except OSError:
                pass
        waited, _ = os.waitpid(pid, os.WNOHANG)
        if waited == pid:
            break
        time.sleep(0.05)
    raise RuntimeError("PTY child %d did not exit" % pid)


def run_client(binary, browse_path, environment, terminate=False):
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(binary, [binary, browse_path, browse_path], environment)
    try:
        set_size(fd, 3, 12)
        read_until(fd, b"resize")
        set_size(fd, 24, 80)
        os.kill(pid, signal.SIGWINCH)
        read_until(fd, b"READ-ONLY")
        if terminate:
            os.kill(pid, signal.SIGTERM)
        else:
            os.write(fd, b"q")
        wait_child(pid, fd)
    except Exception:
        try:
            os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        raise
    finally:
        os.close(fd)
    return pid


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: hosted-pty.py /absolute/path/to/amsftp")
    binary = os.path.realpath(sys.argv[1])
    if not os.path.isabs(binary) or not os.access(binary, os.X_OK):
        raise SystemExit("amsftp binary must be an executable absolute path")
    persistent_root = os.environ.get("AMSFTP_TEST_PERSISTENT_ROOT")
    if persistent_root is None and sys.platform.startswith("linux"):
        candidate = "/var/lib/amsftp-tests/%d" % os.geteuid()
        if os.path.isdir(candidate):
            persistent_root = candidate
    if persistent_root is not None:
        persistent_root = os.path.realpath(persistent_root)
        if not os.path.isabs(persistent_root) or not os.path.isdir(persistent_root):
            raise SystemExit("AMSFTP_TEST_PERSISTENT_ROOT must be an existing absolute directory")
    root = tempfile.mkdtemp(prefix="amsftp-pty-", dir=persistent_root)
    daemon_group = None
    try:
        home = os.path.join(root, "home")
        browse = os.path.join(root, "browse")
        for directory in (home, browse):
            os.mkdir(directory, 0o700)
        environment = os.environ.copy()
        environment.update(
            {
                "HOME": home,
                "TERM": "xterm-256color",
                "XDG_CONFIG_HOME": os.path.join(home, ".config"),
                "XDG_STATE_HOME": os.path.join(home, ".local", "state"),
                "XDG_CACHE_HOME": os.path.join(home, ".cache"),
                "XDG_RUNTIME_DIR": os.path.join(root, "runtime"),
            }
        )
        os.mkdir(environment["XDG_RUNTIME_DIR"], 0o700)
        daemon_group = run_client(binary, browse, environment)
        run_client(binary, browse, environment)
        run_client(binary, browse, environment, terminate=True)
        print("PTY resize, exit/re-entry, and SIGTERM smoke passed")
    finally:
        if daemon_group is not None:
            try:
                os.killpg(daemon_group, signal.SIGTERM)
            except ProcessLookupError:
                pass
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    main()
