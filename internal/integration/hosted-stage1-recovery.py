#!/usr/bin/env python3
import fcntl
import os
import pty
import select
import shutil
import signal
import socket
import struct
import subprocess
import sys
import termios
import time


def terminal_screen_observed(observer, output, patterns, observe_after=0, columns=200, rows=30):
    command = [
        observer,
        "-checkpoint", str(observe_after),
        "-columns", str(columns),
        "-rows", str(rows),
        *(pattern.decode("utf-8") for pattern in patterns),
    ]
    result = subprocess.run(
        command,
        input=output,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if result.returncode == 0:
        return True
    if result.returncode == 1:
        return False
    raise RuntimeError(f"VT observer failed with exit {result.returncode}: {result.stderr.decode(errors='replace')}")


def terminate_if_running(pid):
    try:
        os.kill(pid, signal.SIGTERM)
    except ProcessLookupError:
        pass


def run_self_test(observer):
    prelude = (
        b"\x1b[?2026h\x1b[30;13Hloading | caps:1@1 | sort:name\x1b[?2026l"
    )
    checkpoint = len(prelude)
    split_status = prelude + (
        b"\x1b[?2026h"
        b"\x1b[30;13H\x1b[7mrecon"
        b"\x1b[30;19Hected at nearest accessible"
        b"\x1b[30;47Hparent"
        b"\x1b[?2026l"
        b"\x1b[?2026h"
        b"\x1b[30;13Hold endpoint cleanup failed"
        b"\x1b[3;3Ha-recovered-marker.txt"
        b"\x1b[?2026l"
    )
    expected = (b"reconnected at nearest accessible parent", b"a-recovered-marker.txt")
    if not terminal_screen_observed(observer, bytearray(split_status), expected, observe_after=checkpoint):
        raise RuntimeError("transient split terminal status was not reconstructed")
    terminate_if_running(2_147_483_647)


if len(sys.argv) == 3 and sys.argv[1] == "--self-test":
    run_self_test(sys.argv[2])
    sys.exit(0)


BINARY = os.environ["AMSFTP_RECOVERY_INSTALLED"]
CLIENT = os.environ["AMSFTP_RECOVERY_CLIENT"]
CLIENT_UID = int(os.environ["AMSFTP_RECOVERY_UID"])
CLIENT_GID = int(os.environ["AMSFTP_RECOVERY_GID"])
LOCAL_ROOT = os.environ["AMSFTP_RECOVERY_LOCAL_ROOT"]
A_ROOT = os.environ["AMSFTP_RECOVERY_A_ROOT"]
A_DEEP = os.environ["AMSFTP_RECOVERY_A_DEEP"]
B_ROOT = os.environ["AMSFTP_RECOVERY_B_ROOT"]
SSHD_A_CONFIG = os.environ["AMSFTP_RECOVERY_SSHD_A_CONFIG"]
SSHD_A_LOG = os.environ["AMSFTP_RECOVERY_SSHD_A_LOG"]
SSHD_A_PORT = int(os.environ["AMSFTP_RECOVERY_SSHD_A_PORT"])
STATE_HOME = os.environ["AMSFTP_RECOVERY_STATE_HOME"]
VT_OBSERVER = os.environ["AMSFTP_RECOVERY_VT_OBSERVER"]


def wait_until(predicate, description, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError(f"timed out waiting for {description}")


def process_command(pid):
    try:
        with open(f"/proc/{pid}/cmdline", "rb") as handle:
            return [part.decode() for part in handle.read().split(b"\0") if part]
    except (FileNotFoundError, ProcessLookupError, UnicodeDecodeError):
        return []


def process_uid(pid):
    try:
        with open(f"/proc/{pid}/status", encoding="utf-8") as handle:
            for line in handle:
                if line.startswith("Uid:"):
                    return int(line.split()[1])
    except (FileNotFoundError, ProcessLookupError, ValueError):
        pass
    return None


def process_parent(pid):
    try:
        with open(f"/proc/{pid}/status", encoding="utf-8") as handle:
            for line in handle:
                if line.startswith("PPid:"):
                    return int(line.split()[1])
    except (FileNotFoundError, ProcessLookupError, ValueError):
        pass
    return None


def process_tree(root_pid):
    result = {root_pid}
    while True:
        before = len(result)
        for name in os.listdir("/proc"):
            if not name.isdigit():
                continue
            pid = int(name)
            if process_parent(pid) in result:
                result.add(pid)
        if len(result) == before:
            return result


def daemon_pids():
    result = []
    for name in os.listdir("/proc"):
        if not name.isdigit():
            continue
        pid = int(name)
        if process_uid(pid) == CLIENT_UID and process_command(pid) == [BINARY, "daemon"]:
            result.append(pid)
    return sorted(result)


def single_daemon(exclude=None):
    processes = daemon_pids()
    if len(processes) != 1 or processes[0] == exclude:
        return None
    return processes[0]


def set_size(fd, columns, rows):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, columns, 0, 0))


