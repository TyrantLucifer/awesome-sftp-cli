# Stage 2 Verification Record

- **Status**: In Progress
- **Updated**: 2026-07-16
- **Repository root**: `/Users/bytedance/Downloads/projects/awesome-mac-sftp`
- **Branch**: `codex/stage2-durable-transfers`
- **Stage 1 merge baseline**: commit `b99fca2f729a8445b20935c69eda52cfa6dbbd28`, tree `1cf952ea743992c685f6bf05a75de43ebe7499a8`
- **Baseline Hosted run**: [29468930350](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29468930350) — exact merge commit, successful
- **Current milestone**: M2.1 dependency intake; no schema or production database-open path yet

Stage 2 delivers durable transfers only. It must preserve Stage 1 local/SFTP browsing, authentication, workspace recovery and read-only diagnostics. If persistent state cannot be opened safely, all mutation routes remain disabled.

## Initial safety checkpoint

| Check | Result |
|---|---|
| `git status --short --branch` | PASS: clean `main`, tracking `origin/main` before branch creation |
| `git rev-parse HEAD` | PASS: `b99fca2f729a8445b20935c69eda52cfa6dbbd28` |
| `git rev-parse HEAD^{tree}` | PASS: `1cf952ea743992c685f6bf05a75de43ebe7499a8` |
| `git fetch --prune origin` | PASS: local `main` remains synchronized with `origin/main` |
| `gh run view 29468930350 --repo TyrantLucifer/awsome-sftp-cli ...` | PASS: `headSha` is the exact merge commit and all jobs concluded successfully |
| local/remote Stage 2 branch lookup | PASS: branch absent before creation; created once from the verified baseline |
| codebase-memory index | PASS: project index ready; no worktree changes detected at baseline |

No user change was overwritten. Existing ignored IDE and validation artifacts remain outside the candidate tree.

## Zero-gate dependency intake

**Status**: Complete

Admitted exact pins:

- `modernc.org/sqlite v1.53.0`: tag `v1.53.0`, commit `6b32d1ee965dfe59bf2e50baeb6f451b67d6a71e`, module sum `h1:20WG8N9q4ji/dEqGk4uiI0c6OPjSeLTNYGFCc3+7c1M=`, `go.mod` sum `h1:xoEpOIpGrgT48H5iiyt/YXPCZPEzlfmfFwtk8Lklw8s=`.
- Upstream-resolved `modernc.org/libc v1.73.4`: tag `v1.73.4`, commit `70624da7facfac5a8d2f9b70cba0288b68b5ad01`, module sum `h1:+ra4Ui8ngyt8HDcO1FTDPWlkAh6yOdaO2yAoh8MddQA=`, `go.mod` sum `h1:DXZ3eO8qMCNn2SnmTNCiC71nJ9Rcq3PsnpU6Vc4rWK8=`.

Both candidates are unretracted exact tags, require Go 1.25, and are BSD-3-Clause licensed. The SQLite tag embeds SQLite 3.53.2. The libc distribution also carries the upstream Go BSD and musl MIT notices. Release/tag metadata and the upstream SQLite `go.mod` were reviewed; the latter selects this exact libc version. No `replace`, `latest`, second SQLite driver, or loose modernc upgrade was introduced.

The selected additions are `github.com/dustin/go-humanize v1.0.1`, `github.com/google/pprof v0.0.0-20250317173921-a4b03ec1a45e`, `github.com/google/uuid v1.6.0`, `github.com/hashicorp/golang-lru/v2 v2.0.7`, `github.com/mattn/go-isatty v0.0.20`, `github.com/ncruces/go-strftime v1.0.0`, `github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec`, `modernc.org/cc/v4 v4.28.4`, `modernc.org/ccgo/v4 v4.34.4`, `modernc.org/fileutil v1.4.0`, `modernc.org/gc/v2 v2.6.5`, `modernc.org/gc/v3 v3.1.3`, `modernc.org/goabi0 v0.2.0`, `modernc.org/libc v1.73.4`, `modernc.org/mathutil v1.7.1`, `modernc.org/memory v1.11.0`, `modernc.org/opt v0.2.0`, `modernc.org/sortutil v1.2.1`, `modernc.org/sqlite v1.53.0`, `modernc.org/strutil v1.2.1`, and `modernc.org/token v1.1.0`. The reviewed `go.sum` delta is 49 additive lines. License obligations are compatible with the project: MIT/BSD/Apache-2.0 notices must be retained; HashiCorp LRU's MPL-2.0 file-level source and notice obligations apply if distributed; modernc libc/memory third-party notices must be preserved.

