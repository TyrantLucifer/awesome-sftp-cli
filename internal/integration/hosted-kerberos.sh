#!/usr/bin/env bash
set -euo pipefail

if test "$(id -u)" -ne 0; then
  printf 'hosted-kerberos.sh must run as root\n' >&2
  exit 1
fi

: "${AMSFTP_KERBEROS_BINARY:?AMSFTP_KERBEROS_BINARY is required}"

root="${AMSFTP_KERBEROS_ROOT:-/tmp/amsftp-hosted-kerberos}"
case "${root}" in
  /tmp/amsftp-kerberos-* | /tmp/amsftp-hosted-kerberos) ;;
  *)
    printf 'AMSFTP_KERBEROS_ROOT must be a dedicated /tmp/amsftp-kerberos-* path\n' >&2
    exit 1
    ;;
esac

realm=AMSFTP.TEST
client_user=amsftp-krb-client
installed=/usr/bin/amsftp-kerberos-test
kdc_pid=
sshd_pid=
state_home="/var/lib/amsftp-kerberos-state"
daemon_log="${state_home}/amsftp/log/daemon.jsonl"

stop_group() {
  local pid="$1"
  if test -n "${pid}"; then
    kill -TERM -- "-${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  fi
}

cleanup() {
  stop_group "${sshd_pid}"
  stop_group "${kdc_pid}"
  pkill -KILL -u "${client_user}" -f "${installed}" 2>/dev/null || true
  rm -f "${installed}"
  rm -rf "${state_home}"
  if id "${client_user}" >/dev/null 2>&1; then
    userdel -r "${client_user}" >/dev/null 2>&1 || true
  fi
  rm -rf "${root}"
}
trap cleanup EXIT

if id "${client_user}" >/dev/null 2>&1; then
  userdel -r "${client_user}" >/dev/null 2>&1 || true
fi
useradd --create-home --shell /bin/bash "${client_user}"
account_password="$(openssl rand -hex 24)"
printf '%s:%s\n' "${client_user}" "${account_password}" | chpasswd
unset account_password
client_home="$(getent passwd "${client_user}" | cut -d: -f6)"
install -d -o "${client_user}" -g "${client_user}" -m 0700 "${state_home}"

rm -rf "${root}"
install -d -o root -g root -m 0755 "${root}"
install -d -o "${client_user}" -g "${client_user}" -m 0700 "${root}/client"
install -o root -g root -m 0755 "${AMSFTP_KERBEROS_BINARY}" "${installed}"

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

kdc_port="$(free_port)"
sshd_port="$(free_port)"
krb5_config="${root}/krb5.conf"
kdc_config="${root}/kdc.conf"
kdc_log="${root}/kdc.log"
client_keytab="${root}/client/client.keytab"
client_ccache="${root}/client/client.ccache"
host_keytab="${root}/host.keytab"
client_principal="${client_user}@${realm}"
host_principal="host/localhost@${realm}"
master_password="$(openssl rand -hex 24)"

cat >"${krb5_config}" <<EOF
[libdefaults]
  default_realm = ${realm}
  dns_lookup_kdc = false
  dns_lookup_realm = false
  rdns = false
  forwardable = false
  default_ccache_name = FILE:${client_ccache}

[realms]
  ${realm} = {
    kdc = 127.0.0.1:${kdc_port}
  }

[domain_realm]
  .localhost = ${realm}
  localhost = ${realm}
EOF

cat >"${kdc_config}" <<EOF
[kdcdefaults]
  kdc_listen = 127.0.0.1:${kdc_port}
  kdc_tcp_listen = 127.0.0.1:${kdc_port}

[realms]
  ${realm} = {
    database_name = ${root}/principal
    key_stash_file = ${root}/.k5.${realm}
    acl_file = ${root}/kadm5.acl
    max_life = 10m
    max_renewable_life = 20m
    supported_enctypes = aes256-cts-hmac-sha1-96:normal aes128-cts-hmac-sha1-96:normal
  }

[logging]
  kdc = FILE:${kdc_log}
EOF

env KRB5_CONFIG="${krb5_config}" KRB5_KDC_PROFILE="${kdc_config}" \
  /usr/sbin/kdb5_util create -s -P "${master_password}" -r "${realm}" >/dev/null
env KRB5_CONFIG="${krb5_config}" KRB5_KDC_PROFILE="${kdc_config}" \
  /usr/sbin/kadmin.local -r "${realm}" -q "addprinc -randkey ${client_principal}" >/dev/null
