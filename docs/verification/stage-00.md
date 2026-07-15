# Stage 0 Verification Record

- **Status**: Complete
- **Updated**: 2026-07-15
- **Repository root**: `/Users/bytedance/Documents/Codex/2026-07-14/wo-x/work/stage0-foundation`
- **Branch**: `codex/stage0-foundation`
- **Foundation base**: `e14159cef930ffa513ebf6a9a5319f8f0fab1f9f`
- **Verified implementation commit/tree**: `cf8e6efd2814d835f8c1f5c2739608477b5216ed` / `e70a8f0c5fc57817f6fa44dda31faaf4652b67c5`
- **Staging at evidence capture**: empty

Stage 0 establishes and verifies foundation contracts and engineering gates only. It does not provide a usable TUI, daemon service, SSH/SFTP connection, SQLite persistence, transfer engine, or remote helper, and it is not production-ready. Production/release readiness is assessed only by the Stage 6 hardening and 1.0 release gates.

## Current checkpoint

The Go module, role/domain/IPC/Provider/Fake contracts, docscheck, Make/tooling, supply-chain checks and pinned workflows exist. Tasks1–11, independent reviews, two cold-start audits, the dual-toolchain local gate and pollution checks are complete. Hosted attempt 1 exposed a GNU Make 4.x safe-flag false positive and is superseded. Its focused regression and minimal fix pass locally and are bound to implementation commit `cf8e6ef…`, tree `e70a8f0…`. Replacement Hosted run [`29394698864`](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864) passed all 23 jobs and published the complete artifact/provenance set. CORE-001–008 are Verified; Stage 0 is Complete and Stage 1 is unlocked but Not Started.

At Task 11 review time, most new Stage 0 files were untracked relative to the base commit, so a plain `git diff` was not a complete review artifact. The review and closeout captures therefore used a temporary full-tree index, tracked patch, untracked-file manifest and content digest. The user-owned `.idea/` directory was classified as unrelated local IDE metadata, added to the root ignore policy, and preserved unchanged outside the product candidate.

## Final local closeout

The evidence-only closeout ran from the repository root on 2026-07-15 after both cold-start audits. Candidate and post-gate tree were `5d598eea00fac2b5580bc04596d2bb2c435f4799`; `make ci`, docscheck, artifact/hash checks, native smoke, candidate/status/untracked comparisons, ignored-tree/tar comparisons and empty-staging checks all passed. Evidence is preserved at `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-stage0-final.b5i0FK`.

- Candidate patch SHA-256: `71fae25ebd2f5ed249ad713329eab9b23d89079ce0d21df1b9b165fba3a1f40a`
- Candidate status SHA-256: `c61e2494e7ed2f6f6218460c8af6641a591e66acff047be1d0c6ea6a41987208`
- Untracked manifest: 132 files, SHA-256 `c96a92926ed273db3a50be2a55fcad0a1cf4402df700c6441e8a5e7237a89f3f`
- Ignored output: root `bin`, tree `3c5a3dc60df67f14e0b7d986305ca0985653ce7d`, tar SHA-256 `27c0df71bd43106c0c40ad85c451155a0ac6d350c660d5c93d7e96a7554f2a49`
- Closeout log SHA-256: `759f992f8adbc421c1e7a358176c026dfd7afba03ceb30e09c8b5ff1db2cd384`
- Artifact digest ledger SHA-256: `9f3a98186d33f047df7d1195d3bf8f419cbb7dde9beaa8471f37893e166d55f2`

Commit-readiness documentation is revalidated immediately before the authorized local commit. The exact resulting commit and `HEAD^{tree}` identities belong to Git and the handoff; hosted CI must bind mechanically to that committed tree rather than to a copied identifier in this file.

## Hosted implementation candidate attempt 1 — failed and superseded

[CI run 29394164471](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394164471) executed on 2026-07-15 for commit `1da725478aa772ebc408885427df23f3b9f4c53c`, tree `5880f05d52d618a9b128a37f6925467666fe7cc8`, on branch `codex/stage0-foundation`. All four native jobs, both macOS oldstable jobs, all four cross-build jobs, all eight independent-cache reproducibility builds and `reproducibility-compare` passed. `quality`, `oldstable-ubuntu-22.04` and `oldstable-ubuntu-24.04` failed in `make check`; final provenance `compare` was therefore skipped.

