#!/usr/bin/env bash
set -euo pipefail

: "${AMSFTP_VENDOR_BINARY:?AMSFTP_VENDOR_BINARY is required}"
: "${AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE:?AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE is required}"
: "${AMSFTP_VENDOR_SFTP_ROOT:?AMSFTP_VENDOR_SFTP_ROOT is required}"

root="${AMSFTP_VENDOR_SFTP_ROOT}"
root_prefix=/tmp/amsftp-vendor-sftp-
root_suffix="${root#"${root_prefix}"}"
case "${root}" in
  "${root_prefix}"*) ;;
  *)
    printf 'AMSFTP_VENDOR_SFTP_ROOT must be a dedicated /tmp/amsftp-vendor-sftp-* path\n' >&2
    exit 1
    ;;
esac
case "${root_suffix}" in
  '' | *[!A-Za-z0-9._-]*)
    printf 'AMSFTP_VENDOR_SFTP_ROOT suffix must be one safe path component\n' >&2
    exit 1
    ;;
esac
if test -e "${root}"; then
  printf 'vendor SFTP root already exists\n' >&2
  exit 1
fi
install -d -m 0700 "${root}"

actual_version_file="${root}/proftpd-actual-version"
{
  /usr/sbin/proftpd -v 2>&1
  dpkg-query -W -f='${Package}=${Version}\n' proftpd-core proftpd-mod-crypto | LC_ALL=C sort
} >"${actual_version_file}"
if ! cmp -s "${AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE}" "${actual_version_file}"; then
  printf 'current ProFTPD vendor SFTP version drifted before fixture mutation\n' >&2
  diff -u "${AMSFTP_VENDOR_SFTP_EXPECT_VERSION_FILE}" "${actual_version_file}" >&2 || true
  rm -rf "${root}"
  exit 1
fi
printf 'current ProFTPD vendor SFTP version binding passed\n'

vendor_user=amsftp-vendor-sftp
server_pid=
server_wrapper_pid=
user_created=0
ssh_config_created=0
ssh_directory_created=0
client_home="${HOME}"
ssh_directory="${client_home}/.ssh"
ssh_config="${ssh_directory}/config"
ssh_config_backup="${root}/client-ssh-config"

cleanup() {
  if test -n "${server_pid}"; then
    sudo kill -TERM "${server_pid}" 2>/dev/null || true
    for _ in $(seq 1 100); do
      if ! sudo kill -0 "${server_pid}" 2>/dev/null; then
        break
      fi
      sleep 0.05
    done
    sudo kill -KILL "${server_pid}" 2>/dev/null || true
  fi
  if test -n "${server_wrapper_pid}"; then
    wait "${server_wrapper_pid}" 2>/dev/null || true
  fi
  if test "${ssh_config_created}" = 1; then
    rm -f "${ssh_config}"
  elif test -f "${ssh_config_backup}"; then
    cp -p "${ssh_config_backup}" "${ssh_config}"
  fi
  if test "${ssh_directory_created}" = 1; then
    rmdir "${ssh_directory}" 2>/dev/null || true
  fi
  if test "${user_created}" = 1; then
    sudo userdel -r "${vendor_user}" >/dev/null 2>&1 || true
  fi
  rm -rf "${root}"
}
trap cleanup EXIT

if id "${vendor_user}" >/dev/null 2>&1; then
  printf 'reserved vendor SFTP user already exists\n' >&2
  exit 1
fi
sudo useradd --create-home --user-group --shell /bin/bash "${vendor_user}"
user_created=1
vendor_home="$(getent passwd "${vendor_user}" | cut -d: -f6)"

printf 'vendor-sftp-ready\n' | sudo tee "${vendor_home}/vendor-sftp-marker.txt" >/dev/null
sudo chown "${vendor_user}:${vendor_user}" "${vendor_home}/vendor-sftp-marker.txt"
sudo chmod 0600 "${vendor_home}/vendor-sftp-marker.txt"

/usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${root}/client-key"
/usr/bin/ssh-keygen -e -m RFC4716 -f "${root}/client-key.pub" >"${root}/authorized-keys"
sudo install -d -o "${vendor_user}" -g "${vendor_user}" -m 0700 "${vendor_home}/.sftp"
sudo install -o "${vendor_user}" -g "${vendor_user}" -m 0600 "${root}/authorized-keys" "${vendor_home}/.sftp/authorized_keys"
/usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${root}/host-key"

module_path="$(dpkg -L proftpd-mod-crypto | awk '/\/mod_sftp\.so$/ { print }')"
if test "$(printf '%s\n' "${module_path}" | sed '/^$/d' | wc -l | tr -d '[:space:]')" != 1; then
  printf 'expected exactly one packaged mod_sftp module\n' >&2
  exit 1