class PtyApp:
    def __init__(self, arguments):
        self.master, slave = pty.openpty()
        set_size(slave, 200, 30)

        def child_setup():
            os.setsid()
            fcntl.ioctl(slave, termios.TIOCSCTTY, 0)
            os.initgroups(CLIENT, CLIENT_GID)
            os.setgid(CLIENT_GID)
            os.setuid(CLIENT_UID)

        self.process = subprocess.Popen(
            [BINARY, *arguments],
            stdin=slave,
            stdout=slave,
            stderr=slave,
            env={key: value for key, value in os.environ.items() if key not in {
                "AMSFTP_RECOVERY_INSTALLED",
                "AMSFTP_RECOVERY_CLIENT",
                "AMSFTP_RECOVERY_UID",
                "AMSFTP_RECOVERY_GID",
                "AMSFTP_RECOVERY_LOCAL_ROOT",
                "AMSFTP_RECOVERY_A_ROOT",
                "AMSFTP_RECOVERY_A_DEEP",
                "AMSFTP_RECOVERY_B_ROOT",
                "AMSFTP_RECOVERY_SSHD_A_CONFIG",
                "AMSFTP_RECOVERY_SSHD_A_LOG",
                "AMSFTP_RECOVERY_SSHD_A_PID",
                "AMSFTP_RECOVERY_SSHD_A_PORT",
                "AMSFTP_RECOVERY_STATE_HOME",
            }},
            preexec_fn=child_setup,
            close_fds=True,
        )
        os.close(slave)
        os.set_blocking(self.master, False)
        self.output = bytearray()

    def checkpoint(self):
        self._drain()
        return len(self.output)

    def _drain(self):
        while True:
            ready, _, _ = select.select([self.master], [], [], 0)
            if not ready:
                return
            try:
                chunk = os.read(self.master, 65536)
            except (BlockingIOError, OSError):
                return
            if not chunk:
                return
            self.output.extend(chunk)

    def wait_for(self, *patterns, start=0, timeout=30):
        encoded = [pattern.encode() for pattern in patterns]
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            self._drain()
            visible = self.output[start:]
            if all(pattern in visible for pattern in encoded):
                return
            if self.process.poll() is not None:
                raise RuntimeError(f"amsftp exited {self.process.returncode} before {patterns}")
            time.sleep(0.05)
        raise RuntimeError(f"timed out waiting for {patterns}")

    def wait_for_screen(self, *patterns, start=0, timeout=30):
        encoded = [pattern.encode() for pattern in patterns]
        deadline = time.monotonic() + timeout
        checked_bytes = -1
        while time.monotonic() < deadline:
            self._drain()
            if checked_bytes != len(self.output):
                checked_bytes = len(self.output)
                if terminal_screen_observed(VT_OBSERVER, self.output, encoded, observe_after=start):
                    return
            if self.process.poll() is not None:
                raise RuntimeError(f"amsftp exited {self.process.returncode} before screen showed {patterns}")
            time.sleep(0.05)
        raise RuntimeError(f"timed out waiting for screen to show {patterns}")

    def send(self, value):
        os.write(self.master, value.encode())

    def resize(self, columns):
        set_size(self.master, columns, 30)
        os.killpg(self.process.pid, signal.SIGWINCH)

    def close(self):
        if self.process.poll() is None:
            self.send("q")
        try:
            self.process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(self.process.pid, signal.SIGKILL)
            self.process.wait(timeout=5)
            raise RuntimeError("amsftp did not exit after q")
        self._drain()
        os.close(self.master)
        if self.process.returncode != 0:
            raise RuntimeError(f"amsftp exited {self.process.returncode}")

    def abort(self):
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGKILL)
            self.process.wait(timeout=5)
        try:
            os.close(self.master)
        except OSError:
            pass


def run_and_close(arguments, markers):
    app = PtyApp(arguments)
    try:
        app.wait_for(*markers)
        app.close()
    except Exception:
        app._drain()
        printable = bytes(byte for byte in app.output[-12000:] if byte in b"\n\r\t" or 32 <= byte < 127)
        sys.stderr.write(printable.decode(errors="replace")[-4000:] + "\n")
        app.abort()
        raise