The three failures had one root cause: GNU Make 4.x propagates a safe `-I` include path containing a space and `n` as an `I/path` or `-I/path` word in `MAKEFLAGS`, while the existing guard treated that option word as a short-option cluster and reported a forbidden dry-run flag. macOS GNU Make 3.81 does not expose the option in that form, so the prior local gate could not reproduce it. The regression test `TestCanonicalMakefileClassifiesSyntheticPropagatedFlags` first reproduced both GNU Make 4.x forms, then the Make helper was narrowed to exempt only `I%`/`-I%` option words while preserving short/clustered/dash/long dry-run detection.

The replacement fix passed Go 1.26.5 `make ci` and exact Go 1.25.12 `make check` locally on frozen tree `e70a8f0c5fc57817f6fa44dda31faaf4652b67c5`, with evidence at `/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-hosted-fix-final.5yKPjp`. Log SHA-256 values are `9f210ed08fb4eabbcd7a305890d952efcce67d3d7a0b62ab042b7aa26ad857d2` (`make-ci.log`) and `b3d32bd9a99fb2092e652dbb5b2de3a32061ea4fe3b9ce35e39d049bbdbf04f9` (`oldstable-check.log`). Candidate/post tree and status were identical and native smoke passed.

## Hosted implementation candidate attempt 2 — passed

[CI run 29394698864](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864) executed on 2026-07-15 for exact commit `cf8e6efd2814d835f8c1f5c2739608477b5216ed`, tree `e70a8f0c5fc57817f6fa44dda31faaf4652b67c5`, on branch `codex/stage0-foundation`. All 23 jobs passed: quality; four native and four exact-oldstable platform jobs (`ubuntu-22.04`, `ubuntu-24.04`, `macos-15`, `macos-15-intel`); four target builds; eight independent-cache reproducibility builds; `reproducibility-compare`; and final [`compare`](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864/job/87285699309).