The source-contract test freezes the narrow behavior used by the future backup path: `NewBackup` creates the destination through `newConn`, URI `_pragma` values are applied before `sqlite3_backup_init`, repeated pragmas execute in encoded order, multi-statement `Exec` consumes SQL tails, and the upstream module continues to select libc v1.73.4. The native smoke proves SQLite 3.53.2 open, rollback, commit, a non-empty WAL frame, `Step(-1)`/`Commit` online backup, destination `checkpoint_fullfsync=1`, `fullfsync=1`, `synchronous=FULL`, and the committed row in the backup. It runs only against a temporary generic intake database; production state opening remains absent.

Evidence before any schema or production database open path:

- [x] Exact tag/commit, license, changelog/tag metadata, retraction, complete selected module graph and reviewed `go.sum` diff.
- [x] No replace, `latest`, second SQLite driver or loose upgrade; libc remains the upstream resolution.
- [x] `govulncheck`: zero reachable and zero imported-package findings. The only required-module-only finding is pre-existing GO-2026-5932 in uncalled/unimported `golang.org/x/crypto/openpgp`; it is not introduced or made applicable by this intake.
- [x] Go 1.26.5 and exact Go 1.25.12 compile/test/tidy/verify gates.
- [x] darwin/linux × amd64/arm64 `CGO_ENABLED=0` package and product builds.
- [x] Native macOS APFS open/transaction/WAL/online-backup smoke on Darwin arm64 with 58 GiB free.
- [x] Native Linux open/transaction/WAL/online-backup smoke on exact commit `6959a5ea58aa4a9f6601a10bd91da7164ec891ad`: Ubuntu 22.04 and 24.04 native jobs passed in [Hosted run 29470643854](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29470643854).
- [x] modernc v1.53.0 `NewBackup`, URI pragma ordering and narrow API source-contract check.

Local command ledger:

| Command | Result |
|---|---|
| `go test ./internal/state/sqlite` | PASS on Go 1.26.5, Darwin arm64/APFS |
| `GOTOOLCHAIN=go1.25.12 go test ./internal/state/sqlite` | PASS |
| `go test -race ./internal/state/sqlite` | PASS |
| four `CGO_ENABLED=0 GOOS={darwin,linux} GOARCH={arm64,amd64} go test -c ./internal/state/sqlite` commands | PASS |
| current and Go 1.25.12 root/tools `go mod tidy -diff` and `go mod verify` | PASS; all modules verified |
| `go list -m -retracted modernc.org/sqlite@v1.53.0 modernc.org/libc@v1.73.4` | PASS; neither version retracted |
| `go tool -modfile=tools/go.mod govulncheck -show=verbose ./...` | PASS; zero reachable/imported findings; one pre-existing required-module-only finding |
| `GOTOOLCHAIN=go1.25.12 make check` | PASS |
| `make lint` | PASS; zero issues |
| external-output `make ci` | PASS, including race, fuzz smoke, supply chain, actionlint and four product targets |

## Milestone ledger

### M2.1 — Persistent state-machine foundation