fi
port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

sudo tee "${root}/proftpd.conf" >/dev/null <<EOF
LoadModule mod_sftp.c ${module_path}
ServerName "AMSFTP isolated vendor SFTP"
ServerType standalone
DefaultServer on
UseIPv6 off
Port ${port}
Umask 077
User nobody
Group nogroup
PidFile ${root}/proftpd.pid
ScoreboardFile ${root}/proftpd.scoreboard
SystemLog ${root}/proftpd-system.log
WtmpLog off
AuthOrder mod_auth_unix.c
RequireValidShell on
DefaultRoot ~
AllowOverwrite on
SFTPEngine on
SFTPLog ${root}/proftpd-sftp.log
SFTPHostKey ${root}/host-key
SFTPAuthorizedUserKeys file:~/.sftp/authorized_keys
SFTPAuthMethods publickey
EOF
sudo /usr/sbin/proftpd -t -c "${root}/proftpd.conf"
sudo /usr/bin/setsid /usr/sbin/proftpd -n -c "${root}/proftpd.conf" >"${root}/proftpd-console.log" 2>&1 &
server_wrapper_pid=$!
deadline=$((SECONDS + 15))
until /usr/bin/nc -z 127.0.0.1 "${port}"; do
  if test "${SECONDS}" -ge "${deadline}"; then
    sudo sed -n '1,200p' "${root}/proftpd-console.log" >&2 || true
    printf 'ProFTPD vendor SFTP server did not become ready\n' >&2
    exit 1
  fi
  sleep 0.1
done
if ! test -s "${root}/proftpd.pid"; then
  printf 'ProFTPD vendor SFTP server did not publish a PID\n' >&2
  exit 1
fi
server_pid="$(sudo cat "${root}/proftpd.pid")"
test "${server_pid}" -gt 1
sudo kill -0 "${server_pid}"

if test -d "${ssh_directory}"; then
  test "$(stat -c '%a' "${ssh_directory}")" = 700
else
  install -d -m 0700 "${ssh_directory}"
  ssh_directory_created=1
fi
if test -e "${ssh_config}"; then
  test -f "${ssh_config}"
  cp -p "${ssh_config}" "${ssh_config_backup}"
else
  ssh_config_created=1
fi
/usr/bin/ssh-keyscan -p "${port}" -t ed25519 127.0.0.1 >"${root}/known-hosts" 2>/dev/null
chmod 0600 "${root}/known-hosts" "${root}/client-key"
cat >>"${ssh_config}" <<EOF

Host amsftp-proftpd
  HostName 127.0.0.1
  Port ${port}
  User ${vendor_user}
  IdentityFile ${root}/client-key
  IdentitiesOnly yes
  BatchMode yes
  ConnectTimeout 5
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${root}/known-hosts
  StrictHostKeyChecking yes
  LogLevel ERROR
EOF
chmod 0600 "${ssh_config}"

printf 'ls\n' | /usr/bin/sftp -b - amsftp-proftpd | grep -F 'vendor-sftp-marker.txt'
AMSFTP_REAL_VENDOR_SFTP=1 \
  AMSFTP_VENDOR_SFTP_ALIAS=amsftp-proftpd \
  AMSFTP_VENDOR_SFTP_ROOT=/ \
  go test -count=1 -run '^TestRealProFTPDVendorSFTPLevel0AndDurableTransfers$' ./internal/integration
printf 'vendor SFTP provider browse and durable transfers passed\n'

install -d -m 0700 \
  "${root}/xdg-config" \
  "${root}/xdg-state" \
  "${root}/xdg-cache" \
  "${root}/xdg-runtime"
export XDG_CONFIG_HOME="${root}/xdg-config"
export XDG_STATE_HOME="${root}/xdg-state"
export XDG_CACHE_HOME="${root}/xdg-cache"
export XDG_RUNTIME_DIR="${root}/xdg-runtime"
export TERM=xterm-256color
export AMSFTP_VENDOR_BINARY
expect <<'EXPECT'
set timeout 35
log_user 0
spawn -noecho $env(AMSFTP_VENDOR_BINARY) "amsftp-proftpd:/"
expect {
  -exact "vendor-sftp-marker.txt" {}
  eof { exit 91 }
  timeout { exit 92 }
}
send -- "q"
expect {
  eof {}
  timeout { exit 93 }
}
EXPECT
"${AMSFTP_VENDOR_BINARY}" daemon stop --confirm stop --format json | grep -F '"status":"stopped"'
printf 'vendor SFTP preview-binary TUI browse passed\n'