The run published 36 unexpired artifacts. The final compare downloaded 22 provenance artifacts plus the reproducibility comparison, verified the expected commit/tree/clean status and four accepted target hashes, then published [provenance-compare artifact 8334635589](https://github.com/TyrantLucifer/awsome-sftp-cli/actions/runs/29394698864/artifacts/8334635589), digest `sha256:5e0b1ce400c43c0156fa7c2ad1e4089e83c41708115de688a045f3678f337712`. The reproducibility comparison artifact `8334617155` has digest `sha256:e58475afb5741fd2b9a1d9608a3d8e17bf3c2807445acc8ee8670f870057da8b`; the quality provenance artifact `8334630055` has digest `sha256:03b5d893caedc45508902c26b4e765bb6e0f02a97625bac34d703e2fe5d96014`.

## Task 10 controller-run local checks

Run on native macOS 26.5.1 arm64 from the repository root on 2026-07-15. The single approved GNU Bash 3.2 fail-closed block used GNU Make 3.81 and external build/coverage paths containing spaces.

| Command | Result |
|---|---|
| Go 1.26.5 `make check` | PASS: fmt, vet, unit coverage, Provider contract, docscheck, both-module `tidy -diff` and `verify` |
| Go 1.26.5 `make lint` | PASS: golangci-lint v2.12.2, 0 issues |
| Go 1.26.5 `make supply-chain` | PASS: govulncheck reported no vulnerabilities; actionlint passed both workflows |
| Go 1.26.5 `make ci` | PASS: check, lint, full race, four named fuzz smoke targets, supply-chain, and four builds |
| `make build-all` plus artifact inspection | PASS: exactly four regular non-symlink artifacts; SHA-256 and GOOS/GOARCH/CGO/trimpath metadata matched |
| darwin/arm64 artifact `--help` and `--version` | PASS: role boundary executed natively |
| exact Go 1.25.12 `make check` with `GOTOOLCHAIN=local` | PASS |
| complete candidate and ignored-output pre/post snapshots | PASS: candidate tree `7fe3a08b9830ef887bda03692c4ee13944bbe4db`; ignored tree `3c5a3dc60df67f14e0b7d986305ca0985653ce7d`; ignored tar SHA-256 `27c0df71bd43106c0c40ad85c451155a0ac6d350c660d5c93d7e96a7554f2a49` |
| official `git ls-remote --tags --refs` checks for checkout/setup-go/upload-artifact/download-artifact | PASS: all approved tag/SHA pairs matched on 2026-07-15 |
| independent Task 10 reviews | PASS: build/tools, workflows after remediation, lint remediation, and final docscheck all zero findings; 14 docscheck bypasses replayed fail-closed |
| staging before and after | PASS: empty |

The candidate tree above is only the Task10 snapshot. Task11 architecture/tooling edits changed the complete tree; all relevant checks and hashes must be rerun, and no prior candidate identifier may be attached to the eventual Stage0 candidate.

## Task 11 focused revalidation

| Scope | Result |
|---|---|
| Decision truth chain | PASS: SQLite, OpenSSH/remote command, editor/opener, Helper consent/distribution and release evidence contracts were synchronized; final cross-document review reported no High or Medium findings. |
| GNU Make contract | PASS after all review deltas: `go test -count=1 ./internal/tools/makecontract` and canonical `make-contract` passed. Independent GNU Make 3.81 audits rejected late/continued/EOF execution flags, target-specific recipe controls, internal guard command-line assignments, dual `MAKEFLAGS`/`MFLAGS` input hiding and `-e` environment overrides; they verified 14 exact forced first guards, all 11 Go probes and safe command-line output-directory assignments. |
| Workflow provenance | PASS after multiple review cycles: focused provenance/whitespace/workload tests, `go test -count=1 ./internal/docscheck`, `go run ./internal/tools/docscheck .`, and pinned actionlint passed. Independent reviews verified exact shell whitespace, disabled VCS stamping for cross/repro builds, actual SHA/sidecar and target tuples, accepted comparison evidence, CI build + repro A/B and nightly repro A/B bindings, plus exact nightly fuzz/concurrency workload producer profiles. |
| IPC control boundary | PASS after core-security review: decode and encode reject invalid UTF-8 envelope/control data without U+FFFD normalization; raw JSON Payload is strict UTF-8; unknown Code/Retry/Effect and negative retry delays fail closed; error Location continues to carry original base64 diagnostic path bytes. Focused/full IPC tests, race and vet passed. |
| Diff and staging | PASS: `gofmt -d`, `git diff --check`, cached diff checks and an empty staging area were confirmed after the focused fixes. No commit, push, remote CI or normal full `make ci` was performed. |

Diagnostic work was kept separate from acceptance evidence. The unavailable codebase-memory MCP returned `Transport closed`, so code discovery fell back to compiler/import and targeted source inspection. One controller zsh probe used the reserved variable `status` and was rerun with `rc`; one hand-written continued-`MAKEFLAGS` awk probe had invalid quoting and was rerun with the corrected command, which exited 2 with the expected guard diagnostic. GNU Make toy probes transiently created empty untracked `probe`/`all` files; both were removed with `apply_patch` and the final Make review confirmed neither remained. These exploratory failures are not counted as green gates.

## Superseded Task 11 candidate and gate attempt

The first post-remediation complete-worktree capture is retained only as failed/superseded evidence:

- Candidate tree: `d30a1f422d9852c1d308fe78dd17425a01684d47`
- Complete patch SHA-256: `fb90e13e3170b3016c6917cfef36747e19e36f77adc5a98f46a3f8c3616acb1e`
- Status SHA-256: `c61e2494e7ed2f6f6218460c8af6641a591e66acff047be1d0c6ea6a41987208`
- NUL untracked inventory: 132 entries, SHA-256 `c96a92926ed273db3a50be2a55fcad0a1cf4402df700c6441e8a5e7237a89f3f`
- Evidence path: `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-task11.Ty8b5v`
- Ignored baseline: only `bin/`; tree `3c5a3dc60df67f14e0b7d986305ca0985653ce7d`, tar SHA-256 `27c0df71bd43106c0c40ad85c451155a0ac6d350c660d5c93d7e96a7554f2a49`
- Shared staging remained empty; `.idea/` and `.superpowers/` were excluded from candidate content.

The first `/bin/bash` 3.2 full-gate block recorded macOS 26.5.1 arm64, GNU Make 3.81 and Go 1.26.5, then stopped before any test at `Makefile:88` with `execution-skipping Make flags n/i/q/t and their long forms are not permitted` (exit 2). The cause was a false positive on the documented command-line `BUILD_DIR`/`COVERAGE_DIR` assignments; no partial output is treated as green. The old frozen-tree review independently found that nightly fuzz/concurrency could be replaced by `run: ":"`, while the core review found strict UTF-8, canonical error enum and retry-delay gaps. TDD fixes and independent delta re-reviews now pass, so all identifiers in this subsection remain permanently superseded and cannot be reused for the replacement candidate.

## Second superseded candidate and lint gate

The replacement capture was also invalidated and is retained only as failed/superseded evidence:

- Candidate tree: `6f33b11a2c87116155019c96a5ad84c0632aa255`
- Complete patch SHA-256: `1e4ca78ee3c0e8bd209710c7073e33069f0a8363e9045cebaf9c6c0b1fc3bf45`
- Status SHA-256: `c61e2494e7ed2f6f6218460c8af6641a591e66acff047be1d0c6ea6a41987208`
- NUL untracked inventory: 132 entries, SHA-256 `c96a92926ed273db3a50be2a55fcad0a1cf4402df700c6441e8a5e7237a89f3f`
- Evidence path: `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-task11-r2.dzyxRs`
- Ignored baseline: only `bin/`; tree `3c5a3dc60df67f14e0b7d986305ca0985653ce7d`, tar SHA-256 `27c0df71bd43106c0c40ad85c451155a0ac6d350c660d5c93d7e96a7554f2a49`

Its Go 1.26.5 `/bin/bash` 3.2 gate passed canonical Make verification, format, vet, all-package coverage tests, explicit Provider contract, repository docscheck, both modules' `tidy -diff` and `verify`, then stopped at golangci-lint (exit 2) with seven gosec diagnostics on test-controlled repository/temp paths and constant no-shell `make` subprocesses plus two ST1005 lowercase error-string diagnostics. Race/fuzz/supply-chain/build, repeated race, oldstable and pollution comparison did not run and are not inferred. The fixes add only line-scoped, justified test suppressions and lowercase the two verifier error prefixes; package tests and full golangci-lint now pass with `0 issues`, and an independent delta review found no suppression of production/external-input handling. The frozen-tree reviewer found no additional issue but explicitly marked this tree superseded. A third capture and complete gate are required.

## Third superseded candidate: green gate, review failure

The required third complete-worktree capture was frozen and its local gate completed, but the subsequent whole-tree review found a blocking contract bypass. It is retained as exact historical evidence and is not the candidate eligible for hosted CI:

- Candidate and post-gate tree: `4b60326802ea186cb93a3dcab499b9c4fc53f5e1`
- Complete patch SHA-256: `a53a95b829f8e340bd0d767709787adfd0a37b35e0ec47e00b6c3c9621af47d2`
- Status SHA-256: `c61e2494e7ed2f6f6218460c8af6641a591e66acff047be1d0c6ea6a41987208`
- NUL untracked inventory: 132 entries, SHA-256 `c96a92926ed273db3a50be2a55fcad0a1cf4402df700c6441e8a5e7237a89f3f`
- Evidence path: `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-task11-r3.dJS9Jt`
- Gate log SHA-256: `f0ff7640c27ba6e6a056c30683329595c5fdcd2139048bb173d64b3e6e6d3323`
- Ignored baseline and post-state: only `bin/`; tree `3c5a3dc60df67f14e0b7d986305ca0985653ce7d`, tar SHA-256 `27c0df71bd43106c0c40ad85c451155a0ac6d350c660d5c93d7e96a7554f2a49`

The single `/bin/bash` 3.2 block ran from 2026-07-15T04:53:08Z through 05:00:47Z on native macOS 26.5.1 arm64 with GNU Make 3.81. Go 1.26.5 `go mod tidy -diff` and `make ci`, `go test -race -count=10 ./...`, repository docscheck, pinned gofmt and candidate diff checks passed; exact Go 1.25.12 with `GOTOOLCHAIN=local` passed `make check`. Lint reported `0 issues`, govulncheck reported no vulnerabilities, all four fuzz targets passed, and no failure/warning/skip appeared in the 206-line log. An independent evidence audit verified all silent checks, exact pre/post status/untracked/tree/tar equality and empty staging.

The four Go 1.26.5 `CGO_ENABLED=0`, trimpath artifacts had these SHA-256 values and matching `go version -m` target metadata; native darwin/arm64 `--help`/`--version` passed:

| Artifact | SHA-256 |
|---|---|
| `amsftp-darwin-arm64` | `07018e424495a71e6bcf726f6a7f309d36927de7678a83cf081f66b0a5e08249` |
| `amsftp-darwin-amd64` | `7da18d6477faa55992ae415d5c0a4edd336450c6782499df819c9a7a5b7179ae` |
| `amsftp-linux-arm64` | `5ed36ec3ca94f4673da130a3a898defaca5b2e0965579a98a174f92c2fa38eb2` |
| `amsftp-linux-amd64` | `c75d2a513162440afb07438c22b45eb40622de873fb20b1e766d894af6489228` |

The independent whole-tree review found one Medium: command-line assignments could replace ordinary `MAKE_EXECUTION_SKIP_FLAGS`, helper results or `MAKE_CONTRACT_RECIPE_GUARD`, allowing `make -n MAKE_EXECUTION_SKIP_FLAGS= check` and `make SHELL=/usr/bin/true MAKE_CONTRACT_RECIPE_GUARD= check` to return 0 without executing the gate. Regression tests first reproduced all eight bypass forms. The fix uses GNU Make `override` for the complete dependency chain, rejects command-line-origin `MAKEFLAGS`/`MFLAGS`, and adds runtime probes for intermediate variables, `SHELL`, `.SHELLFLAGS`, `.RECIPEPREFIX`, dual inputs and `-e` environment overrides. Both exact reproductions now exit 2; safe output-directory assignments, package test/race/vet, repository lint (`0 issues`) and docscheck pass. The sole fix re-review reported no High, Medium or Low finding. Because the files changed, the green tree above remains permanently superseded and a replacement full gate is mandatory.

## Post-fix replacement local gate

The post-review replacement was frozen after the Make fix and preliminary durable-state update, then passed the full local gate from the beginning:

- Candidate and post-gate tree: `c91ea59aa021d971effb91a74407a9eb301d68fe`
- Complete patch SHA-256: `b87a77d728d22c586b16df6c5f8d9727a548b8f9c64ea9218030b2d6899e1d69`
- Status SHA-256: `c61e2494e7ed2f6f6218460c8af6641a591e66acff047be1d0c6ea6a41987208`
- NUL untracked inventory: 132 entries, SHA-256 `c96a92926ed273db3a50be2a55fcad0a1cf4402df700c6441e8a5e7237a89f3f`
- Evidence path: `/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-task11-r4.cWCjfQ`
- Gate log SHA-256: `f731b1038498badf94ae3dce64f97b9ec940757fd648ee5ca58e67a685674388`
- Ignored baseline and post-state: only `bin/`; tree `3c5a3dc60df67f14e0b7d986305ca0985653ce7d`, tar SHA-256 `27c0df71bd43106c0c40ad85c451155a0ac6d350c660d5c93d7e96a7554f2a49`

The single `/bin/bash` 3.2 block ran from 2026-07-15T05:20:45Z through 05:28:03Z on native macOS 26.5.1 arm64 with GNU Make 3.81. Every command exited 0:

```bash
set -euo pipefail
cd /Users/bytedance/Documents/Codex/2026-07-14/wo-x/work/stage0-foundation
ROOT=/Users/bytedance/Documents/Codex/2026-07-14/wo-x/work/stage0-foundation
BASE=e14159cef930ffa513ebf6a9a5319f8f0fab1f9f
EVIDENCE_TMP=/private/var/folders/l7/7379px6d495gzqjf6df3953m0000gn/T/amsftp-task11-r4.cWCjfQ
GO125=/Users/bytedance/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.12.darwin-arm64/bin/go

test "$(pwd -P)" = "$ROOT"
test "$(git rev-parse --show-toplevel)" = "$ROOT"
test "$(git rev-parse "$BASE^{commit}")" = "$BASE"
test "$(git rev-parse HEAD)" = "$BASE"
test "$(<"$EVIDENCE_TMP/candidate.tree")" = c91ea59aa021d971effb91a74407a9eb301d68fe
test -z "$(git diff --cached --name-only)"

GOTOOLCHAIN=go1.26.5 go mod tidy -diff
GOTOOLCHAIN=go1.26.5 /usr/bin/make \
  BUILD_DIR="$EVIDENCE_TMP/build outputs" \
  COVERAGE_DIR="$EVIDENCE_TMP/coverage outputs" ci
GOTOOLCHAIN=go1.26.5 go test -race -count=10 ./...
GOTOOLCHAIN=go1.26.5 go run ./internal/tools/docscheck "$ROOT"

GOFMT="$(GOTOOLCHAIN=go1.26.5 go env GOROOT)/bin/gofmt"
UNFORMATTED="$(find cmd internal -type f -name '*.go' -print0 | xargs -0 "$GOFMT" -l)"
test -z "$UNFORMATTED"
COMMON_OBJECTS="$(git rev-parse --path-format=absolute --git-common-dir)/objects"
GIT_INDEX_FILE="$EVIDENCE_TMP/candidate.index" \
GIT_OBJECT_DIRECTORY="$EVIDENCE_TMP/candidate.objects" \
GIT_ALTERNATE_OBJECT_DIRECTORIES="$COMMON_OBJECTS" \
  git diff --cached --check "$BASE" -- .

GOTOOLCHAIN=local "$GO125" version
GOTOOLCHAIN=local /usr/bin/make GO="$GO125" \
  BUILD_DIR="$EVIDENCE_TMP/oldstable build outputs" \
  COVERAGE_DIR="$EVIDENCE_TMP/oldstable coverage outputs" check

cmp -s "$EVIDENCE_TMP/candidate.status" "$EVIDENCE_TMP/post.status"
cmp -s "$EVIDENCE_TMP/candidate.untracked.zlist" "$EVIDENCE_TMP/post.untracked.zlist"
cmp -s "$EVIDENCE_TMP/candidate.tree" "$EVIDENCE_TMP/post.tree"
cmp -s "$EVIDENCE_TMP/candidate.ignored-tree" "$EVIDENCE_TMP/post.ignored-tree"
cmp -s "$EVIDENCE_TMP/candidate.ignored.tar" "$EVIDENCE_TMP/post.ignored.tar"
test -z "$(git diff --cached --name-only)"
git diff --check
git diff --cached --check
```

No controller script was used for this run; the literal shell above is the durable validation replay ledger. The pre/post temporary-index and ignored-output capture commands are preserved verbatim in [Task 11 Step 1 and Step 3](../superpowers/plans/2026-07-14-stage-0-foundation.md#task-11-integrate-review-verify-stage-0-and-update-durable-state); the replacement run used that block with the literal `ROOT`, `BASE` and `EVIDENCE_TMP` values above, wrote only below `EVIDENCE_TMP`, and compared rather than overwrote the baseline.

The stable `make ci` included canonical Make verification and its guard-override attack matrix, fmt, vet, all-package coverage, explicit Provider contract, docscheck, both modules' tidy/verify, golangci-lint `0 issues`, native race, four named fuzz targets, govulncheck with no vulnerabilities, pinned actionlint and four target builds. The extra 10-run race passed; the slowest package took 308.433 seconds. Go 1.25.12 ran with `GOTOOLCHAIN=local` and passed the complete compatibility `make check`. Candidate/post status, NUL inventory, tree, ignored Git tree and same-run tar were byte-identical; staging stayed empty. The log contains no failure, warning, skip, panic or fatal marker.

The four artifact hashes and `go version -m` metadata are identical to the table above: each reports Go 1.26.5, its expected GOOS/GOARCH, `CGO_ENABLED=0` and trimpath; native darwin/arm64 help/version smoke passed. A separate read-only evidence audit verified the temporary index/full patch (149 paths: 17 tracked modifications plus 132 untracked files), every logged/silent gate, all artifact bytes/metadata, native smoke, candidate/post comparisons and empty staging. It reported PASS with no hidden failure, warning or whole-target skip. Its first auxiliary zsh check accidentally reused special variable `path` and lost `PATH`; the same read-only check passed after renaming the variable, so that diagnostic is not product-gate evidence.

## Cold-start audits

Cold-start audit 1 used only `docs/README.md` and its documented new-session reading order, without `.superpowers`, chat/Git history, network or file changes. It recovered the current stage and reason, replacement tree/evidence path, missing hosted evidence, conditional Stage 1 M1.1 action, IPC 1.0 framing/cursor/error rules, read-only Provider plus optional mutable facet and capability identity, and the non-production-ready boundary. It returned FAIL solely because the then-labelled exact command block left `ROOT`, `EVIDENCE_TMP`, `GO125` undefined and used an `<all cmd/internal Go files>` placeholder. The literal replay ledger above closed that gap.

Cold-start audit 2 independently recovered the same facts and confirmed the literal ledger/evidence, but strictly returned FAIL on one stale Current checkpoint sentence that still said both audits remained. After that single sentence was corrected, the same independent Agent rechecked the reading chain and returned PASS with no blocking documentation gap. At that checkpoint it correctly confirmed Stage 0 remained In Progress and Stage 1 remained closed pending Hosted CI. Neither audit modified files or used full CI, network, Git history or `.superpowers`. The later green exact-tree Hosted run closes that external gate; the product remains non-production-ready.

## Deferred evidence outside the Stage 0 exit gate

- Native arm64 nightly runner availability — Not verified; Stage 6 must supply native Linux arm64 install/smoke evidence before a production support claim.
- Docker-based supplemental Linux check — Not run; the installed client had no available daemon socket. Hosted native Ubuntu 22.04/24.04 jobs are the accepted Stage 0 Linux evidence.
- The evidence/status-only closeout commit receives its own exact-tree CI attestation after push; that run is reported in the external handoff because a commit cannot self-reference its future run URL.

Cross-compilation on macOS is not treated as native Linux or Linux race evidence. Hosted run 29394698864 supplies the required native platform evidence for the verified implementation tree.

## CORE evidence

| ID | Result | Evidence |
|---|---|---|
| CORE-001 | PASS | Role dispatch/buildinfo tests, four-target builds, metadata checks, native darwin/arm64 smoke and all eight Hosted native/oldstable legs passed. |
| CORE-002 | PASS | IPC envelope, handshake, frame, byte-path, cancellation, strict UTF-8, canonical controls and fuzz tests passed local and Hosted gates. |
| CORE-003 | PASS | Provider validators, reusable contract suite and Fake conformance passed local dual-toolchain and Hosted gates. |
| CORE-004 | PASS | Capability values/contracts plus D3 bookkeeping and E1 operation enforcement cover session generations, complete/incomplete withdrawal, restoration, new-session reset and typed capability-loss errors. |
| CORE-005 | PASS | Deterministic Fake Groups A–E, repeated/race/fuzz/cross-toolchain gates and independent whole-task review passed. |
| CORE-006 | PASS | Strict schema/default/version/unknown-field behavior and deterministic clock tests passed local and Hosted gates. |
| CORE-007 | PASS | Pinned workflows, every native/oldstable leg, four builds, eight reproducibility producers, comparison and final provenance aggregation passed run 29394698864. |
| CORE-008 | PASS | Docscheck and durable plans passed; two independent cold-start audit cycles closed the literal-ledger and stale-checkpoint gaps and ended PASS. |

All Stage 0 CORE rows are Verified.

## Immediate next action

Begin Stage 1 M1.1 in a separate implementation change: perform exact `tcell/v3 v3.4.0` dependency intake, then implement ADR-0007 Paths, single-instance lock and peer-UID boundaries test-first. The user-owned `.idea/` drift remains preserved ignored metadata. Stage 0 completion does not make the product usable or production-ready.
