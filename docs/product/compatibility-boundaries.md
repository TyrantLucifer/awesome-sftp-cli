# Compatibility Boundaries

This is the frozen AMSFTP 1.0 public version inventory. The owning package remains the sole runtime decision point; this registry and its exact snapshot test make drift visible before migration or compatibility code changes. `Reads` lists formats accepted directly or through the explicit migration path.

| Boundary | Current | Reads | Writes | Unsupported/newer behavior | Owner |
|---|---:|---:|---:|---|---|
| cli contract | 1 | 1 | 1 | unknown command or output version rejected | `internal/app` |
| config document | 1 | 1 | 1 | newer schema rejected before use | `internal/config` |
| config effective output | 1 | 1 | 1 | unknown output version rejected by consumers | `internal/config` |
| workspace document | 2 | 1-2 | 2 | newer schema rejected before write | `internal/workspace` |
| sqlite state | 4 | 1-4 | 4 | newer head rejected before runtime write | `internal/state/migration` |
| cache filesystem manifest | 1 | 1 | 1 | unknown format rejected before content use | `internal/cachefs` |
| client-daemon IPC | 1.0 | 1.0 | 1.0 | no shared major/minor fails handshake | `internal/ipc` |
| helper release manifest | 1 | 1 | release-only | unknown header rejected before install | `internal/helper` |
| helper state index | 2 | 1-2 | 2 | unknown or newer schema rejected before mutation | `internal/helper` |
| helper wire envelope | 1 | 1 | 1 | unknown envelope rejected before dispatch | `internal/helper` |

The SQLite `1-4` read range includes forward migration to head 4; it is not a promise that arbitrary historical or newer databases are writable. Cache catalog tables were introduced by SQLite schema 2, while cache filesystem manifests remain an independently validated format 1. Helper state v1 is atomically migrated to v2 before mutation; v2 retains parallel exact artifacts, one active selection, the monotonic high-water, and at most one durable removal claim. Helper release-manifest writes are release-only and production distribution remains CLOSED until the protected signing/notarization/byte-binding gates pass.

Daemon negotiation is connection-scoped and precedes runtime state: a protocol-incompatible client fails before session allocation. Rejecting that client does not replace the daemon or its control socket, and compatible clients already connected continue on their negotiated protocol. CLI startup is fail-closed as well: only a successful absence result may invoke the daemon starter. That result includes the narrow residual case where a validated private Unix Socket has no live instance lock; status reports stopped without mutation, and only the daemon process after it acquires the matching instance lock may revalidate and remove the residual Socket before binding. An unhealthy, unauthenticated, or incompatible live Socket is preserved for explicit diagnosis and verified shutdown; a regular file, symbolic link, unsafe Socket, or held lock is never classified as residual.

## Frozen historical source inventory

This inventory freezes the complete set of persistent source formats before M6.2 migration code changes. Every row is captured as repository bytes pinned by SHA-256 and exercised by a current-owner reader test. Provenance names the commit that first wrote the source format, except the config sample which intentionally comes from the frozen exact-main baseline.

| Source | Version | Provenance commit | Capture status | Fixture | SHA-256 | Owner |
|---|---:|---|---|---|---|---|
| config document | 1 | `312bcccbcbd54246bbe5ff9babf4f14560449176` | captured | `internal/compatibility/testdata/historical/config-v1-exact-main.json` | `8c7c60ffcb676a47669b45fbb01334dde662984d6fdfcf5a25983d226cf24e04` | `internal/config` |
| workspace document | 1 | `e07413d46f516f8b0f92c61d006927c1aa319f0f` | captured | `internal/compatibility/testdata/historical/workspace-v1-stage1.json` | `9b8b085174b455805cd38a899702cad1363e6b1cf19a4bc98b5b715ebf9c8220` | `internal/workspace` |
| workspace document | 2 | `8bbb0f144583bbff10746ebdb22f82f86b4655e6` | captured | `internal/compatibility/testdata/historical/workspace-v2-stage3.json` | `1f137d8470e2d005d1672df39fb3c8bf6c7107b766ce9b62d3581c92680cdd40` | `internal/workspace` |
| sqlite state | 1 | `486a63f90be51c0d79a454bef52e9e3302df5250` | captured | `internal/compatibility/testdata/historical/sqlite-v1-stage2.sqlite` | `51f218895205098523be59d6ce58ac87d93d5f61746caae3c9c4e01ed18ce080` | `internal/state/migration` |
| sqlite state | 2 | `4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9` | captured | `internal/compatibility/testdata/historical/sqlite-v2-stage3.sqlite` | `d3f5fb72368d0d6b0aa82c9dca19f883c6acfda36050e028ac98822cffd489de` | `internal/state/migration` |
| sqlite state | 3 | `939ba9c5d978b8ea5bf1ae060ff62a0769d0d6c0` | captured | `internal/compatibility/testdata/historical/sqlite-v3-stage3.sqlite` | `deabc90d2f3699eb10c520f71fbc691e10eac65153bd1a3c2ae3f78fe41213cf` | `internal/state/migration` |
| sqlite verified backup | 1 | `4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9` | captured | `internal/compatibility/testdata/historical/sqlite-backup-v1-stage3.sqlite` | `0cc96fdafb32ab94d7d3dcef8fb4225ba67df5d504482c5e10b0a79a1cd2c3bb` | `internal/statefs` |
| sqlite verified backup | 2 | `939ba9c5d978b8ea5bf1ae060ff62a0769d0d6c0` | captured | `internal/compatibility/testdata/historical/sqlite-backup-v2-stage3.sqlite` | `4baec16549566416c959fe5b75f85b7e0c94cfff069a93c59fdca422eba079c2` | `internal/statefs` |
| cache entry manifest | 1 | `8a4ada06836b9ed71c72b40949d6b87d8e1f849a` | captured | `internal/compatibility/testdata/historical/cache-entry-manifest-v1-stage3.json` | `9979ce7f860182d4553c482a91c05e2d30bc81a540cc0188351ee068781ff1e0` | `internal/cachefs` |
| cache materialization manifest | 1 | `8a4ada06836b9ed71c72b40949d6b87d8e1f849a` | captured | `internal/compatibility/testdata/historical/cache-materialization-manifest-v1-stage3.json` | `b2992fbc5fe52198d1738ac42d3dd165289632e2103e1f760d2652f058a5272c` | `internal/cachefs` |
| helper release manifest | 1 | `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | captured | `internal/compatibility/testdata/historical/helper-release-manifest-v1-stage4.txt` | `fdaa89f1dc9fa60458b8cec81f19dfd3c028fee21f056b1c0f4650fcf4556c6f` | `internal/helper` |
| helper state index | 1 | `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | captured | `internal/compatibility/testdata/historical/helper-state-index-v1-stage4.json` | `ed71e086bdd008dd959afa5b14b02a1713bd2b811ebc2d4be44027e4acdfb9fa` | `internal/helper` |
| helper metadata | 1 | `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | captured | `internal/compatibility/testdata/historical/helper-metadata-v1-stage4.json` | `8062c2e12178bd0e0f002f4e0901198cd2363e1317b21d199bf57dd34f1016b1` | `internal/helper` |
