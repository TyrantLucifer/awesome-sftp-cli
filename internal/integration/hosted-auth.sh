#!/usr/bin/env bash
set -euo pipefail

if test "$(id -u)" -ne 0; then
  printf 'hosted-auth.sh must run as root\n' >&2
  exit 1
fi

: "${AMSFTP_AUTH_BINARY:?AMSFTP_AUTH_BINARY is required}"

root="${AMSFTP_AUTH_ROOT:-/tmp/amsftp-hosted-auth}"
case "${root}" in
  /tmp/amsftp-auth-* | /tmp/amsftp-hosted-auth) ;;
  *)
    printf 'AMSFTP_AUTH_ROOT must be a dedicated /tmp/amsftp-auth-* path\n' >&2
    exit 1
    ;;
esac
installed=/usr/local/bin/amsftp-auth-test
client_user=amsftp-client
target_user=amsftp-target
mfa_user=amsftp-mfa
sshd_pid=

cleanup() {
  if test -n "${sshd_pid}"; then
    kill -TERM -- "-${sshd_pid}" 2>/dev/null || true
    wait "${sshd_pid}" 2>/dev/null || true
  fi
  pkill -KILL -u "${client_user}" -f "${installed}" 2>/dev/null || true
  pkill -KILL -u "${client_user}" -x ssh-agent 2>/dev/null || true
}
trap cleanup EXIT

for user_name in "${client_user}" "${target_user}" "${mfa_user}"; do
  if id "${user_name}" >/dev/null 2>&1; then
    userdel -r "${user_name}" >/dev/null 2>&1 || true
  fi
  useradd --create-home --shell /bin/bash "${user_name}"
done

rm -rf "${root}"
install -d -o root -g root -m 0755 "${root}"
install -o root -g root -m 0755 "${AMSFTP_AUTH_BINARY}" "${installed}"

client_home="$(getent passwd "${client_user}" | cut -d: -f6)"
target_home="$(getent passwd "${target_user}" | cut -d: -f6)"
mfa_home="$(getent passwd "${mfa_user}" | cut -d: -f6)"
password="$(openssl rand -hex 18)"
mfa_password="$(openssl rand -hex 18)"
key_passphrase="$(openssl rand -hex 18)"
printf '%s:%s\n%s:%s\n' "${target_user}" "${password}" "${mfa_user}" "${mfa_password}" | chpasswd

install -d -o "${client_user}" -g "${client_user}" -m 0700 "${client_home}/.ssh"
runuser -u "${client_user}" -- /usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${client_home}/.ssh/client_key"
runuser -u "${client_user}" -- /usr/bin/ssh-keygen -q -t ed25519 -N "${key_passphrase}" -f "${client_home}/.ssh/mfa_key"

for destination in "${target_home}" "${mfa_home}"; do
  destination_user="$(basename "${destination}")"
  install -d -o "${destination_user}" -g "${destination_user}" -m 0700 "${destination}/.ssh"
done
install -o "${target_user}" -g "${target_user}" -m 0600 "${client_home}/.ssh/client_key.pub" "${target_home}/.ssh/authorized_keys"
install -o "${mfa_user}" -g "${mfa_user}" -m 0600 "${client_home}/.ssh/mfa_key.pub" "${mfa_home}/.ssh/authorized_keys"
printf 'key-agent-password-proxy-ready\n' >"${target_home}/endpoint-auth.txt"
printf 'multi-prompt-ready\n' >"${mfa_home}/endpoint-mfa.txt"
chown "${target_user}:${target_user}" "${target_home}/endpoint-auth.txt"
chown "${mfa_user}:${mfa_user}" "${mfa_home}/endpoint-mfa.txt"

sshd_root="${root}/sshd"
install -d -o root -g root -m 0700 "${sshd_root}"
/usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${sshd_root}/host_key"
port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
cat >"${sshd_root}/sshd_config" <<EOF
Port ${port}
ListenAddress 127.0.0.1
HostKey ${sshd_root}/host_key
PidFile ${sshd_root}/sshd.pid
PasswordAuthentication yes
KbdInteractiveAuthentication no
PubkeyAuthentication yes
UsePAM no
StrictModes yes
PermitRootLogin no
AllowAgentForwarding no
AllowTcpForwarding yes
X11Forwarding no
Subsystem sftp internal-sftp
AllowUsers ${target_user} ${mfa_user}
Match User ${mfa_user}
  AuthenticationMethods publickey,password