env KRB5_CONFIG="${krb5_config}" KRB5_KDC_PROFILE="${kdc_config}" \
  /usr/sbin/kadmin.local -r "${realm}" -q "ktadd -k ${client_keytab} ${client_principal}" >/dev/null
env KRB5_CONFIG="${krb5_config}" KRB5_KDC_PROFILE="${kdc_config}" \
  /usr/sbin/kadmin.local -r "${realm}" -q "addprinc -randkey ${host_principal}" >/dev/null
env KRB5_CONFIG="${krb5_config}" KRB5_KDC_PROFILE="${kdc_config}" \
  /usr/sbin/kadmin.local -r "${realm}" -q "ktadd -k ${host_keytab} ${host_principal}" >/dev/null
chown "${client_user}:${client_user}" "${client_keytab}"
chmod 0600 "${client_keytab}" "${host_keytab}"

setsid env KRB5_CONFIG="${krb5_config}" KRB5_KDC_PROFILE="${kdc_config}" \
  /usr/sbin/krb5kdc -n -r "${realm}" -P "${root}/krb5kdc.pid" >/dev/null 2>&1 &
kdc_pid=$!
deadline=$((SECONDS + 10))
until /usr/bin/nc -z 127.0.0.1 "${kdc_port}"; do
  if test "${SECONDS}" -ge "${deadline}"; then
    sed -n '1,160p' "${kdc_log}" >&2 || true
    printf 'test KDC did not become ready\n' >&2
    exit 1
  fi
  sleep 0.1
done

printf '%s\n' "${client_principal}" >"${client_home}/.k5login"
printf 'gssapi-ready\n' >"${client_home}/endpoint-gssapi.txt"
chown "${client_user}:${client_user}" "${client_home}/.k5login" "${client_home}/endpoint-gssapi.txt"
chmod 0600 "${client_home}/.k5login"

sshd_root="${root}/sshd"
install -d -o root -g root -m 0700 "${sshd_root}"
/usr/bin/ssh-keygen -q -t ed25519 -N '' -f "${sshd_root}/host_key"
cat >"${sshd_root}/sshd_config" <<EOF
Port ${sshd_port}
ListenAddress 127.0.0.1
HostKey ${sshd_root}/host_key
PidFile ${sshd_root}/sshd.pid
AuthorizedKeysFile none
PasswordAuthentication no
KbdInteractiveAuthentication no
PubkeyAuthentication no
GSSAPIAuthentication yes
GSSAPICleanupCredentials yes
GSSAPIStrictAcceptorCheck no
UsePAM no
StrictModes yes
PermitRootLogin no
AllowAgentForwarding no
AllowTcpForwarding no
X11Forwarding no
Subsystem sftp internal-sftp
AllowUsers ${client_user}
EOF
mkdir -p /run/sshd
setsid env KRB5_CONFIG="${krb5_config}" KRB5_KTNAME="FILE:${host_keytab}" \
  /usr/sbin/sshd -D -e -f "${sshd_root}/sshd_config" >"${sshd_root}/sshd.log" 2>&1 &
sshd_pid=$!
deadline=$((SECONDS + 10))
until /usr/bin/nc -z 127.0.0.1 "${sshd_port}"; do
  if test "${SECONDS}" -ge "${deadline}"; then
    sed -n '1,160p' "${sshd_root}/sshd.log" >&2 || true
    printf 'Kerberos sshd did not become ready\n' >&2
    exit 1
  fi
  sleep 0.1
done

install -d -o "${client_user}" -g "${client_user}" -m 0700 "${client_home}/.ssh"
/usr/bin/ssh-keyscan -p "${sshd_port}" 127.0.0.1 2>/dev/null | \
  sed "s/^\[127\\.0\\.0\\.1\]:${sshd_port}/[localhost]:${sshd_port}/" >"${client_home}/.ssh/known_hosts"
chown "${client_user}:${client_user}" "${client_home}/.ssh/known_hosts"
chmod 0600 "${client_home}/.ssh/known_hosts"
cat >"${client_home}/.ssh/config" <<EOF
Host auth-gssapi
  HostName localhost
  Port ${sshd_port}
  User ${client_user}
  ConnectTimeout 5
  GlobalKnownHostsFile /dev/null
  UserKnownHostsFile ${client_home}/.ssh/known_hosts
  StrictHostKeyChecking yes
  LogLevel ERROR
  GSSAPIAuthentication yes
  GSSAPITrustDNS no
  GSSAPIDelegateCredentials yes
  PreferredAuthentications gssapi-with-mic
  PasswordAuthentication no
  KbdInteractiveAuthentication no
  PubkeyAuthentication no
