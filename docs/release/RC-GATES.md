# Release-candidate gate record

REL-008 uses a strict external JSON record to bind every release gate to one exact candidate commit/tree and to prevent partial, synthetic, failed, skipped, or mixed-revision evidence from being treated as release success. The validator checks evidence; it does not run gates, query GitHub, freeze an RC, sign artifacts, or create release authority.

No release-candidate record exists yet. Production Helper and Level 2 remain **CLOSED**, no RC is frozen, and no current run may be presented as final REL-008 evidence.

## Operator commands

Keep the real record outside the repository and run the validator with the exact current Go toolchain:

```sh
GOTOOLCHAIN=local go run ./internal/tools/releasegate candidate /absolute/path/to/rc-gates.json
GOTOOLCHAIN=local go run ./internal/tools/releasegate final /absolute/path/to/rc-gates.json
```

`candidate` requires the complete gate catalog plus exact candidate push and PR runs. `final` requires the same evidence and an exact post-merge identity plus the successful `main` run for that merge commit. Input must be a non-empty, bounded, non-symlink regular file. Unknown fields, trailing JSON, invalid 40-character lowercase Git object IDs, duplicate/missing gates or runs, empty references, and file-identity changes while opening are rejected.

## Record v1

The top-level schema is `amsftp-release-gate-record-v1`:

```json
{
  "schema": "amsftp-release-gate-record-v1",
  "candidate": {"commit": "<40 lowercase hex>", "tree": "<40 lowercase hex>"},
  "merge": {"commit": "<40 lowercase hex>", "tree": "<40 lowercase hex>"},
  "gates": [],
  "hosted": []
}
```

`merge` is absent during candidate validation and mandatory for final validation. Every gate row contains `id`, its exact `kind`, the candidate `commit` and `tree`, `status: "completed"`, `conclusion: "success"`, a non-empty `reference`, and gate-specific `metrics` where required. A reference identifies the immutable report/log/artifact; it is not accepted as proof unless the bound gate fields and external evidence are truthful.

The required ordered catalog is:

| Gate ID | Exact evidence kind | Boundary |
|---|---|---|
| `current_ci` | `make_ci` | exact current Go complete local gate |
| `oldstable_check` | `make_check` | exact Go 1.25.12 complete local check |
| `race` | `race` | full race gate |
| `fuzz` | `fuzz` | required protocol/path fuzz gates |
| `fault_injection` | `fault_injection` | complete declared fault matrix |
| `real_authentication` | `real_authentication` | real OpenSSH/Askpass/Kerberos matrix |
| `native_compatibility` | `native_compatibility` | declared native OS/architecture compatibility matrix |
| `migration_rollback` | `migration_rollback` | historical migration, recovery, and rollback |
| `documentation` | `documentation` | docs/contracts/user-path gate |
| `scale_50k` | `scale` | 50,000-entry gate |
| `scale_million` | `scale` | million-node gate |
| `physical_100gib` | `physical` | at least 100 GiB actually transferred; `synthetic` is rejected |
| `isolated_level2` | `process_network_isolated` | at least two independent processes, a real network boundary, and zero daemon content bytes |
| `soak` | `soak` | observed duration meets the positive declared duration and race/deadlock/leak acceptance is explicit |
| `reproducibility` | `reproducibility` | exact candidate reproducibility evidence |
| `security_review` | `security_review` | at least two independent reviews of the exact candidate |
| `secret_pollution` | `secret_pollution` | final candidate secret and production-fixture pollution audit |

Gate kinds are deliberately non-interchangeable. `make ci`, sparse/synthetic logical-size checks, same-process fixtures, cross-builds, or a prior candidate cannot satisfy physical 100 GiB, isolated Level 2, native compatibility, or exact-candidate review rows.

## Hosted evidence

Candidate validation requires exactly one `candidate_push` and one `candidate_pr` row. Each row contains a positive `run_id`, the exact candidate `head_sha`, `status: "completed"`, `conclusion: "success"`, and job counts. `jobs_success` must equal the positive `jobs_total`; `jobs_skipped` and `jobs_failed` must both be zero. Classified flaky failures remain useful historical evidence but cannot satisfy this final record.

Final validation additionally requires exactly one `postmerge_main` row bound to the merge commit, with the same all-jobs-success rule. This preserves the required candidate → merge → exact-main ordering and prevents a failed, dependency-skipped, superseded, or unrelated run from opening release actions.

The record intentionally contains no private key, credential, ticket, authentication answer, notarization secret, or artifact bytes. Protected evidence references may disclose only the non-sensitive identities and results allowed by the Stage 6 release policy.
