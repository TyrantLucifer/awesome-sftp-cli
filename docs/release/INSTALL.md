# Install AMSFTP

These instructions cover the immutable tar archives. A published release must be downloaded together with `checksums.txt`, `sbom.spdx.json`, provenance/attestation material, and release notes from the same release.

## Quick install for an X.Y.Z public preview

After the first strict `X.Y.Z` public preview is published, install the latest release without `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL \
  https://github.com/TyrantLucifer/awesome-sftp-cli/releases/latest/download/install.sh | sh
```

The script supports macOS/Linux on ARM64 and AMD64. It resolves the immutable release, downloads the matching archive plus `checksums.txt`, requires one exact lowercase SHA-256 match, and uses that staged binary to preflight the executable plus config/state/cache paths before creating a target directory, stopping a daemon, or replacing a file. It then verifies the reported version, safely stops a proven running daemon during upgrade, preserves a real prior binary as `amsftp.previous`, regenerates man/completion files, and starts plus probes the exact new daemon version.

The default prefix is `$HOME/.local`. If that executable or its persistent paths contain a symlink, foreign-owned ancestor, unsafe mode/ACL, or unsupported filesystem trust boundary, the installer automatically uses an already provisioned `/var/lib/amsftp-users/<uid>` root. A managed root contains `bin/amsftp`, `config`, `state`, and `cache`; its owner-private `.amsftp-root` marker makes future commands and upgrades reuse the layout without XDG environment changes. The installer leaves an old unsafe prefix untouched and tells the user to place the managed `bin` first in `PATH`.

When the managed root does not exist, preflight exits before target mutation and prints the exact one-time administrator commands. Their general form is:

```sh
sudo install -d -o root -g root -m 0755 /var/lib/amsftp-users
sudo install -d -o "$USER" -g "$(id -gn)" -m 0700 \
  "/var/lib/amsftp-users/$(id -u)"
```

Rerun the original installer afterward; automatic discovery then completes installation. Use `--root /absolute/path` for another pre-provisioned owner-private managed root, `--prefix /absolute/path` for a conventional layout that independently passes preflight, `--version X.Y.Z` for an exact release, or `--no-start-daemon` when needed. The installer never invokes `sudo`, changes a shared mount owner, widens permissions, or changes Gatekeeper policy.

Homebrew provides the same four immutable archives:

```sh
brew install TyrantLucifer/tap/amsftp
```

After either supported installation, use `amsftp upgrade` for normal updates. The command detects Homebrew versus the exact `<prefix>/bin/amsftp` standalone layout, checks the latest strict version before stopping anything, refuses active-Job interruption, and restores only a daemon that was previously running. Direct installer and `brew upgrade` commands remain documented recovery paths.

The currently published owner-only internal tag is not a strict public-preview channel and remains a manual checksum-verified install.

## Manual archive install

1. Select the archive that exactly matches the operating system and architecture: `darwin_amd64`, `darwin_arm64`, `linux_amd64`, or `linux_arm64`.
2. Verify the archive SHA-256 against `checksums.txt` before extraction. On macOS, the final release instructions also require the published Developer ID/notarization identity; a checksum alone is not a signing proof.
3. Extract the archive into a new directory. Do not merge it with a previous extraction.
4. Copy `amsftp` to one trusted directory already under the user's control, such as `$HOME/.local/bin`, and keep mode `0755`. Copy `share/man/man1/amsftp.1` to the matching user man directory, such as `$HOME/.local/share/man/man1/amsftp.1`, with mode `0644`. Do not run AMSFTP with `sudo`, setuid, or from a writable PATH directory.
5. Generate completion directly from that exact installed binary: `amsftp completion bash`, `amsftp completion zsh`, or `amsftp completion fish`. Store only the selected script in the shell's user completion directory; regeneration after upgrade prevents binary/completion drift. The generated script completes saved names only after `--workspace` by invoking the same installed binary's bounded read-only workspace query; it does not start the daemon or create a missing state directory.
6. Run `amsftp --version`, then `amsftp daemon start --format json` and `amsftp daemon status --format json`. The reported version/commit must match `VERSION.json` and the release record.

For an unsupported custom layout or a manual in-place binary upgrade, follow the complete [upgrade and rollback procedure](UPGRADE.md): stop the daemon, extract the new archive into a separate directory, verify its checksum/signing evidence and version metadata, and replace only the installed binary, man page, and generated completion files. Start the new daemon and confirm Jobs/state health before deleting the previous extraction. An older binary encountering a newer database must not be used for mutation; retain the state and use the current binary or the documented read-only diagnosis/restore path.

The application/package ID is `io.github.tyrantlucifer.amsftp`. If a final channel installs a user service, its only accepted identifiers are launchd label `io.github.tyrantlucifer.amsftp.daemon` and systemd user unit `amsftp-daemon.service`. The Homebrew formula name is `amsftp`. A public preview archive and formula do not claim that protected signing/notarization is complete.

The first run creates only owner-private configuration, state, log, cache, and runtime paths. AMSFTP does not install a privileged service, setuid binary, kernel component, system SSH replacement, production Helper credential, or production Level 2 credential.
