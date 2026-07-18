# Install AMSFTP

These instructions cover the immutable tar archives. A published release must be downloaded together with `checksums.txt`, `sbom.spdx.json`, provenance/attestation material, and release notes from the same release.

1. Select the archive that exactly matches the operating system and architecture: `darwin_amd64`, `darwin_arm64`, `linux_amd64`, or `linux_arm64`.
2. Verify the archive SHA-256 against `checksums.txt` before extraction. On macOS, the final release instructions also require the published Developer ID/notarization identity; a checksum alone is not a signing proof.
3. Extract the archive into a new directory. Do not merge it with a previous extraction.
4. Copy `amsftp` to one trusted directory already under the user's control, such as `$HOME/.local/bin`, and keep mode `0755`. Do not run it with `sudo`, setuid, or a writable PATH directory.
5. Run `amsftp --version`, then `amsftp daemon start --format json` and `amsftp daemon status --format json`. The reported version/commit must match `VERSION.json` and the release record.

The application/package ID is `io.github.tyrantlucifer.amsftp`. If a final channel installs a user service, its only accepted identifiers are launchd label `io.github.tyrantlucifer.amsftp.daemon` and systemd user unit `amsftp-daemon.service`. The Homebrew formula name is `amsftp`. A public preview archive does not claim that protected signing/notarization or channel installation is complete.

The first run creates only owner-private configuration, state, log, cache, and runtime paths. AMSFTP does not install a privileged service, setuid binary, kernel component, system SSH replacement, production Helper credential, or production Level 2 credential.
