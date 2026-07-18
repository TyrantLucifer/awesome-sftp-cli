# Public release packaging

The public packaging path is deterministic and credential-free. It does not sign binaries, notarize macOS artifacts, create a production Helper manifest, or open production Helper/Level 2. Those gates remain closed until the final protected release sequence.

Run the dedicated repository tool with an explicit manifest and a new output directory. It is intentionally outside the canonical `Makefile`: that file permits only its frozen build/test output variables and rejects new dynamic recipe inputs.

```sh
GOTOOLCHAIN=local go run ./internal/tools/releasepack \
  /absolute/path/to/release-manifest.json \
  /absolute/path/to/new-release-directory
```

The manifest itself may be named anywhere, but every referenced file must be a canonical relative path below the manifest's directory. Symlinks, empty files, files outside that directory, unknown JSON fields, duplicate targets, duplicate binary paths, missing dependency license declarations, and existing output directories are rejected.

## Manifest v1

```json
{
  "schema": "amsftp-public-release-manifest-v1",
  "version": "1.0.0",
  "commit": "<40 lowercase hexadecimal characters>",
  "tree": "<40 lowercase hexadecimal characters>",
  "source_date_epoch": 0,
  "materials": {
    "license": "LICENSE",
    "notice": "release-input/NOTICE",
    "install": "release-input/INSTALL.md",
    "uninstall": "release-input/UNINSTALL.md"
  },
  "platforms": [
    {"os": "darwin", "arch": "amd64", "path": "dist/amsftp-darwin-amd64"},
    {"os": "darwin", "arch": "arm64", "path": "dist/amsftp-darwin-arm64"},
    {"os": "linux", "arch": "amd64", "path": "dist/amsftp-linux-amd64"},
    {"os": "linux", "arch": "arm64", "path": "dist/amsftp-linux-arm64"}
  ],
  "modules": [
    {"path": "example.invalid/replace-with-a-real-module", "version": "v0.0.0", "sum": "h1:replace-with-the-real-go-sum", "license": "replace-with-a-reviewed-SPDX-expression"}
  ]
}
```

The example dependency row is deliberately not release-ready. A real manifest must contain the complete resolved build dependency inventory with reviewed SPDX license expressions and Go module sums. The repository currently has no project `LICENSE`; selecting one is a project-owner legal decision. Packaging therefore requires an explicit non-empty LF-terminated license input and fails closed until the owner supplies it.

## Exact outputs

The command creates one previously absent directory containing exactly:

- `amsftp_<version>_darwin_amd64.tar.gz`
- `amsftp_<version>_darwin_arm64.tar.gz`
- `amsftp_<version>_linux_amd64.tar.gz`
- `amsftp_<version>_linux_arm64.tar.gz`
- `checksums.txt`
- `sbom.spdx.json`
- `provenance.input.json`

Each archive has one canonical root directory and only `amsftp`, `VERSION.json`, `LICENSE`, `NOTICE`, `INSTALL.md`, and `UNINSTALL.md`. Timestamps use `source_date_epoch`; ownership, modes, gzip metadata, entry order, checksum order, SPDX package order, and provenance archive order are deterministic. The provenance file is an unsigned input, not an attestation.

Public binaries use the `public_preview` state. Linux production admission separately requires frozen final unsigned bytes. Darwin production admission separately requires Developer ID Application identity, hardened runtime, trusted timestamp, strict verification, notarization `Accepted`, submission/CDHash/certificate evidence, and an Accepted-ZIP binary SHA-256 equal to the admitted binary. Passing public packaging never satisfies those production gates.
