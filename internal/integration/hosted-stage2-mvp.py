#!/usr/bin/env python3
"""Native PTY proof for the Stage 2 local single-file copy MVP."""

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


def read_until(fd, wanted, timeout=15):
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
    raise RuntimeError("PTY output did not contain %r; tail=%r" % (wanted, bytes(output[-3000:])))


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


def launch(binary, source, destination, environment):
    pid, fd = pty.fork()
    if pid == 0:
        os.execve(binary, [binary, source, destination], environment)
    set_size(fd, 24, 120)
    return pid, fd


def wait_for_path(path, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if os.path.isfile(path):
            return
        time.sleep(0.05)
    raise RuntimeError("copy did not publish expected final path %r" % path)


def run_copy(binary, source, destination, environment, filename, expected_path):
    pid, fd = launch(binary, source, destination, environment)
    try:
        read_until(fd, filename.encode("utf-8"))
        os.write(fd, b"y")
        read_until(fd, b"source captured")
        os.write(fd, b"\t")
        os.write(fd, b"p")
        read_until(fd, b"(queued)")
        wait_for_path(expected_path)
        os.write(fd, b"J")
        read_until(fd, b"completed")
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


def prove_reattach(binary, source, destination, environment):
    pid, fd = launch(binary, source, destination, environment)
    try:
        read_until(fd, b"READ-ONLY")
        os.write(fd, b"J")
        read_until(fd, b"completed")
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


def main():
    if len(sys.argv) not in (2, 4):
        raise SystemExit("usage: hosted-stage2-mvp.py /absolute/path/to/amsftp [ssh-alias remote-root]")
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
    root = tempfile.mkdtemp(prefix="amsftp-stage2-mvp-", dir=persistent_root)
    daemon_group = None
    try:
        home = os.path.join(root, "home")
        source = os.path.join(root, "source")
        destination = os.path.join(root, "destination")
        runtime = os.path.join(root, "runtime")
        for directory in (home, source, destination, runtime):
            os.mkdir(directory, 0o700)
        environment = os.environ.copy()
        environment.update(
            {
                "HOME": home,
                "TERM": "xterm-256color",
                "XDG_CONFIG_HOME": os.path.join(home, ".config"),
                "XDG_STATE_HOME": os.path.join(home, ".local", "state"),
                "XDG_CACHE_HOME": os.path.join(home, ".cache"),
                "XDG_RUNTIME_DIR": runtime,
            }
        )
        if len(sys.argv) == 2:
            payload = b"stage2-local-user-visible-mvp\n"
            with open(os.path.join(source, "payload.txt"), "wb") as handle:
                handle.write(payload)
            daemon_group = run_copy(binary, source, destination, environment, "payload.txt", os.path.join(destination, "payload.txt"))
            with open(os.path.join(destination, "payload.txt"), "rb") as handle:
                copied = handle.read()
            if copied != payload:
                raise RuntimeError("copied payload mismatch: %r" % copied)
            leftovers = [name for name in os.listdir(destination) if ".part-" in name]
            if leftovers:
                raise RuntimeError("completed copy retained part files: %r" % leftovers)
            prove_reattach(binary, source, destination, environment)
            print("Stage 2 local PTY copy and durable Jobs reattach MVP passed")
        else:
            alias = sys.argv[2]
            remote_root = os.path.realpath(sys.argv[3])
            if not remote_root.startswith("/") or not os.path.isdir(remote_root):
                raise RuntimeError("remote root must be an existing absolute directory")
            upload_directory = os.path.join(remote_root, "upload")
            download_directory = os.path.join(remote_root, "download")
            local_download = os.path.join(root, "local-download")
            for directory in (upload_directory, download_directory, local_download):
                os.mkdir(directory, 0o700)
            upload_payload = b"stage2-user-visible-upload\n"
            with open(os.path.join(source, "upload.txt"), "wb") as handle:
                handle.write(upload_payload)
            daemon_group = run_copy(binary, source, alias + ":" + upload_directory, environment, "upload.txt", os.path.join(upload_directory, "upload.txt"))
            with open(os.path.join(upload_directory, "upload.txt"), "rb") as handle:
                uploaded = handle.read()
            if uploaded != upload_payload:
                raise RuntimeError("uploaded payload mismatch: %r" % uploaded)
            download_payload = b"stage2-user-visible-download\n"
            with open(os.path.join(download_directory, "download.txt"), "wb") as handle:
                handle.write(download_payload)
            run_copy(binary, alias + ":" + download_directory, local_download, environment, "download.txt", os.path.join(local_download, "download.txt"))
            with open(os.path.join(local_download, "download.txt"), "rb") as handle:
                downloaded = handle.read()
            if downloaded != download_payload:
                raise RuntimeError("downloaded payload mismatch: %r" % downloaded)
            prove_reattach(binary, source, destination, environment)
            print("Stage 2 temporary-sshd PTY upload, download, and durable Jobs MVP passed")
    finally:
        if daemon_group is not None:
            try:
                os.killpg(daemon_group, signal.SIGTERM)
            except ProcessLookupError:
                pass
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    main()
