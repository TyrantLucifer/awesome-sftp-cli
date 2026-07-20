#!/bin/sh

set -eu

umask 077

release_origin="https://github.com/TyrantLucifer/awesome-sftp-cli"
version="latest"
prefix="${HOME}/.local"
start_daemon="true"
work_dir=""
staged_binary=""

usage() {
    printf '%s\n' 'usage: install.sh [--version X.Y.Z] [--prefix /absolute/path] [--no-start-daemon]'
}

die() {
    printf 'amsftp installer: %s\n' "$1" >&2
    exit 1
}

cleanup() {
    if test -n "$staged_binary" && test -e "$staged_binary"; then
        rm -f "$staged_binary"
    fi
    if test -n "$work_dir" && test -d "$work_dir"; then
        rm -rf "$work_dir"
    fi
}

trap cleanup EXIT HUP INT TERM

while test "$#" -gt 0; do
    case "$1" in
        --version)
            test "$#" -ge 2 || die '--version requires a value'
            version=$2
            shift 2
            ;;
        --prefix)
            test "$#" -ge 2 || die '--prefix requires a value'
            prefix=$2
            shift 2
            ;;
        --no-start-daemon)
            start_daemon="false"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage >&2
            die "unknown argument: $1"
            ;;
    esac
done

case "$prefix" in
    /*) ;;
    *) die '--prefix must be an absolute path' ;;
esac
test "$prefix" != "/" || die '--prefix cannot be the filesystem root'

command -v curl >/dev/null 2>&1 || die 'curl is required'
command -v tar >/dev/null 2>&1 || die 'tar is required'
command -v install >/dev/null 2>&1 || die 'install is required'

if test "$version" = "latest"; then
    effective_url=$(curl -fsSLI --proto '=https' -o /dev/null -w '%{url_effective}' "$release_origin/releases/latest") || die 'cannot resolve the latest release'
    tag=${effective_url##*/}
    case "$tag" in
        v*) version=${tag#v} ;;
        *) die 'latest release did not resolve to a v-prefixed tag' ;;
    esac
else
    case "$version" in
        v*) version=${version#v} ;;
    esac
fi

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    die 'version must use the X.Y.Z release form'
fi

case "$(uname -s)" in
    Darwin) target_os="darwin" ;;
    Linux) target_os="linux" ;;
    *) die 'only macOS and Linux are supported' ;;
esac

case "$(uname -m)" in
    arm64|aarch64) target_arch="arm64" ;;
    x86_64|amd64) target_arch="amd64" ;;
    *) die 'only arm64 and amd64 are supported' ;;
esac

archive_name="amsftp_${version}_${target_os}_${target_arch}.tar.gz"
download_base="$release_origin/releases/download/v${version}"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/amsftp-install.XXXXXX") || die 'cannot create a temporary directory'
archive_path="$work_dir/$archive_name"
checksums_path="$work_dir/checksums.txt"

printf 'Downloading AMSFTP %s for %s/%s...\n' "$version" "$target_os" "$target_arch"
curl -fL --retry 3 --proto '=https' -o "$archive_path" "$download_base/$archive_name" || die 'archive download failed'
curl -fL --retry 3 --proto '=https' -o "$checksums_path" "$download_base/checksums.txt" || die 'checksum download failed'

expected=$(awk -v name="$archive_name" '
    $2 == name {
        if (found) exit 2
        value = $1
        found = 1
    }
    END {
        if (!found) exit 1
        print value
    }
' "$checksums_path") || die 'checksums.txt does not contain one exact archive entry'
case "$expected" in
    ''|*[!0-9a-f]*) die 'published checksum is not lowercase SHA-256' ;;
esac
test "${#expected}" -eq 64 || die 'published checksum is not 64 hexadecimal characters'

if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$archive_path" | awk '{print $1}')
else
    actual=$(shasum -a 256 "$archive_path" | awk '{print $1}')
fi
test "$actual" = "$expected" || die 'archive checksum mismatch'

archive_root="$work_dir/amsftp_${version}_${target_os}_${target_arch}"
source_binary="$archive_root/amsftp"
source_man="$archive_root/share/man/man1/amsftp.1"
tar -xzf "$archive_path" -C "$work_dir" \
    "amsftp_${version}_${target_os}_${target_arch}/amsftp" \
    "amsftp_${version}_${target_os}_${target_arch}/share/man/man1/amsftp.1" || die 'archive extraction failed'
