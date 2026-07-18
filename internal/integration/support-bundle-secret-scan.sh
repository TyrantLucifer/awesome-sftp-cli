run_support_bundle_secret_scan() (
  if test "$#" -lt 6; then
    printf 'support-bundle secret scan requires user, binary, home, state, output, and needle file\n' >&2
    return 1
  fi

  local scan_user="$1"
  local binary="$2"
  local scan_home="$3"
  local scan_state="$4"
  local scan_root="$5"
  local needle_file="$6"
  shift 6

  case "${scan_root}" in
    "${scan_home}"/*) ;;
    *)
      printf 'support-bundle scan root must be beneath the isolated home\n' >&2
      return 1
      ;;
  esac
  test -x "${binary}"
  test -s "${needle_file}"
  test ! -e "${scan_root}"

  local derived_needles="${needle_file}.derived"
  test ! -e "${derived_needles}"
  cleanup_support_bundle_secret_scan() {
    rm -f "${needle_file}" "${derived_needles}"
    rm -rf "${scan_root}"
  }
  trap cleanup_support_bundle_secret_scan EXIT
  install -o root -g root -m 0600 "${needle_file}" "${derived_needles}"
  local credential
  for credential in "$@"; do
    test -s "${credential}"
    sha256sum "${credential}" | cut -d' ' -f1 >>"${derived_needles}"
    base64 -w 0 "${credential}" >>"${derived_needles}"
    printf '\n' >>"${derived_needles}"
  done

  local scan_group
  scan_group="$(id -gn "${scan_user}")"
  install -d -o "${scan_user}" -g "${scan_group}" -m 0700 "${scan_root}"

  local preview="${scan_root}/preview.json"
  local result="${scan_root}/create.json"
  local archive="${scan_root}/support.tar.gz"
  local expanded="${scan_root}/expanded"
  runuser -u "${scan_user}" -- env -i \
    HOME="${scan_home}" \
    XDG_STATE_HOME="${scan_state}" \
    PATH=/usr/local/bin:/usr/bin:/bin \
    "${binary}" support-bundle preview --format json >"${preview}"
  chmod 0600 "${preview}"

  local consent
  consent="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["consent_digest"])' "${preview}")"
  printf '%s\n' "${consent}" | grep -Eq '^[0-9a-f]{64}$'
  runuser -u "${scan_user}" -- env -i \
    HOME="${scan_home}" \
    XDG_STATE_HOME="${scan_state}" \
    PATH=/usr/local/bin:/usr/bin:/bin \
    "${binary}" support-bundle create --consent "${consent}" --output "${archive}" --format json >"${result}"
  chmod 0600 "${result}"

  local mode owner
  mode="$(stat -c '%a' "${archive}")"
  owner="$(stat -c '%U' "${archive}")"
  if test "${mode}" != 600 || test "${owner}" != "${scan_user}"; then
    printf 'created support bundle is not an owner-private mode=600 artifact\n' >&2
    return 1
  fi

  install -d -o root -g root -m 0700 "${expanded}"
  tar -xzf "${archive}" --no-same-owner --no-same-permissions -C "${expanded}"
  if find "${scan_root}" -type f -readable -exec grep -aFl -f "${derived_needles}" {} + | grep -q .; then
    printf 'authentication secret or private path appeared in support-bundle output\n' >&2
    return 1
  fi

  local candidate
  for credential in "$@"; do
    while IFS= read -r -d '' candidate; do
      if cmp -s "${credential}" "${candidate}"; then
        printf 'authentication credential bytes were copied into support-bundle output\n' >&2
        return 1
      fi
    done < <(find "${scan_root}" -type f -print0)
  done

  printf 'real authentication support-bundle secret scan passed\n'
)
