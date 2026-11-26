# Install, Upgrade, and Uninstall AMSFTP

[简体中文](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/zh-CN/user/installation.md)

AMSFTP supports macOS (Apple Silicon and Intel) and Linux (AMD64 and ARM64).
Use Homebrew for package-managed installs, or the standalone installer for a
user-owned install under `$HOME/.local`.

> [!WARNING]
> Public macOS builds are neither signed nor notarized. Download only from
> official release channels, and never disable system-wide security controls for
> an unverified binary.

## Install with Homebrew

```sh
brew install TyrantLucifer/tap/amsftp
amsftp --version
amsftp daemon start
amsftp daemon status
```

Homebrew manages the binary and installs the `amsftp(1)` manual page.

## Install with the standalone script

The official script installs into `$HOME/.local` without `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

It downloads the archive for your OS/CPU, verifies its SHA-256 checksum, checks
directory ownership and permissions, atomically replaces the binary, installs
the man page, and generates shell completions.

Make sure `$HOME/.local/bin` is on your `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

### When the installer asks for a custom private data directory

AMSFTP refuses to store private data (config, database, socket, cache) under
directories with unsafe ownership, group/world write permissions, unexpected
ACLs, or symlinks. If your home directory fails these checks, the installer
falls back to `/var/lib/amsftp-users/<your-uid>`.

If that directory does not exist yet, the installer exits without modifying
anything and prints the exact one-time administrator commands to create it. Run
those commands, then re-run the installer—or pass a pre-created private data
directory explicitly:

```sh
sh install.sh --root /absolute/private/path
```

Run `install.sh --help` to see all `--prefix`, `--root`, `--version`, and daemon
flags.

## Install an archive manually

1. Download the matching archive and `checksums.txt` from
   [GitHub Releases](https://github.com/TyrantLucifer/awesome-sftp-cli/releases).
2. Verify the SHA-256 checksum before extracting (`sha256sum <archive>` on
   Linux, or `shasum -a 256 <archive>` on macOS) and require an exact match with
   `checksums.txt`.
3. Extract to a clean directory, place `amsftp` in a user-owned `PATH` folder
   (such as `$HOME/.local/bin`), and copy `share/man/man1/amsftp.1` to your user
   man directory.
4. Generate shell completion (`amsftp completion bash|zsh|fish`) and verify:

   ```sh
   amsftp --version
   amsftp config validate
   amsftp doctor
   ```

Never run AMSFTP as `root`, set it setuid, or install it in a directory writable
by other users.

## Build from source

Requires Go 1.26.6:

```sh
git clone https://github.com/TyrantLucifer/awesome-sftp-cli.git
cd awesome-sftp-cli
GOTOOLCHAIN=local go build -trimpath -o ./amsftp ./cmd/amsftp
./amsftp --version
```

Local source builds are for development and are not managed by `amsftp upgrade`.

## Upgrade and rollback

Both Homebrew and standalone installs support:

```sh
amsftp upgrade
```

AMSFTP checks for updates before stopping anything, refuses to upgrade while a
job is actively transferring data, restarts the daemon only if it was running
before, and verifies that the new binary and daemon versions match.

If an upgrade fails **before** any database migration changed the database
format:

1. Stop your daemon: `amsftp daemon stop --confirm stop`.
2. Restore the previous verified binary (standalone installs keep it at
   `<prefix>/bin/amsftp.previous`), man page, and completion files.
3. Verify with `amsftp --version`, `amsftp daemon start`, `amsftp daemon status`,
   and `amsftp doctor`.

If a database migration already started or you are unsure whether the database
format changed, **do not downgrade to an older binary** and do not delete the
socket or SQLite files. See
[Troubleshooting and recovery](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/help/troubleshooting.md).

## Uninstall

1. Stop your user daemon:

   ```sh
   amsftp daemon stop --confirm stop
   amsftp daemon status
   ```

2. Remove the program:
   - **Homebrew:** `brew uninstall amsftp`
   - **Standalone:** delete `amsftp`, `amsftp.1`, and generated completion files.

User config, workspaces, job history, logs, and cache are kept on purpose. Back
up anything you need before removing them:

- **macOS:** `~/Library/Application Support/io.github.tyrantlucifer.amsftp`,
  `~/Library/Caches/io.github.tyrantlucifer.amsftp`,
  `~/Library/Logs/io.github.tyrantlucifer.amsftp`
- **Linux:** the `amsftp` subdirectory under your XDG config, state, cache, and
  runtime directories (logs live inside the AMSFTP state directory)
- **Custom private data directory:** `config`, `state`, and `cache` under that
  directory

Next: [User guide](https://github.com/TyrantLucifer/awesome-sftp-cli/blob/main/docs/user/guide.md).
