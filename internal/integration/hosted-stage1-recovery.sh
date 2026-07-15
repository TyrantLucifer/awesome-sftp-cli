#!/usr/bin/env bash
set -euo pipefail

if test "$(id -u)" -ne 0; then
  printf 'hosted-stage1-recovery.sh must run as root\n' >&2
  exit 1
fi

: "${AMSFTP_RECOVERY_BINARY:?AMSFTP_RECOVERY_BINARY is required}"

root="${AMSFTP_RECOVERY_ROOT:-/tmp/amsftp-recovery-default}"
case "${root}" in
  /tmp/amsftp-recovery-* | /tmp/amsftp-auth-*-recovery) ;;
  *)
    printf 'AMSFTP_RECOVERY_ROOT must be a dedicated /tmp recovery path\n' >&2
    exit 1
    ;;
esac

installed=/usr/bin/amsftp-recovery-test
client_user=amsftp-recovery-client
target_a=amsftp-recovery-a
target_b=amsftp-recovery-b
state_home=/var/lib/amsftp-recovery-state
sshd_a_pid=
sshd_b_pid=

stop_group() {
  local pid="${1:-}"
  if test -n "${pid}" && kill -0 "${pid}" 2>/dev/null; then
    kill -TERM -- "-${pid}" 2>/dev/null || true
    for _ in $(seq 1 50); do
      if ! kill -0 "${pid}" 2>/dev/null; then
        return 0
      fi
      sleep 0.1
    done
    kill -KILL -- "-${pid}" 2>/dev/null || true
  fi
}

cleanup() {
  for pid_file in "${root}/sshd-a/sshd.pid" "${root}/sshd-b/sshd.pid"; do
    if test -f "${pid_file}"; then
      stop_group "$(cat "${pid_file}")"
    fi
  done
  stop_group "${sshd_a_pid}"
  stop_group "${sshd_b_pid}"
  pkill -KILL -u "${client_user}" -f "^${installed}" 2>/dev/null || true
  rm -f "${installed}"
  rm -rf "${state_home}" "${root}"
  for user_name in "${client_user}" "${target_a}" "${target_b}"; do
    if id "${user_name}" >/dev/null 2>&1; then
      userdel -r "${user_name}" >/dev/null 2>&1 || true
    fi
  done
}
trap cleanup EXIT

cleanup
trap cleanup EXIT

for user_name in "${client_user}" "${target_a}" "${target_b}"; do
  useradd --create-home --shell /bin/bash "${user_name}"
done

# OpenSSH rejects locked local accounts before evaluating authorized_keys on
# the hosted Ubuntu runners. Give only the two SFTP targets random, test-local
# passwords while every recovery sshd keeps password authentication disabled.
recovery_account_password="$(openssl rand -hex 24)"
printf '%s:%s\n%s:%s\n' \
  "${target_a}" "${recovery_account_password}" \
  "${target_b}" "${recovery_account_password}" | chpasswd
unset recovery_account_password

install -d -o root -g root -m 0755 "${root}"
install -o root -g root -m 0755 "${AMSFTP_RECOVERY_BINARY}" "${installed}"

client_home="$(getent passwd "${client_user}" | cut -d: -f6)"
target_a_home="$(getent passwd "${target_a}" | cut -d: -f6)"
target_b_home="$(getent passwd "${target_b}" | cut -d: -f6)"
client_uid="$(id -u "${client_user}")"
client_gid="$(id -g "${client_user}")"
runtime_home="/run/user/${client_uid}"

install -d -o "${client_user}" -g "${client_user}" -m 0700 \
  "${client_home}/.ssh" \
  "${client_home}/.config" \
  "${client_home}/.cache" \
  "${state_home}" \
  "${runtime_home}"
runuser -u "${client_user}" -- /usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${client_home}/.ssh/recovery_key"
chmod 0600 "${client_home}/.ssh/recovery_key"

for target in "${target_a}" "${target_b}"; do
  target_home="$(getent passwd "${target}" | cut -d: -f6)"
  install -d -o "${target}" -g "${target}" -m 0700 "${target_home}/.ssh"
  install -o "${target}" -g "${target}" -m 0600 "${client_home}/.ssh/recovery_key.pub" "${target_home}/.ssh/authorized_keys"
done

a_root="${target_a_home}/recovery-root"
a_deep="${a_root}/gone/deep"
b_root="${target_b_home}/recovery-root"
local_root="${client_home}/local-root"
install -d -o "${target_a}" -g "${target_a}" -m 0700 "${a_deep}"
install -d -o "${target_b}" -g "${target_b}" -m 0700 "${b_root}"
install -d -o "${client_user}" -g "${client_user}" -m 0700 "${local_root}"
printf 'local-ready\n' >"${local_root}/local-stage1-marker.txt"
printf 'a-parent-ready\n' >"${a_root}/a-parent-marker.txt"
printf 'a-deep-ready\n' >"${a_deep}/a-deep-marker.txt"
printf 'b-ready\n' >"${b_root}/b-stage1-marker.txt"
chown -R "${client_user}:${client_user}" "${local_root}"
chown -R "${target_a}:${target_a}" "${a_root}"
chown -R "${target_b}:${target_b}" "${b_root}"

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

