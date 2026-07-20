# Public release packaging

The public packaging path is deterministic and credential-free. It does not sign binaries, notarize macOS artifacts, create a production Helper manifest, or open production Helper/Level 2. Those gates remain closed until the final protected release sequence.

Run the dedicated repository tool with an explicit manifest and a new output directory. It is intentionally outside the canonical `Makefile`: that file permits only its frozen build/test output variables and rejects new dynamic recipe inputs.

```sh
GOTOOLCHAIN=local go run ./internal/tools/releasepack \
  /absolute/path/to/release-manifest.json \
  /absolute/path/to/new-release-directory
```

The manifest itself may be named anywhere, but every referenced file must be a canonical relative path below the manifest's directory. Symlinks, empty files, files outside that directory, unknown JSON fields, duplicate targets, duplicate binary paths, missing dependency license declarations, and existing output directories are rejected. Each input binary is inspected as a Go executable: its main package must be the AMSFTP command, GOOS/GOARCH must match the manifest target, CGO must be disabled, trimpath must be enabled, the embedded VCS revision must equal the manifest commit, and a dirty build is rejected.

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
    "uninstall": "release-input/UNINSTALL.md",
    "man": "release-input/amsftp.1"
  },
  "platforms": [
    {"os": "darwin", "arch": "amd64", "path": "dist/amsftp-darwin-amd64"},
    {"os": "darwin", "arch": "arm64", "path": "dist/amsftp-darwin-arm64"},
    {"os": "linux", "arch": "amd64", "path": "dist/amsftp-linux-amd64"},
    {"os": "linux", "arch": "arm64", "path": "dist/amsftp-linux-arm64"}
  ],
  "modules": [
    {"path": "example.invalid/replace-with-a-real-module", "version": "v0.0.0", "sum": "h1:replace-with-the-real-go-sum", "license": "replace-with-a-reviewed-SPDX-expression", "targets": [{"os":"darwin","arch":"amd64"}]}
  ]
}
```

The example dependency row is deliberately not release-ready. A real manifest must contain the complete resolved build dependency inventory with reviewed SPDX license expressions and Go module sums. The project owner selected Apache License 2.0; the committed root [LICENSE](../../LICENSE) and one-line [project-license.spdx](project-license.spdx) are the protected public-preview inputs. Packaging still requires the explicit non-empty LF-terminated license material and fails closed on missing or drifted input.

The current linked-module declaration is [runtime-dependencies.json](runtime-dependencies.json). Every module explicitly names the release targets where it must be linked; the packer filters that union and compares the resulting module/replacement set against each of the four binaries. An omitted target, extra target, missing row, unexpected row, version drift, sum drift, or replacement drift rejects the bundle. Module sums must be canonical `h1:` SHA-256 values, and `NONE`, `NOASSERTION`, malformed, or control-bearing license expressions are rejected. This declaration is an SBOM input and version-drift gate.

The exact redistribution-source inventory is [license-materials.json](license-materials.json), and its deterministic archive-facing output is [NOTICE](NOTICE). The inventory covers every one of the 20 declared runtime modules exactly once, preserves the resolved immutable `github.com/TyrantLucifer/sftp` replacement identity for `github.com/pkg/sftp`, and SHA-256-binds every included source file. In addition to each resolved module's root `LICENSE`, it includes `modernc.org/libc`'s complete `LICENSE-3RD-PARTY.md` and `modernc.org/memory`'s `LICENSE-GO` and `LICENSE-MMAP-GO` redistribution texts. `make notice-check` resolves the actual module directories through the Go module graph, rejects missing/extra/duplicate/drifted modules, unsafe or symlinked sources, and digest or byte changes, then requires byte identity with the committed NOTICE. This technical closure does not choose the AMSFTP project license or replace owner/legal review of that separate decision.

The maintained archive-facing instructions are [INSTALL.md](INSTALL.md) and [UNINSTALL.md](UNINSTALL.md). A release manifest may reference copied byte-identical versions of those files below its own confined input root.

## Exact outputs

The command creates one previously absent directory containing exactly:

- `amsftp_<version>_darwin_amd64.tar.gz`
- `amsftp_<version>_darwin_arm64.tar.gz`
- `amsftp_<version>_linux_amd64.tar.gz`
- `amsftp_<version>_linux_arm64.tar.gz`
- `checksums.txt`
- `sbom.spdx.json`
- `provenance.input.json`

Each archive has one canonical root directory containing `amsftp`, `VERSION.json`, `LICENSE`, `NOTICE`, `INSTALL.md`, `UNINSTALL.md`, and `share/man/man1/amsftp.1`. Timestamps use `source_date_epoch`; ownership, modes, gzip metadata, entry order, checksum order, SPDX package order, and provenance archive order are deterministic. The provenance file is an unsigned input, not an attestation.

`VERSION.json` also freezes the ADR-0009 identifiers: application/package ID `io.github.tyrantlucifer.amsftp`, launchd label `io.github.tyrantlucifer.amsftp.daemon`, systemd user unit `amsftp-daemon.service`, and Homebrew formula `amsftp`. CI builds all four clean public-preview binaries, regenerates and byte-compares the committed third-party NOTICE, packages that exact NOTICE and the same inputs twice, compares every output byte, verifies the archive checksums, extracts the native Linux archive into a clean home, checks version/commit and service identifiers, starts/statuses/stops the daemon, and removes only the isolated install root. CI-only LICENSE fixtures remain explicit non-release contract material; the protected tag workflow instead consumes the committed Apache-2.0 project license.

Public binaries use the `public_preview` state. Linux production admission separately requires frozen final unsigned bytes. Darwin production admission separately requires Developer ID Application identity, hardened runtime, trusted timestamp, strict verification, notarization `Accepted`, submission/CDHash/certificate evidence, and an Accepted-ZIP binary SHA-256 equal to the admitted binary. Passing public packaging never satisfies those production gates.

The release harness can render the `amsftp` Homebrew formula only after the caller supplies one reviewed project SPDX license identifier and the exact final four canonical archives. It emits one immutable GitHub Release URL and SHA-256 per macOS/Linux and ARM/Intel target, installs the binary and man page, generates bash/zsh/fish completion through `amsftp completion <shell>`, and runs a version smoke. Archive order does not affect the formula bytes. Prerelease versions, incomplete or duplicate targets, noncanonical names, empty bytes, compound-license DSL, symlinked archives, and input drift fail closed. Rendering does not itself publish a formula, make preview bytes final, create a service, or establish Helper trust.

## Public-preview channel automation

The protected [Public Preview Release workflow](../../.github/workflows/release.yml) accepts only an existing strict `vX.Y.Z` tag bound to the checked-out commit. It requires a non-empty repository `LICENSE` and a separate one-line `docs/release/project-license.spdx`, builds clean CGO-disabled binaries for the four canonical targets, runs the public packer, appends the checksum-bound `install.sh`, renders `amsftp.rb`, and uploads one verified workflow artifact. A tag-triggered publish job then creates the immutable GitHub Release with the protected `PUBLIC_RELEASE_TOKEN` and updates `TyrantLucifer/homebrew-tap` using the protected `HOMEBREW_TAP_TOKEN` secret. A rerun accepts an existing Release only after every asset is byte-identical and treats an already-current formula as success, so a tap-side interruption is recoverable without replacing release bytes. Repository workflow permissions remain read-only.

The repository contains the owner-approved Apache-2.0 license and no credential material. The tap repository, protected environment, tag policy, and both environment secrets are configured externally; the first strict tag remains the final irreversible publication trigger. The workflow publishes an unsigned public preview, not a signed/notarized final release or public 1.0. Production Helper and Level 2 remain CLOSED.

## Helper release sidecars and runtime admission

For each canonical archive `amsftp_<version>_<os>_<arch>.tar.gz`, the protected final release must also publish `amsftp_<version>_<os>_<arch>.helper-manifest` and `amsftp_<version>_<os>_<arch>.helper-manifest.sig` under the same immutable `v<version>` GitHub Release. Runtime resolution accepts only the four frozen targets and canonical release versions. It reads the bounded manifest and detached signature first, verifies current Ed25519 trust/revocation/deny/floor/client policy, and matches the signed version/target before any archive request.

Archive acquisition is deliberately lazy: rejecting preliminary consent, binding probe, target, or high-water performs zero archive reads. After those checks, each Installer validation/upload reopen independently downloads through bounded official GitHub HTTPS release-asset redirects, enforces compressed and decompressed hard limits, requires the exact deterministic USTAR entry set, and streams the exact root `amsftp` into an immediately unlinked `0600` regular staging file. Installer expected+1/size/SHA-256 checks bind every reopen to the signed manifest. There is no arbitrary URL/path input, and Homebrew/TLS remains transport rather than a trust root. Production keys/floors, protected final bytes, manifest generation/signing, daemon/SFTP lifecycle composition, and public mutation remain CLOSED.
