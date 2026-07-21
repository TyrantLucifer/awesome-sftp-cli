#!/usr/bin/env python3
"""Native Stage 3 shell/PTY handoff smoke for local and temporary-sshd panes."""

import fcntl
import os
import pty
import select
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import time
import termios


def set_size(fd, rows, columns):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))


def screen_observed(observer, output, wanted):
    result = subprocess.run(
        [observer, "-checkpoint", "0", "-columns", "120", "-rows", "24", wanted.decode("utf-8")],
        input=output,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode == 0:
        return True
    if result.returncode == 1:
        return False
    raise RuntimeError("VT observer failed with exit %d: %s" % (result.returncode, result.stderr.decode(errors="replace")))


def read_until(fd, observer, output, wanted, timeout=15):
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
        if screen_observed(observer, output, wanted):
            return
    raise RuntimeError("PTY screen did not contain %r; tail=%r" % (wanted, bytes(output[-3000:])))


def wait_for_shell_prompt(fd, timeout=15):
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
        if bytes(output).rstrip().endswith((b"$", b"#")):
            return
    raise RuntimeError("foreground shell prompt was not observed; tail=%r" % bytes(output[-3000:]))


def wait_for_marker(path, expected, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with open(path, "r", encoding="utf-8") as handle:
                value = handle.read().strip()
        except FileNotFoundError:
            time.sleep(0.05)
            continue
        if os.path.realpath(value) != os.path.realpath(expected):
            raise RuntimeError("shell cwd marker = %r, want %r" % (value, expected))
        return
    raise RuntimeError("shell did not create cwd marker %r" % path)


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
    os.waitpid(pid, 0)
    raise RuntimeError("PTY child %d did not exit" % pid)


def exercise_shell(binary, observer, endpoint, cwd, marker, command_marker, environment):
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(binary, [binary, endpoint, endpoint], environment)
    output = bytearray()
    try:
        set_size(fd, 24, 120)
        read_until(fd, observer, output, b"Ready")

        # Prove the user-visible one-shot command surface through the real
        # process cwd (local) or a fresh OpenSSH transport (remote). The second
        # Enter is sent only after the bounded confirmation UI is visible.
        os.write(fd, b"!")
        read_until(fd, observer, output, b"One-time command")
        os.write(fd, b"pwd > .amsftp-stage3-command-cwd")
        os.write(fd, b"\r")
        read_until(fd, observer, output, b"Confirm one-time command")
        os.write(fd, b"\r")
        wait_for_marker(command_marker, cwd)
        output = bytearray()
        read_until(fd, observer, output, b"command exit 0")

        os.write(fd, b"g")
        time.sleep(0.1)
        os.write(fd, b"s")
        # Do not send proof input until the foreground child owns the PTY;
        # otherwise tcell could consume it before the handoff completes.
        wait_for_shell_prompt(fd)
        os.write(fd, b"pwd > .amsftp-stage3-shell-cwd\nexit\n")
        wait_for_marker(marker, cwd)
        output = bytearray()
        read_until(fd, observer, output, b"shell exited; pane refreshed")
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
    if len(sys.argv) not in (4, 6):
        raise SystemExit("usage: hosted-stage3-shell.py amsftp vt-observer local-root [ssh-alias remote-root]")
    binary = os.path.realpath(sys.argv[1])
    observer = os.path.realpath(sys.argv[2])
    local_root = os.path.realpath(sys.argv[3])
    for label, path in (("amsftp", binary), ("VT observer", observer)):
        if not os.path.isabs(path) or not os.access(path, os.X_OK):
            raise SystemExit("%s must be an executable absolute path" % label)
    if not os.path.isdir(local_root):
        raise SystemExit("local root must be an existing directory")

    persistent_root = os.environ.get("AMSFTP_TEST_PERSISTENT_ROOT")
    if persistent_root is None and sys.platform.startswith("linux"):
        candidate = "/var/lib/amsftp-tests/%d" % os.geteuid()
        if os.path.isdir(candidate):
            persistent_root = candidate
    root = tempfile.mkdtemp(prefix="amsftp-stage3-shell-", dir=persistent_root)
    daemon_group = None
    try:
        if sys.platform.startswith("linux"):
            installed_binary = os.path.join(root, "amsftp")
            shutil.copy2(binary, installed_binary)
            os.chmod(installed_binary, 0o700)
            binary = installed_binary
        home = os.path.join(root, "home")
        runtime = os.path.join(root, "runtime")
        for directory in (home, runtime):
            os.mkdir(directory, 0o700)
        environment = os.environ.copy()
        environment.update(
            {
                "HOME": home,
                "SHELL": "/bin/sh",
                "TERM": "xterm-256color",
                "XDG_CONFIG_HOME": os.path.join(home, ".config"),
                "XDG_STATE_HOME": os.path.join(home, ".local", "state"),
                "XDG_CACHE_HOME": os.path.join(home, ".cache"),
                "XDG_RUNTIME_DIR": runtime,
            }
        )
        endpoint = local_root
        cwd = local_root
        marker = os.path.join(local_root, ".amsftp-stage3-shell-cwd")
        command_marker = os.path.join(local_root, ".amsftp-stage3-command-cwd")
        if len(sys.argv) == 6:
            alias = sys.argv[4]
            cwd = os.path.realpath(sys.argv[5])
            endpoint = alias + ":" + cwd
            marker = os.path.join(cwd, ".amsftp-stage3-shell-cwd")
            command_marker = os.path.join(cwd, ".amsftp-stage3-command-cwd")
        daemon_group = exercise_shell(binary, observer, endpoint, cwd, marker, command_marker, environment)
        os.remove(marker)
        os.remove(command_marker)
        mode = "remote OpenSSH" if len(sys.argv) == 6 else "local /bin/sh"
        print("Stage 3 %s one-shot command plus foreground PTY handoff and exact cwd smoke passed" % mode)
    finally:
        if daemon_group is not None:
            try:
                os.killpg(daemon_group, signal.SIGTERM)
            except ProcessLookupError:
                pass
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    main()