port_a="$(free_port)"
port_b="${port_a}"
while test "${port_b}" = "${port_a}"; do
  port_b="$(free_port)"
done

configure_sshd() {
  local name="$1" port="$2" user_name="$3" directory
  directory="${root}/sshd-${name}"
  install -d -o root -g root -m 0700 "${directory}"
  /usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${directory}/host_key"
  cat >"${directory}/sshd_config" <<EOF
Port ${port}
ListenAddress 127.0.0.1
HostKey ${directory}/host_key
PidFile ${directory}/sshd.pid
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication yes
UsePAM no
StrictModes yes
PermitRootLogin no
AllowAgentForwarding no
AllowTcpForwarding no
X11Forwarding no
Subsystem sftp internal-sftp
AllowUsers ${user_name}
EOF
}

start_sshd() {
  local name="$1" port="$2" directory pid deadline
  directory="${root}/sshd-${name}"
  setsid /usr/sbin/sshd -D -e -f "${directory}/sshd_config" >>"${directory}/sshd.log" 2>&1 &
  pid=$!
  deadline=$((SECONDS + 10))
  until /usr/bin/nc -z 127.0.0.1 "${port}"; do
    if test "${SECONDS}" -ge "${deadline}"; then
      sed -n '1,160p' "${directory}/sshd.log" >&2
      printf 'recovery sshd %s did not become ready\n' "${name}" >&2
      return 1
    fi
    sleep 0.1
  done
  printf '%s\n' "${pid}"
}

mkdir -p /run/sshd
configure_sshd a "${port_a}" "${target_a}"
configure_sshd b "${port_b}" "${target_b}"
sshd_a_pid="$(start_sshd a "${port_a}")"
sshd_b_pid="$(start_sshd b "${port_b}")"

known_hosts="${client_home}/.ssh/known_hosts"
/usr/bin/ssh-keyscan -p "${port_a}" 127.0.0.1 >"${known_hosts}" 2>/dev/null
/usr/bin/ssh-keyscan -p "${port_b}" 127.0.0.1 >>"${known_hosts}" 2>/dev/null
chown "${client_user}:${client_user}" "${known_hosts}"
chmod 0600 "${known_hosts}"

cat >"${client_home}/.ssh/config" <<EOF
Host recovery-a
  HostName 127.0.0.1
  Port ${port_a}
  User ${target_a}
  IdentityFile ${client_home}/.ssh/recovery_key
  IdentitiesOnly yes
  PreferredAuthentications publickey
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${known_hosts}
  StrictHostKeyChecking yes
  LogLevel ERROR
  ConnectTimeout 5

Host recovery-b
  HostName 127.0.0.1
  Port ${port_b}
  User ${target_b}
  IdentityFile ${client_home}/.ssh/recovery_key
  IdentitiesOnly yes
  PreferredAuthentications publickey
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${known_hosts}
  StrictHostKeyChecking yes
  LogLevel ERROR
  ConnectTimeout 5
EOF
chown "${client_user}:${client_user}" "${client_home}/.ssh/config"
chmod 0600 "${client_home}/.ssh/config"

env -i \
  PATH=/usr/local/bin:/usr/bin:/bin \
  TERM=xterm-256color \
  HOME="${client_home}" \
  XDG_CONFIG_HOME="${client_home}/.config" \
  XDG_CACHE_HOME="${client_home}/.cache" \
  XDG_STATE_HOME="${state_home}" \
  XDG_RUNTIME_DIR="${runtime_home}" \
  AMSFTP_RECOVERY_INSTALLED="${installed}" \
  AMSFTP_RECOVERY_CLIENT="${client_user}" \
  AMSFTP_RECOVERY_UID="${client_uid}" \
  AMSFTP_RECOVERY_GID="${client_gid}" \
  AMSFTP_RECOVERY_LOCAL_ROOT="${local_root}" \
  AMSFTP_RECOVERY_A_ROOT="${a_root}" \
  AMSFTP_RECOVERY_A_DEEP="${a_deep}" \
  AMSFTP_RECOVERY_B_ROOT="${b_root}" \
  AMSFTP_RECOVERY_SSHD_A_CONFIG="${root}/sshd-a/sshd_config" \
  AMSFTP_RECOVERY_SSHD_A_LOG="${root}/sshd-a/sshd.log" \
  AMSFTP_RECOVERY_SSHD_A_PID="${sshd_a_pid}" \
  AMSFTP_RECOVERY_SSHD_A_PORT="${port_a}" \
  AMSFTP_RECOVERY_STATE_HOME="${state_home}" \
  python3 ./internal/integration/hosted-stage1-recovery.py

printf 'hosted Stage 1 recovery matrix passed\n'