EOF
mkdir -p /run/sshd
setsid /usr/sbin/sshd -D -e -f "${sshd_root}/sshd_config" >"${sshd_root}/sshd.log" 2>&1 &
sshd_pid=$!
deadline=$((SECONDS + 10))
until /usr/bin/nc -z 127.0.0.1 "${port}"; do
  if test "${SECONDS}" -ge "${deadline}"; then
    sed -n '1,160p' "${sshd_root}/sshd.log" >&2
    printf 'test sshd did not become ready\n' >&2
    exit 1
  fi
  sleep 0.1
done

/usr/bin/ssh-keyscan -p "${port}" 127.0.0.1 >"${client_home}/.ssh/known_hosts" 2>/dev/null
chown "${client_user}:${client_user}" "${client_home}/.ssh/known_hosts"
chmod 0600 "${client_home}/.ssh/known_hosts"

expect_script="${root}/auth.expect"
cat >"${expect_script}" <<'EXPECT'
set timeout 35
match_max 200000
log_user 0
log_file -noappend $env(AMSFTP_OUTPUT)
spawn -noecho $env(AMSFTP_INSTALLED) $env(AMSFTP_LOCATION) /tmp

switch -- $env(AMSFTP_CASE_MODE) {
  none {
    expect -exact $env(AMSFTP_MARKER)
  }
  password {
    expect -re {(?i)password:}
    send -- "$env(AMSFTP_PASSWORD)\r"
    expect -exact $env(AMSFTP_MARKER)
  }
  mfa {
    expect -re {(?i)passphrase}
    send -- "$env(AMSFTP_KEY_PASSPHRASE)\r"
    expect -re {(?i)password:}
    send -- "$env(AMSFTP_MFA_PASSWORD)\r"
    expect -exact $env(AMSFTP_MARKER)
  }
  confirm {
    expect -re {(?i)(authenticity|continue connecting|yes/no)}
    send -- "\r"
    expect -re {(?i)password:}
    send -- "$env(AMSFTP_PASSWORD)\r"
    expect -exact $env(AMSFTP_MARKER)
  }
  wrong {
    expect -re {(?i)password:}
    send -- "definitely-wrong\r"
    expect eof
    exit 0
  }
  cancel {
    expect -re {(?i)password:}
    send -- "\033"
    expect eof
    exit 0
  }
  default {
    exit 90
  }
}

send -- "q"
expect eof
EXPECT
chmod 0644 "${expect_script}"

write_config() {
  config_body="$1"
  printf '%s\n' "${config_body}" >"${client_home}/.ssh/config"
  chown "${client_user}:${client_user}" "${client_home}/.ssh/config"
  chmod 0600 "${client_home}/.ssh/config"
}

common_config() {
  cat <<EOF
  HostName 127.0.0.1
  Port ${port}
  ConnectTimeout 5
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${client_home}/.ssh/known_hosts
  StrictHostKeyChecking yes
  LogLevel ERROR
  NumberOfPasswordPrompts 1
EOF
}

run_case() {
  name="$1"
  mode="$2"
  alias_name="$3"
  remote_user="$4"
  marker="$5"
  case_root="${root}/cases/${name}"
  for directory in "${case_root}" "${case_root}/runtime" "${case_root}/config" "${case_root}/state" "${case_root}/cache"; do
    install -d -o "${client_user}" -g "${client_user}" -m 0700 "${directory}"
  done
  output="${case_root}/terminal.log"
  if ! runuser -u "${client_user}" -- env \
    HOME="${client_home}" \
    XDG_RUNTIME_DIR="${case_root}/runtime" \
    XDG_CONFIG_HOME="${case_root}/config" \
    XDG_STATE_HOME="${case_root}/state" \
    XDG_CACHE_HOME="${case_root}/cache" \
    AMSFTP_INSTALLED="${installed}" \
    AMSFTP_OUTPUT="${output}" \
    AMSFTP_LOCATION="${alias_name}:${remote_user}" \
    AMSFTP_MARKER="${marker}" \
    AMSFTP_CASE_MODE="${mode}" \
    AMSFTP_PASSWORD="${password}" \
    AMSFTP_MFA_PASSWORD="${mfa_password}" \
    AMSFTP_KEY_PASSPHRASE="${key_passphrase}" \
    SSH_AUTH_SOCK="${SSH_AUTH_SOCK:-}" \
    /usr/bin/expect -f "${expect_script}"; then
    sed -n '1,200p' "${output}" 2>/dev/null | sed \
      -e "s/${password}/[redacted]/g" \
      -e "s/${mfa_password}/[redacted]/g" \
      -e "s/${key_passphrase}/[redacted]/g" >&2 || true
    printf 'authentication case %s failed\n' "${name}" >&2
    exit 1
  fi
  test -s "${output}"
}