EOF
chown "${client_user}:${client_user}" "${client_home}/.ssh/config"
chmod 0600 "${client_home}/.ssh/config"

expect_script="${root}/kerberos.expect"
vt_observer="${root}/vt-observer"
go build -trimpath -o "${vt_observer}" ./internal/integration/vt-observer
chmod 0755 "${vt_observer}"
cat >"${expect_script}" <<'EXPECT'
set timeout 30
match_max 200000
log_user 0
log_file -noappend $env(AMSFTP_OUTPUT)
spawn -noecho /bin/sh -c {exec "$AMSFTP_INSTALLED" "$AMSFTP_LOCATION" /tmp 2>"$AMSFTP_STDERR"}
if {$env(AMSFTP_EXPECT_SUCCESS) == "yes"} {
  expect {
    -exact $env(AMSFTP_MARKER) {}
    eof { exit 91 }
    timeout { exit 92 }
  }
  send -- "q"
  expect {
    eof { exit 0 }
    timeout { exit 93 }
  }
}
set timeout 1
for {set attempt 0} {$attempt < 30} {incr attempt} {
  expect {
    eof { exit 90 }
    -exact $env(AMSFTP_MARKER) { exit 94 }
    timeout {}
  }
  if {![catch {
    set handle [open $env(AMSFTP_DAEMON_LOG) r]
    set daemon_log [read $handle]
    close $handle
  }] && [string first {"event":"rpc_request_failed"} $daemon_log] >= 0 && [string first {"error_code":"auth_required"} $daemon_log] >= 0} {
    send -- "q"
    set timeout 5
    expect {
      eof { exit 0 }
      timeout { exit 96 }
    }
  }
}
exit 95
EXPECT
chmod 0644 "${expect_script}"

stop_client_daemon() {
  local daemon_pattern deadline
  daemon_pattern="^${installed} daemon$"
  if ! pgrep -u "${client_user}" -f "${daemon_pattern}" >/dev/null; then
    printf 'Kerberos daemon already stopped with isolated login session\n'
    return 0
  fi
  if ! pkill -TERM -u "${client_user}" -f "${daemon_pattern}"; then
    if ! pgrep -u "${client_user}" -f "${daemon_pattern}" >/dev/null; then
      printf 'Kerberos daemon stopped before explicit SIGTERM\n'
      return 0
    fi
    printf 'Kerberos daemon could not be signaled\n' >&2
    return 1
  fi
  deadline=$((SECONDS + 5))
  while pgrep -u "${client_user}" -f "${daemon_pattern}" >/dev/null; do
    if test "${SECONDS}" -ge "${deadline}"; then
      ps -o pid=,ppid=,stat=,args= -u "${client_user}" | sed -n '1,80p' >&2
      printf 'Kerberos daemon did not stop after SIGTERM\n' >&2
      return 1
    fi
    sleep 0.1
  done
}

run_case() {
  local name="$1"
  local expect_success="$2"
  local case_root="${client_home}/cases/${name}"
  local output="${case_root}/terminal.log"
  local client_stderr="${case_root}/client.stderr"
  install -d -o "${client_user}" -g "${client_user}" -m 0700 "${case_root}"
  rm -f "${daemon_log}"
  set +e
  runuser -u "${client_user}" -- env -i \
    HOME="${client_home}" \
    XDG_STATE_HOME="${state_home}" \
    PATH=/usr/bin:/bin \
    TERM=xterm-256color \
    KRB5_CONFIG="${krb5_config}" \
    KRB5CCNAME="FILE:${client_ccache}" \
    AMSFTP_INSTALLED="${installed}" \
    AMSFTP_LOCATION="auth-gssapi:${client_home}" \
    AMSFTP_MARKER=endpoint-gssapi.txt \
    AMSFTP_EXPECT_SUCCESS="${expect_success}" \
    AMSFTP_OUTPUT="${output}" \
    AMSFTP_STDERR="${client_stderr}" \
    AMSFTP_DAEMON_LOG="${daemon_log}" \
    /usr/bin/expect -f "${expect_script}"
  expect_exit=$?
  set -e
  if test "${expect_exit}" -ne 0; then
    printf 'Kerberos case %s expect exit: %s\n' "${name}" "${expect_exit}" >&2
    /usr/bin/strings "${client_stderr}" 2>/dev/null | sed -n '1,120p' >&2 || true
    /usr/bin/strings "${output}" 2>/dev/null | sed -n '1,160p' >&2 || true
    sed -n '1,160p' "${sshd_root}/sshd.log" >&2 || true
    sed -n '1,160p' "${kdc_log}" >&2 || true
    exit 1
  fi
  if test "${expect_success}" != yes; then
    if ! grep -F '"event":"rpc_request_failed"' "${daemon_log}" | grep -Fq '"error_code":"auth_required"'; then
      printf 'Kerberos case %s did not record a bounded authentication failure\n' "${name}" >&2
      sed -n '1,120p' "${daemon_log}" >&2 || true
      exit 1
    fi
    if ! "${vt_observer}" -columns 200 -rows 30 'connect auth-gssapi failed' <"${output}"; then
      printf 'Kerberos case %s did not render its authentication failure\n' "${name}" >&2
      /usr/bin/strings "${output}" 2>/dev/null | sed -n '1,160p' >&2 || true
      exit 1
    fi
  fi
  stop_client_daemon
  printf 'Kerberos case %s passed\n' "${name}"
}