test -f "$source_binary" && test ! -L "$source_binary" || die 'archive binary is missing or unsafe'
test -f "$source_man" && test ! -L "$source_man" || die 'archive man page is missing or unsafe'

bin_dir="$prefix/bin"
man_dir="$prefix/share/man/man1"
bash_dir="$prefix/share/bash-completion/completions"
zsh_dir="$prefix/share/zsh/site-functions"
fish_dir="$prefix/share/fish/vendor_completions.d"
for directory in "$bin_dir" "$man_dir" "$bash_dir" "$zsh_dir" "$fish_dir"; do
    test ! -L "$directory" || die "install directory is a symlink: $directory"
    install -d -m 0755 "$directory" || die "cannot prepare install directory: $directory"
done

installed_binary="$bin_dir/amsftp"
staged_binary="$bin_dir/.amsftp.install.$$"
test ! -e "$staged_binary" || die 'staged install path already exists'
install -m 0755 "$source_binary" "$staged_binary" || die 'cannot stage the new binary'
staged_version=$($staged_binary --version) || die 'staged binary version check failed'
case "$staged_version" in
    "$version "*) ;;
    *) die "staged binary reports an unexpected version: $staged_version" ;;
esac

was_running="false"
if test -x "$installed_binary" && test ! -L "$installed_binary"; then
    old_status=$($installed_binary daemon status --format json) || die 'cannot safely determine the current daemon state'
    case "$old_status" in
        *'"running":true'*)
            was_running="true"
            $installed_binary daemon stop --confirm stop --format json >/dev/null || die 'cannot stop the current daemon safely'
            stopped_status=$($installed_binary daemon status --format json) || die 'cannot verify the current daemon stopped'
            case "$stopped_status" in
                *'"running":false'*) ;;
                *) die 'current daemon did not report a stopped state' ;;
            esac
            ;;
    esac
    previous_tmp="$bin_dir/.amsftp.previous.$$"
    test ! -e "$previous_tmp" || die 'previous-version staging path already exists'
    install -m 0755 "$installed_binary" "$previous_tmp" || die 'cannot preserve the previous binary'
    mv -f "$previous_tmp" "$bin_dir/amsftp.previous" || die 'cannot publish the previous binary'
fi

mv "$staged_binary" "$installed_binary" || die 'cannot atomically publish the new binary'
staged_binary=""
install -m 0644 "$source_man" "$man_dir/amsftp.1" || die 'cannot install the man page'

completion_tmp="$work_dir/completion"
$installed_binary completion bash >"$completion_tmp" || die 'cannot generate bash completion'
install -m 0644 "$completion_tmp" "$bash_dir/amsftp" || die 'cannot install bash completion'
$installed_binary completion zsh >"$completion_tmp" || die 'cannot generate zsh completion'
install -m 0644 "$completion_tmp" "$zsh_dir/_amsftp" || die 'cannot install zsh completion'
$installed_binary completion fish >"$completion_tmp" || die 'cannot generate fish completion'
install -m 0644 "$completion_tmp" "$fish_dir/amsftp.fish" || die 'cannot install fish completion'

if test "$start_daemon" = "true"; then
    $installed_binary daemon start --format json >/dev/null || die 'new binary installed, but daemon startup failed; previous binary is preserved as amsftp.previous'
    new_status=$($installed_binary daemon status --format json) || die 'new daemon status failed'
    case "$new_status" in
        *'"running":true'*) ;;
        *) die 'new daemon did not report a running state' ;;
    esac
elif test "$was_running" = "true"; then
    printf '%s\n' 'The previous daemon was stopped and --no-start-daemon left it stopped.'
fi

if ! $installed_binary doctor --format json >/dev/null; then
    printf '%s\n' 'AMSFTP installed, but doctor reported an issue; run amsftp doctor --format json for details.' >&2
fi

printf 'AMSFTP %s installed at %s\n' "$version" "$installed_binary"
case ":${PATH}:" in
    *":$bin_dir:"*) ;;
    *) printf 'Add %s to PATH before running amsftp.\n' "$bin_dir" ;;
esac