common="$(common_config)"

write_config "Host auth-key
${common}
  User ${target_user}
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey"
run_case key none auth-key "${target_home}" endpoint-auth.txt

agent_socket="${client_home}/.ssh/agent.sock"
runuser -u "${client_user}" -- /usr/bin/ssh-agent -a "${agent_socket}" >/dev/null
runuser -u "${client_user}" -- env SSH_AUTH_SOCK="${agent_socket}" /usr/bin/ssh-add "${client_home}/.ssh/client_key" </dev/null >/dev/null
write_config "Host auth-agent
${common}
  User ${target_user}
  IdentityFile none
  IdentitiesOnly no
  PreferredAuthentications publickey"
export SSH_AUTH_SOCK="${agent_socket}"
run_case agent none auth-agent "${target_home}" endpoint-auth.txt
unset SSH_AUTH_SOCK

proxy_marker="${client_home}/proxy-command-hit"
proxy_script="${client_home}/proxy-command.sh"
cat >"${proxy_script}" <<EOF
#!/bin/sh
/usr/bin/touch ${proxy_marker}
exec /usr/bin/nc "\$1" "\$2"
EOF
chown "${client_user}:${client_user}" "${proxy_script}"
chmod 0700 "${proxy_script}"
write_config "Host auth-proxy-command
${common}
  User ${target_user}
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey
  ProxyCommand ${proxy_script} %h %p"
run_case proxy-command none auth-proxy-command "${target_home}" endpoint-auth.txt
test -f "${proxy_marker}"

write_config "Host auth-jump
${common}
  User ${target_user}
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey

Host auth-proxy-jump
${common}
  User ${target_user}
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey
  ProxyJump auth-jump"
run_case proxy-jump none auth-proxy-jump "${target_home}" endpoint-auth.txt

write_config "Host auth-password auth-wrong auth-cancel
${common}
  User ${target_user}
  PubkeyAuthentication no
  PreferredAuthentications password"
run_case password password auth-password "${target_home}" endpoint-auth.txt
run_case wrong-password wrong auth-wrong "${target_home}" endpoint-auth.txt
run_case cancel cancel auth-cancel "${target_home}" endpoint-auth.txt

write_config "Host auth-mfa
${common}
  User ${mfa_user}
  IdentityFile ${client_home}/.ssh/mfa_key
  IdentitiesOnly yes
  PreferredAuthentications publickey,password"
run_case mfa mfa auth-mfa "${mfa_home}" endpoint-mfa.txt

confirm_known_hosts="${root}/cases/confirm/confirmed-known-hosts"
write_config "Host auth-confirm
  HostName 127.0.0.1
  Port ${port}
  User ${target_user}
  ConnectTimeout 5
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${confirm_known_hosts}
  StrictHostKeyChecking ask
  LogLevel ERROR
  NumberOfPasswordPrompts 1
  PubkeyAuthentication no
  PreferredAuthentications password"
run_case confirm confirm auth-confirm "${target_home}" endpoint-auth.txt
test -s "${confirm_known_hosts}"

for secret in "${password}" "${mfa_password}" "${key_passphrase}"; do
  if find "${root}/cases" "${client_home}" -type f -readable -exec grep -IlF -- "${secret}" {} + | grep -q .; then
    printf 'authentication plaintext persisted in test-owned files\n' >&2
    exit 1
  fi
done

printf 'hosted authentication matrix passed\n'