- **Status**: In Progress
- **Goal**: ADR-0008 state store, Version 1 schema, Job/step state machine, transactional events and deterministic restart recovery.
- **Current action**: implement pending-attempt/online-backup/restore-hold/retention and WAL-budget closeout around the completed Version 1 foundation.
- **Implemented foundation**: checksum-v1 golden and real-v1 digest; single-statement lexer/main-only admission; explicit `BEGIN IMMEDIATE` runner; exact runner/domain DDL; 24,495-byte whole-schema contract (`659edd23b5bc332b488a171c920815daffef6223ef2d3859215ba177c3d55e64`); APFS/ext4/XFS gate; raw identity; WAL/locking/full-sync probe; durable-intent/no-replace bootstrap; runtime validation; transactional Job/event store and conservative restart pause before bind.
- **Last green command**: current/Go 1.25.12 focused state/job suites and focused race; four-target statefs test compilation; repository `go test ./...`; `make lint` with zero issues.

### M2.2 — Single-file copy, conflict and commit

- **Status**: Not Started
- **Gate**: M2.1 complete locally and in Hosted native evidence.
- **Required MVP**: user-visible local and temporary-sshd single-file copy steps plus real execution evidence.

### M2.3 — Directory copy and dual-remote relay

- **Status**: Not Started
- **Gate**: M2.2 complete with final-name safety and recovery evidence.

### M2.4 — Move, rename, delete and recovery closeout

- **Status**: Not Started
- **Gate**: M2.3 complete with bounded directory/relay resource evidence.

## Failure and repair ledger

| Issue | Attempt and evidence | Repair | Result |
|---|---|---|---|
| Test-first dependency intake | Focused test failed because `modernc.org/sqlite` was not yet required. | Added only the two exact pins and the registration seam. | Expected RED then GREEN. |
| Intake lint | First run reported an unwrapped EOF comparison and untrusted file/tool paths. | Used `errors.Is` and documented trusted module-cache reads. | Advanced to one remaining issue. |
| Intake lint | Second run reported deprecated `runtime.GOROOT`. | Resolved the active Go binary with `exec.LookPath`. | Third run passed with zero issues. |
| Tools module check | A root-level `-modfile=tools/go.mod` tidy attempt incorrectly tried to resolve repository-internal imports. | Used the Makefile's correct `go -C tools` form; no file was changed by the failed command. | Both toolchains' tools tidy/verify checks passed. |
| Schema contract query | First runtime manifest attempt used unquoted `notnull` in the table-valued PRAGMA query. | Quoted the metadata column while keeping table names parameterized. | Contract suite passed on the second attempt. |
| State foundation lint | First lint run found wrapped-error, trusted-path annotation and one Darwin conversion issue. | Preserved causes with `%w`, documented exact validated paths and removed the redundant conversion. | Second lint run passed with zero issues. |
| Immutable max-page validation | First existing-state reopen expected connection-local `max_page_count` in an immutable reader. | Kept raw 8 GiB file-size identity plus bootstrap/runtime max-page readback; immutable validation no longer claims a writer-connection setting. | Initial bootstrap and existing reopen passed on the second attempt. |
| Foundation module hygiene | First dual-toolchain `make check` stopped at `go mod tidy -diff` because new statefs code directly imports the existing `x/sys v0.47.0` pin. | Moved the unchanged pin from indirect to direct with `go mod tidy`. | Current and exact Go 1.25.12 `make check` passed on the second attempt. |

No issue exceeded three attempts.

## Final gate ledger

The following remain open until exact-candidate evidence is recorded:

- [ ] `make docs-check`
- [ ] `make check`
- [ ] `make lint`
- [ ] `make supply-chain`
- [ ] `make ci`
- [ ] `go test -race ./...`
- [ ] `GOTOOLCHAIN=go1.25.12 make check`
- [ ] Stage 2 SQLite, migration, Provider mutation, Planner, Job, IPC/event, transfer, conflict, recovery, PTY and performance suites.
- [ ] Four-target build and reproducibility/provenance comparison.
- [ ] Native APFS/ext4/XFS, two-sshd, ProxyCommand, Kerberos, crash/fault, sparse-file/large-tree and secret/pollution evidence.
- [ ] Independent cold-start audit from `docs/README.md`.
- [ ] Exact final SHA Hosted CI green and PR Ready for review.
