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
