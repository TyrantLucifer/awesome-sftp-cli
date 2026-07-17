# Compatibility Boundaries

This is the frozen AMSFTP 1.0 public version inventory. The owning package remains the sole runtime decision point; this registry and its exact snapshot test make drift visible before migration or compatibility code changes. `Reads` lists formats accepted directly or through the explicit migration path.

| Boundary | Current | Reads | Writes | Unsupported/newer behavior | Owner |
|---|---:|---:|---:|---|---|
| cli contract | 1 | 1 | 1 | unknown command or output version rejected | `internal/app` |
| config document | 1 | 1 | 1 | newer schema rejected before use | `internal/config` |
| config effective output | 1 | 1 | 1 | unknown output version rejected by consumers | `internal/config` |
| workspace document | 2 | 1-2 | 2 | newer schema rejected before write | `internal/workspace` |
| sqlite state | 3 | 1-3 | 3 | newer head rejected before runtime write | `internal/state/migration` |
| cache filesystem manifest | 1 | 1 | 1 | unknown format rejected before content use | `internal/cachefs` |
| client-daemon IPC | 1.0 | 1.0 | 1.0 | no shared major/minor fails handshake | `internal/ipc` |
| helper release manifest | 1 | 1 | release-only | unknown header rejected before install | `internal/helper` |
| helper wire envelope | 1 | 1 | 1 | unknown envelope rejected before dispatch | `internal/helper` |

The SQLite `1-3` read range includes forward migration to head 3; it is not a promise that arbitrary historical or newer databases are writable. Cache catalog tables were introduced by SQLite schema 2, while cache filesystem manifests remain an independently validated format 1. Helper release-manifest writes are release-only and production distribution remains CLOSED until the protected signing/notarization/byte-binding gates pass.

## Frozen historical source inventory

This inventory freezes the complete set of persistent source formats before M6.2 migration code changes. A `captured` row has repository bytes pinned by SHA-256 and a current-owner reader test. Every remaining row must become `captured` before its owner is changed in M6.2; a planned path is not migration evidence. Provenance names the commit that first wrote the source format, except the config sample which intentionally comes from the frozen exact-main baseline.

| Source | Version | Provenance commit | Capture status | Fixture | SHA-256 | Owner |
|---|---:|---|---|---|---|---|
| config document | 1 | `312bcccbcbd54246bbe5ff9babf4f14560449176` | captured | `internal/compatibility/testdata/historical/config-v1-exact-main.json` | `8c7c60ffcb676a47669b45fbb01334dde662984d6fdfcf5a25983d226cf24e04` | `internal/config` |
| workspace document | 1 | `e07413d46f516f8b0f92c61d006927c1aa319f0f` | captured | `internal/compatibility/testdata/historical/workspace-v1-stage1.json` | `9b8b085174b455805cd38a899702cad1363e6b1cf19a4bc98b5b715ebf9c8220` | `internal/workspace` |
| workspace document | 2 | `8bbb0f144583bbff10746ebdb22f82f86b4655e6` | captured | `internal/compatibility/testdata/historical/workspace-v2-stage3.json` | `1f137d8470e2d005d1672df39fb3c8bf6c7107b766ce9b62d3581c92680cdd40` | `internal/workspace` |
| sqlite state | 1 | `486a63f90be51c0d79a454bef52e9e3302df5250` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/sqlite-v1-stage2.sqlite` | `-` | `internal/state/migration` |
| sqlite state | 2 | `4eb1961b7b3b5495620fb1f6fcb3b88c52a4fba9` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/sqlite-v2-stage3.sqlite` | `-` | `internal/state/migration` |
| sqlite state | 3 | `939ba9c5d978b8ea5bf2aadd3485831d78b533c2e` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/sqlite-v3-stage3.sqlite` | `-` | `internal/state/migration` |
| cache entry manifest | 1 | `8a4ada06836b9ed71c72b40949d6b87d8e1f849a` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/cache-entry-manifest-v1-stage3.json` | `-` | `internal/cachefs` |
| cache materialization manifest | 1 | `8a4ada06836b9ed71c72b40949d6b87d8e1f849a` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/cache-materialization-manifest-v1-stage3.json` | `-` | `internal/cachefs` |
| helper release manifest | 1 | `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/helper-release-manifest-v1-stage4.txt` | `-` | `internal/helper` |
| helper state index | 1 | `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/helper-state-index-v1-stage4.json` | `-` | `internal/helper` |
| helper metadata | 1 | `145b50ae871aa91f8acc0505d2b6b9bd19bae742` | capture-required-before-M6.2-mutation | `internal/compatibility/testdata/historical/helper-metadata-v1-stage4.json` | `-` | `internal/helper` |
