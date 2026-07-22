#!/usr/bin/env python3
"""Native PTY proof for the Stage 2 local single-file copy MVP."""

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


def selection_ready(observer, output, wanted):
    result = subprocess.run(
        [observer, "-final", "-absent", "Loading", "-columns", "120", "-rows", "24", wanted.decode("utf-8")],
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
    if screen_observed(observer, output, wanted):
        return bytes(output)
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
            return bytes(output)
    raise RuntimeError("PTY output did not contain %r; tail=%r" % (wanted, bytes(output[-3000:])))


def read_ready_selection(fd, observer, output, filename, timeout=15, settle=1):
    wanted = ("▌ " + filename).encode("utf-8")
    deadline = time.monotonic() + timeout
    ready_since = None
    while time.monotonic() < deadline:
        if selection_ready(observer, output, wanted):
            if ready_since is None:
                ready_since = time.monotonic()
            elif time.monotonic() - ready_since >= settle:
                return bytes(output)
        else:
            ready_since = None
        ready, _, _ = select.select([fd], [], [], 0.05)
        if not ready:
            continue
        try:
            chunk = os.read(fd, 65536)
        except OSError:
            break
        if not chunk:
            break
        output.extend(chunk)
    raise RuntimeError("PTY selection did not become ready for %r; tail=%r" % (wanted, bytes(output[-3000:])))


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


def wait_for_absence(path, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if not os.path.lexists(path):
            return
        time.sleep(0.05)
    raise RuntimeError("operation did not remove expected path %r" % path)


def run_copy(binary, observer, source, destination, environment, filename, expected_path):
    pid, fd = launch(binary, source, destination, environment)
    output = bytearray()
    try:
        read_ready_selection(fd, observer, output, filename)
        os.write(fd, b"y")
        read_until(fd, observer, output, b"source captured")
        os.write(fd, b"\t")
        os.write(fd, b"p")
        read_until(fd, observer, output, b"Job queued:")
        wait_for_path(expected_path)
        os.write(fd, b"J")
        read_until(fd, observer, output, b"Completed")
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


def run_move(binary, observer, source, destination, environment, filename):
    pid, fd = launch(binary, source, destination, environment)
    output = bytearray()
    source_path = os.path.join(source, filename)
    destination_path = os.path.join(destination, filename)
    try:
        read_ready_selection(fd, observer, output, filename)
        os.write(fd, b"d")
        read_until(fd, observer, output, b"cut source captured")
        os.write(fd, b"\t")
        os.write(fd, b"p")
        read_until(fd, observer, output, b"Confirm durable move")
        os.write(fd, b"\r")
        read_until(fd, observer, output, b"Job queued:")
        wait_for_path(destination_path)
        wait_for_absence(source_path)
        os.write(fd, b"J")
        read_until(fd, observer, output, b"Completed")
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


def prove_reattach(binary, observer, source, destination, environment):
    pid, fd = launch(binary, source, destination, environment)
    output = bytearray()
    try:
        read_until(fd, observer, output, b"Ready")
        os.write(fd, b"J")
        read_until(fd, observer, output, b"Completed")
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


def run_rename(binary, observer, source, destination, environment, old_name, new_name):
    pid, fd = launch(binary, source, destination, environment)
    output = bytearray()
    try:
        read_ready_selection(fd, observer, output, old_name)
        os.write(fd, b"r")
        read_until(fd, observer, output, b"Rename through durable Job")
        os.write(fd, new_name.encode("utf-8") + b"\r")
        read_until(fd, observer, output, b"Job queued:")
        wait_for_path(os.path.join(source, new_name))
        wait_for_absence(os.path.join(source, old_name))
        os.write(fd, b"J")
        read_until(fd, observer, output, b"Completed")
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


def run_delete(binary, observer, source, destination, environment, filename):
    pid, fd = launch(binary, source, destination, environment)
    output = bytearray()
    try:
        read_ready_selection(fd, observer, output, filename)
        os.write(fd, b"D")
        read_until(fd, observer, output, b"Delete frozen selection")
        os.write(fd, b"\r")
        read_until(fd, observer, output, b"Confirm irreversible deletion")
        os.write(fd, b"\r")
        read_until(fd, observer, output, b"Job queued:")
        wait_for_absence(os.path.join(source, filename))
        os.write(fd, b"J")
        read_until(fd, observer, output, b"Completed")
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
    observer = os.path.realpath(os.environ.get("AMSFTP_STAGE2_VT_OBSERVER", ""))
    if not os.path.isabs(observer) or not os.access(observer, os.X_OK):
        raise SystemExit("AMSFTP_STAGE2_VT_OBSERVER must be an executable absolute path")
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
    environment = None
    try:
        if sys.platform.startswith("linux"):
            installed_binary = os.path.join(root, "amsftp")
            shutil.copy2(binary, installed_binary)
            os.chmod(installed_binary, 0o700)
            binary = installed_binary
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
            run_copy(binary, observer, source, destination, environment, "payload.txt", os.path.join(destination, "payload.txt"))
            with open(os.path.join(destination, "payload.txt"), "rb") as handle:
                copied = handle.read()
            if copied != payload:
                raise RuntimeError("copied payload mismatch: %r" % copied)
            leftovers = [name for name in os.listdir(destination) if ".part-" in name]
            if leftovers:
                raise RuntimeError("completed copy retained part files: %r" % leftovers)
            with open(os.path.join(source, "move.txt"), "wb") as handle:
                handle.write(b"stage2-confirmed-move\n")
            run_move(binary, observer, source, destination, environment, "move.txt")
            run_rename(binary, observer, destination, source, environment, "move.txt", "renamed.txt")
            run_delete(binary, observer, destination, source, environment, "payload.txt")
            prove_reattach(binary, observer, source, destination, environment)
            print("Stage 2 local PTY copy, confirmed move, rename, confirmed delete, and durable Jobs reattach MVP passed")
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
            run_copy(binary, observer, source, alias + ":" + upload_directory, environment, "upload.txt", os.path.join(upload_directory, "upload.txt"))
            with open(os.path.join(upload_directory, "upload.txt"), "rb") as handle:
                uploaded = handle.read()
            if uploaded != upload_payload:
                raise RuntimeError("uploaded payload mismatch: %r" % uploaded)
            download_payload = b"stage2-user-visible-download\n"
            with open(os.path.join(download_directory, "download.txt"), "wb") as handle:
                handle.write(download_payload)
            run_copy(binary, observer, alias + ":" + download_directory, local_download, environment, "download.txt", os.path.join(local_download, "download.txt"))
            with open(os.path.join(local_download, "download.txt"), "rb") as handle:
                downloaded = handle.read()
            if downloaded != download_payload:
                raise RuntimeError("downloaded payload mismatch: %r" % downloaded)
            prove_reattach(binary, observer, source, destination, environment)
            print("Stage 2 temporary-sshd PTY upload, download, and durable Jobs MVP passed")
    finally:
        if environment is not None:
            subprocess.run(
                [binary, "daemon", "stop", "--format", "json"],
                env=environment,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                check=False,
                timeout=10,
            )
        shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    main()
