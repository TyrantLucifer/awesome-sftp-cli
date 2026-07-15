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
installed=/usr/bin/amsftp-auth-test
client_user=amsftp-client
target_user=amsftp-target
mfa_user=amsftp-mfa
sshd_pid=
state_home="/var/lib/amsftp-auth-state"

cleanup() {
  if test -n "${sshd_pid}"; then
    kill -TERM -- "-${sshd_pid}" 2>/dev/null || true
    wait "${sshd_pid}" 2>/dev/null || true
  fi
  pkill -KILL -u "${client_user}" -f "${installed}" 2>/dev/null || true
  pkill -KILL -u "${client_user}" -x ssh-agent 2>/dev/null || true
  rm -f "${installed}"
  rm -rf "${state_home}"
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
install -d -o "${client_user}" -g "${client_user}" -m 0700 "${state_home}"
printf '%s:%s\n%s:%s\n' "${target_user}" "${password}" "${mfa_user}" "${mfa_password}" | chpasswd

install -d -o "${client_user}" -g "${client_user}" -m 0700 "${client_home}/.ssh"
runuser -u "${client_user}" -- /usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${client_home}/.ssh/client_key"
runuser -u "${client_user}" -- /usr/bin/ssh-keygen -q -t ed25519 -N "${key_passphrase}" -f "${client_home}/.ssh/mfa_key"
chmod 0600 "${client_home}/.ssh/client_key" "${client_home}/.ssh/mfa_key"

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

proc record_failure {stage} {
  global env
  set diagnostic [open $env(AMSFTP_DIAGNOSTIC) a]
  if {[catch {wait} wait_result]} {
    puts $diagnostic "$stage wait_error"
  } else {
    puts $diagnostic "$stage wait=$wait_result"
  }
  close $diagnostic
}

proc record_timeout {stage} {
  global env
  set diagnostic [open $env(AMSFTP_DIAGNOSTIC) a]
  puts $diagnostic "$stage timeout"
  close $diagnostic
}

proc record_observation {observation} {
  global env
  set diagnostic [open $env(AMSFTP_DIAGNOSTIC) a]
  puts $diagnostic "$observation observed"
  close $diagnostic
}

if {[catch {
  spawn -noecho /bin/sh -c {exec "$AMSFTP_INSTALLED" "$AMSFTP_LOCATION" /tmp 2>"$AMSFTP_STDERR"}
}]} {
  set diagnostic [open $env(AMSFTP_DIAGNOSTIC) a]
  puts $diagnostic "spawn error"
  close $diagnostic
  exit 89
}

proc expect_marker {} {
  global env
  expect {
    -exact $env(AMSFTP_MARKER) { return }
    eof { record_failure marker_eof; exit 91 }
    timeout { record_timeout marker; exit 92 }
  }
}

proc expect_prompt {pattern} {
  expect {
    -re $pattern { return }
    eof { record_failure prompt_eof; exit 93 }
    timeout { record_timeout prompt; exit 94 }
  }
}

proc expect_process_exit {} {
  expect {
    eof { return }
    timeout { record_timeout process_exit; exit 95 }
  }
}

switch -- $env(AMSFTP_CASE_MODE) {
  none {
    expect_marker
  }
  password {
    expect_prompt {(?i)password:}
    send -- "$env(AMSFTP_PASSWORD)\r"
    expect_marker
  }
  mfa {
    expect_prompt {(?i)passphrase}
    send -- "$env(AMSFTP_KEY_PASSPHRASE)\r"
    expect_prompt {(?i)password:}
    send -- "$env(AMSFTP_MFA_PASSWORD)\r"
    expect_marker
  }
  confirm {
    expect_prompt {(?i)(authenticity|continue connecting|yes/no)}
    send -- "yes\r"
    expect_prompt {(?i)password:}
    send -- "$env(AMSFTP_PASSWORD)\r"
    expect_marker
  }
  wrong {
    expect_prompt {(?i)password:}
    send -- "definitely-wrong\r"
    expect_prompt {(?i)(authentication failed|connect .* failed|failed)}
    record_observation bounded_failure
  }
  cancel {
    expect_prompt {(?i)password:}
    send -- "\033"
    after 1000
    send -- "q"
    expect_process_exit
    record_observation bounded_failure
    exit 0
  }
  failure {
    after 1000
    send -- "q"
    expect_process_exit
    record_observation bounded_failure
    exit 0
  }
  default {
    exit 90
  }
}

send -- "q"
expect_process_exit
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

stop_client_daemon() {
  local daemon_pattern deadline
  daemon_pattern="^${installed} daemon$"
  if ! pgrep -u "${client_user}" -f "${daemon_pattern}" >/dev/null; then
    printf 'authentication daemon already stopped with isolated login session\n'
    return 0
  fi
  if ! pkill -TERM -u "${client_user}" -f "${daemon_pattern}"; then
    if ! pgrep -u "${client_user}" -f "${daemon_pattern}" >/dev/null; then
      printf 'authentication daemon stopped before explicit SIGTERM\n'
      return 0
    fi
    printf 'authentication daemon could not be signaled\n' >&2
    return 1
  fi
  deadline=$((SECONDS + 5))
  while pgrep -u "${client_user}" -f "${daemon_pattern}" >/dev/null; do
    if test "${SECONDS}" -ge "${deadline}"; then
      ps -o pid=,ppid=,stat=,args= -u "${client_user}" | sed -n '1,80p' >&2
      printf 'authentication daemon did not stop after SIGTERM\n' >&2
      return 1
    fi
    sleep 0.1
  done
}

preflight_sftp_transport() {
  local alias_name="$1"
  local preflight_stderr="${client_home}/preflight-${alias_name}.stderr"
  if ! runuser -u "${client_user}" -- env -i \
    HOME="${client_home}" \
    XDG_STATE_HOME="${state_home}" \
    PATH=/usr/local/bin:/usr/bin:/bin \
    /usr/bin/timeout 10 /usr/bin/ssh \
      -T -oEscapeChar=none -oForwardAgent=no -oForwardX11=no \
      -oPermitLocalCommand=no -oClearAllForwardings=yes -oRemoteCommand=none \
      -oStdinNull=no -oForkAfterAuthentication=no -oTunnel=no \
      -oGSSAPIDelegateCredentials=no -s "${alias_name}" sftp \
      </dev/null >/dev/null 2>"${preflight_stderr}"; then
    printf 'exact OpenSSH SFTP preflight failed for %s\n' "${alias_name}" >&2
    sed -n '1,120p' "${preflight_stderr}" >&2 || true
    sed -n '1,160p' "${sshd_root}/sshd.log" >&2 || true
    return 1
  fi
  rm -f "${preflight_stderr}"
}

run_case() {
  name="$1"
  mode="$2"
  alias_name="$3"
  remote_user="$4"
  marker="$5"
  case_root="${client_home}/cases/${name}"
  install -d -o "${client_user}" -g "${client_user}" -m 0700 "${case_root}"
  output="${case_root}/terminal.log"
  diagnostic="${case_root}/expect.diagnostic"
  client_stderr="${case_root}/client.stderr"
  set +e
  runuser -u "${client_user}" -- env -i \
    HOME="${client_home}" \
    XDG_STATE_HOME="${state_home}" \
    PATH=/usr/local/bin:/usr/bin:/bin \
    TERM=xterm-256color \
    AMSFTP_DIAGNOSTIC="${diagnostic}" \
    AMSFTP_INSTALLED="${installed}" \
    AMSFTP_OUTPUT="${output}" \
    AMSFTP_STDERR="${client_stderr}" \
    AMSFTP_LOCATION="${alias_name}:${remote_user}" \
    AMSFTP_MARKER="${marker}" \
    AMSFTP_CASE_MODE="${mode}" \
    AMSFTP_PASSWORD="${password}" \
    AMSFTP_MFA_PASSWORD="${mfa_password}" \
    AMSFTP_KEY_PASSPHRASE="${key_passphrase}" \
    SSH_AUTH_SOCK="${SSH_AUTH_SOCK:-}" \
    /usr/bin/expect -f "${expect_script}"
  expect_exit=$?
  set -e
  if test "${expect_exit}" -ne 0; then
    printf 'expect exit: %s\n' "${expect_exit}" >&2
    sed -n '1,40p' "${diagnostic}" 2>/dev/null >&2 || true
    /usr/bin/strings "${client_stderr}" 2>/dev/null | sed -n '1,120p' | sed \
      -e "s/${password}/[redacted]/g" \
      -e "s/${mfa_password}/[redacted]/g" \
      -e "s/${key_passphrase}/[redacted]/g" >&2 || true
    /usr/bin/strings "${output}" 2>/dev/null | sed -n '1,200p' | sed \
      -e "s/${password}/[redacted]/g" \
      -e "s/${mfa_password}/[redacted]/g" \
      -e "s/${key_passphrase}/[redacted]/g" >&2 || true
    output_bytes=0
    if test -f "${output}"; then
      output_bytes="$(wc -c <"${output}")"
    fi
    printf 'terminal output bytes: %s\n' "${output_bytes}" >&2
    runtime_root="/tmp/amsftp-$(id -u "${client_user}")"
    if test -e "${runtime_root}"; then
      stat -c 'runtime=%n type=%F uid=%u gid=%g mode=%a' "${runtime_root}" >&2 || true
      find "${runtime_root}" -maxdepth 1 -printf 'runtime-entry=%f type=%y uid=%U gid=%G mode=%m\n' >&2 || true
    else
      printf 'runtime root absent: %s\n' "${runtime_root}" >&2
    fi
    daemon_diagnostic="${case_root}/daemon.diagnostic"
    set +e
    runuser -u "${client_user}" -- env -i \
      HOME="${client_home}" \
      XDG_STATE_HOME="${state_home}" \
      PATH=/usr/local/bin:/usr/bin:/bin \
      /usr/bin/timeout --signal=TERM 2 "${installed}" daemon \
      </dev/null >"${daemon_diagnostic}" 2>&1
    daemon_exit=$?
    set -e
    printf 'foreground daemon diagnostic exit: %s\n' "${daemon_exit}" >&2
    /usr/bin/strings "${daemon_diagnostic}" 2>/dev/null | sed -n '1,120p' >&2 || true
    sed -n '1,160p' "${sshd_root}/sshd.log" | sed \
      -e "s/${password}/[redacted]/g" \
      -e "s/${mfa_password}/[redacted]/g" \
      -e "s/${key_passphrase}/[redacted]/g" >&2 || true
    ps -o pid=,ppid=,stat=,args= -u "${client_user}" | sed -n '1,80p' >&2 || true
    printf 'authentication case %s failed\n' "${name}" >&2
    exit 1
  fi
  case "${mode}" in
    wrong | cancel | failure)
      if ! grep -qx 'bounded_failure observed' "${diagnostic}"; then
        printf 'authentication case %s did not report a bounded pane-local failure\n' "${name}" >&2
        /usr/bin/strings "${output}" 2>/dev/null | sed -n '1,120p' >&2 || true
        exit 1
      fi
      ;;
  esac
  stop_client_daemon
  printf 'authentication case %s passed\n' "${name}"
}