def stop_process_group(pid):
    # OpenSSH accepted-session children may create their own process groups.
    # Snapshot and terminate the full descendant tree, rescanning while the
    # listener is still alive so no accepted SFTP session survives the restart.
    tracked = set()
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        tracked.update(process_tree(pid))
        live = []
        for process_pid in sorted(tracked, reverse=True):
            try:
                os.kill(process_pid, 0)
                live.append(process_pid)
            except ProcessLookupError:
                continue
        if not live:
            return
        for process_pid in live:
            try:
                os.kill(process_pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
        time.sleep(0.05)
    for process_pid in sorted(tracked, reverse=True):
        try:
            os.kill(process_pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    wait_until(
        lambda: all(not os.path.exists(f"/proc/{process_pid}") for process_pid in tracked),
        f"process tree {pid} stop",
        timeout=5,
    )


def start_sshd_a():
    log = open(SSHD_A_LOG, "ab", buffering=0)
    process = subprocess.Popen(
        ["/usr/bin/setsid", "/usr/sbin/sshd", "-D", "-e", "-f", SSHD_A_CONFIG],
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=subprocess.STDOUT,
        close_fds=True,
    )

    def accepting():
        try:
            with socket.create_connection(("127.0.0.1", SSHD_A_PORT), timeout=0.2):
                return True
        except OSError:
            if process.poll() is not None:
                raise RuntimeError(f"restarted sshd A exited {process.returncode}")
            return False

    wait_until(accepting, "restarted sshd A")
    return process.pid, log


def assert_private_runtime():
    runtime_socket = os.path.join(os.environ["XDG_RUNTIME_DIR"], "amsftp", "control-v1.sock")
    mode = os.stat(runtime_socket).st_mode & 0o777
    if mode != 0o600:
        raise RuntimeError(f"control socket mode is {mode:o}, want 600")
    log_file = os.path.join(STATE_HOME, "amsftp", "log", "daemon.jsonl")
    mode = os.stat(log_file).st_mode & 0o777
    if mode != 0o600:
        raise RuntimeError(f"daemon log mode is {mode:o}, want 600")
    with open(log_file, "rb") as handle:
        content = handle.read()
    if b'"request_id"' not in content or b'"event"' not in content:
        raise RuntimeError("daemon log lacks request correlation records")


def main():
    run_and_close([LOCAL_ROOT, f"recovery-a:{A_ROOT}"], ["local-stage1-marker.txt", "a-parent-marker.txt"])

    workspace = PtyApp([f"recovery-a:{A_ROOT}", f"recovery-b:{B_ROOT}"])
    try:
        workspace.wait_for("a-parent-marker.txt", "b-stage1-marker.txt")
        workspace.send("Srecovery-stage1\r")
        workspace.wait_for("workspace saved: recovery-stage1")
        workspace.close()
    except Exception:
        workspace.abort()
        raise

    run_and_close(["--workspace", "recovery-stage1"], ["a-parent-marker.txt", "b-stage1-marker.txt"])

    app = PtyApp([f"recovery-a:{A_DEEP}", f"recovery-b:{B_ROOT}"])
    restarted_sshd_log = None
    try:
        app.wait_for("a-deep-marker.txt", "b-stage1-marker.txt")
        original_sshd_pid = int(os.environ["AMSFTP_RECOVERY_SSHD_A_PID"])
        stop_process_group(original_sshd_pid)
        shutil.rmtree(os.path.join(A_ROOT, "gone"))
        with open(os.path.join(A_ROOT, "a-recovered-marker.txt"), "w", encoding="utf-8") as handle:
            handle.write("a-recovered\n")
        with open(os.path.join(B_ROOT, "b-unaffected-marker.txt"), "w", encoding="utf-8") as handle:
            handle.write("b-unaffected\n")
        restarted_sshd_pid, restarted_sshd_log = start_sshd_a()
        os.environ["AMSFTP_RECOVERY_SSHD_A_PID"] = str(restarted_sshd_pid)
        checkpoint = app.checkpoint()
        app.send("R")
        app.wait_for_screen("reconnected at nearest accessible parent", "a-recovered-marker.txt", start=checkpoint, timeout=35)
        checkpoint = app.checkpoint()
        app.send("\tR")
        app.wait_for("b-unaffected-marker.txt", start=checkpoint)
        checkpoint = app.checkpoint()
        app.resize(180)
        app.wait_for("a-recovered-marker.txt", "b-unaffected-marker.txt", start=checkpoint)
        app.resize(200)

        old_daemon = wait_until(single_daemon, "single daemon PID")
        os.kill(old_daemon, signal.SIGTERM)
        wait_until(lambda: old_daemon not in daemon_pids(), "old daemon exit")
        with open(os.path.join(A_ROOT, "a-daemon-marker.txt"), "w", encoding="utf-8") as handle:
            handle.write("a-daemon\n")
        with open(os.path.join(B_ROOT, "b-daemon-marker.txt"), "w", encoding="utf-8") as handle:
            handle.write("b-daemon\n")
        new_daemon = wait_until(lambda: single_daemon(old_daemon), "replacement daemon", timeout=30)
        if new_daemon == old_daemon:
            raise RuntimeError("daemon PID did not change")
        app.wait_for_screen("a-daemon-marker.txt", "b-daemon-marker.txt", timeout=30)
        assert_private_runtime()
        app.close()

        terminate_if_running(new_daemon)
        wait_until(lambda: not daemon_pids(), "final daemon exit")
    except Exception:
        app._drain()
        printable = bytes(byte for byte in app.output[-12000:] if byte in b"\n\r\t" or 32 <= byte < 127)
        sys.stderr.write(printable.decode(errors="replace")[-4000:] + "\n")
        app.abort()
        raise
    finally:
        if restarted_sshd_log is not None:
            restarted_sshd_log.close()

    print("Stage 1 PTY recovery scenarios passed")


if __name__ == "__main__":
    main()