acquire_ticket() {
  local lifetime="$1"
  runuser -u "${client_user}" -- env KRB5_CONFIG="${krb5_config}" \
    /usr/bin/kinit -k -t "${client_keytab}" -c "FILE:${client_ccache}" -l "${lifetime}" "${client_principal}"
  chown "${client_user}:${client_user}" "${client_ccache}"
  chmod 0600 "${client_ccache}"
}

destroy_ticket() {
  runuser -u "${client_user}" -- env KRB5_CONFIG="${krb5_config}" \
    /usr/bin/kdestroy -c "FILE:${client_ccache}" 2>/dev/null || true
  rm -f "${client_ccache}"
}

preflight_sftp_transport() {
  local preflight_stderr="${root}/preflight.stderr"
  if ! runuser -u "${client_user}" -- env -i \
    HOME="${client_home}" \
    PATH=/usr/bin:/bin \
    KRB5_CONFIG="${krb5_config}" \
    KRB5CCNAME="FILE:${client_ccache}" \
    /usr/bin/timeout 10 /usr/bin/ssh \
      -T -oEscapeChar=none -oForwardAgent=no -oForwardX11=no \
      -oPermitLocalCommand=no -oClearAllForwardings=yes -oRemoteCommand=none \
      -oStdinNull=no -oForkAfterAuthentication=no -oTunnel=no \
      -oGSSAPIDelegateCredentials=no -s auth-gssapi sftp \
      </dev/null >/dev/null 2>"${preflight_stderr}"; then
    printf 'exact OpenSSH Kerberos SFTP preflight failed\n' >&2
    sed -n '1,120p' "${preflight_stderr}" >&2 || true
    runuser -u "${client_user}" -- env KRB5_CONFIG="${krb5_config}" \
      /usr/bin/klist -c "FILE:${client_ccache}" >&2 || true
    sed -n '1,160p' "${sshd_root}/sshd.log" >&2 || true
    sed -n '1,160p' "${kdc_log}" >&2 || true
    exit 1
  fi
  rm -f "${preflight_stderr}"
}

acquire_ticket 5m
preflight_sftp_transport
run_case valid yes

destroy_ticket
run_case missing no

acquire_ticket 5s
sleep 6
if runuser -u "${client_user}" -- env KRB5_CONFIG="${krb5_config}" \
  /usr/bin/klist -s -c "FILE:${client_ccache}"; then
  printf 'short-lived Kerberos ticket did not expire\n' >&2
  exit 1
fi
run_case expired no

acquire_ticket 5m
run_case externally-renewed yes

if grep -RIlF -- "${client_ccache}" "${client_home}" | grep -q .; then
  printf 'Kerberos credential-cache path persisted in client-owned artifacts\n' >&2
  exit 1
fi
while IFS= read -r -d '' candidate; do
  if cmp -s "${client_ccache}" "${candidate}"; then
    printf 'Kerberos credential cache was copied to %s\n' "${candidate}" >&2
    exit 1
  fi
done < <(find "${client_home}" -type f -print0)

destroy_ticket
rm -f "${client_keytab}"
test ! -e "${client_ccache}"
printf 'hosted Kerberos/GSSAPI matrix passed\n'