common="$(common_config)"

include_directory="${client_home}/.ssh/conf.d"
install -d -o "${client_user}" -g "${client_user}" -m 0700 "${include_directory}"
cat >"${include_directory}/included.conf" <<EOF
Host auth-include-match
${common}
  User ${target_user}
EOF
chown "${client_user}:${client_user}" "${include_directory}/included.conf"
chmod 0600 "${include_directory}/included.conf"
write_config "Include ${include_directory}/*.conf
Match originalhost auth-include-match
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey"
preflight_sftp_transport auth-include-match
run_case include-match none auth-include-match "${target_home}" endpoint-auth.txt

control_path="${client_home}/.ssh/control-%C"
write_config "Host auth-control-master
${common}
  User ${target_user}
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey
  ControlMaster auto
  ControlPath ${control_path}
  ControlPersist 30"
accepted_before="$(grep -c "Accepted publickey for ${target_user}" "${sshd_root}/sshd.log" || true)"
run_case control-master-new none auth-control-master "${target_home}" endpoint-auth.txt
runuser -u "${client_user}" -- env HOME="${client_home}" /usr/bin/ssh -O check auth-control-master >/dev/null 2>&1
accepted_after_new="$(grep -c "Accepted publickey for ${target_user}" "${sshd_root}/sshd.log" || true)"
test "${accepted_after_new}" -gt "${accepted_before}"
run_case control-master-reuse none auth-control-master "${target_home}" endpoint-auth.txt
runuser -u "${client_user}" -- env HOME="${client_home}" /usr/bin/ssh -O check auth-control-master >/dev/null 2>&1
accepted_after_reuse="$(grep -c "Accepted publickey for ${target_user}" "${sshd_root}/sshd.log" || true)"
test "${accepted_after_reuse}" = "${accepted_after_new}"
runuser -u "${client_user}" -- env HOME="${client_home}" /usr/bin/ssh -O exit auth-control-master >/dev/null 2>&1

write_config "Host auth-key
${common}
  User ${target_user}
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey"
preflight_sftp_transport auth-key
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

confirm_known_hosts="${client_home}/cases/confirm/confirmed-known-hosts"
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

changed_key="${sshd_root}/changed_host_key"
changed_known_hosts="${client_home}/.ssh/changed_known_hosts"
/usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${changed_key}"
printf '[127.0.0.1]:%s %s\n' "${port}" "$(cut -d' ' -f1,2 "${changed_key}.pub")" >"${changed_known_hosts}"
chown "${client_user}:${client_user}" "${changed_known_hosts}"
chmod 0600 "${changed_known_hosts}"
changed_known_hosts_digest="$(sha256sum "${changed_known_hosts}" | cut -d' ' -f1)"
daemon_log="${state_home}/amsftp/log/daemon.jsonl"
host_key_failures_before="$(grep -c '"event":"rpc_request_failed".*"error_code":"permission_denied"' "${daemon_log}" 2>/dev/null || true)"
write_config "Host auth-host-key-changed
  HostName 127.0.0.1
  Port ${port}
  User ${target_user}
  ConnectTimeout 5
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${changed_known_hosts}
  StrictHostKeyChecking yes
  LogLevel ERROR
  IdentityFile ${client_home}/.ssh/client_key
  IdentitiesOnly yes
  PreferredAuthentications publickey"
run_case host-key-changed failure auth-host-key-changed "${target_home}" endpoint-auth.txt
test "$(sha256sum "${changed_known_hosts}" | cut -d' ' -f1)" = "${changed_known_hosts_digest}"
host_key_failures_after="$(grep -c '"event":"rpc_request_failed".*"error_code":"permission_denied"' "${daemon_log}" 2>/dev/null || true)"
test "${host_key_failures_after}" -gt "${host_key_failures_before}"

for secret in "${password}" "${mfa_password}" "${key_passphrase}"; do
  if find "${root}" "${client_home}" "${state_home}" -type f -readable -exec grep -IlF -- "${secret}" {} + | grep -q .; then
    printf 'authentication plaintext persisted in test-owned files\n' >&2
    exit 1
  fi
done

AMSFTP_RECOVERY_BINARY="${installed}" \
  AMSFTP_RECOVERY_ROOT="${root}-recovery" \
  bash ./internal/integration/hosted-stage1-recovery.sh

printf 'hosted authentication matrix passed\n'
