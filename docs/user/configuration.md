# Configuration Reference

AMSFTP configuration is a versioned, strict JSON document. The current schema version is `1`. Unknown fields, trailing JSON values, missing `schema_version`, unsupported versions, values above hard safety ceilings, and explicitly invalid zero values are rejected.

## Location and precedence

AMSFTP currently supports one user configuration file at the platform path reported by its runtime-path policy. There is no system-wide configuration file in 1.0. An absent user file means the documented defaults are effective.

The current precedence is:

1. Explicit CLI launch locations or `--workspace` select the startup target; they do not rewrite configuration.
2. A saved workspace supplies pane locations, layout, filters, and workspace cache policy.
3. The user configuration supplies the versioned application settings documented below.
4. Built-in defaults fill every omitted configuration field.

Environment variables are not a general configuration layer. AMSFTP passes the process environment to the validated system OpenSSH and uses bounded environment lookup when resolving default editor/opener commands. It does not accept secrets, SSH options, or safety-policy overrides through AMSFTP-specific environment variables. Job-semantic settings are frozen when a Job is planned; a future safe reload may affect only explicitly documented live settings.

## Commands

Validate the effective default/user configuration:

```console
amsftp config validate
```

Validate an explicit owner-private regular file without replacing the user configuration:

```console
amsftp config validate /path/to/config.json
```

Print a versioned, redacted JSON representation of the effective configuration:

```console
amsftp config print-effective
amsftp config print-effective /path/to/config.json
```

`print-effective` never prints configured command arguments: each argument is replaced with `<redacted>`. It writes one JSON object to stdout with `output_version: 1`, the exact resolution contract under `resolution_policy`, and the configuration under `config`. The policy records CLI startup selection → workspace state → user configuration → built-in defaults, rejects system-wide and AMSFTP-specific environment configuration layers, restricts environment use to validated OpenSSH inheritance and external-command discovery, requires restart for every setting, and freezes Job semantics at plan creation. Diagnostics go to stderr. The command does not contact an endpoint, prompt for authentication, start a transfer, or mutate configuration.

Print the complete effective Normal and Visual keymap, including each action's current input, default input, remap policy, and override state:

```console
amsftp config print-effective-keymap
amsftp config print-effective-keymap /path/to/config.json
```

The deterministic JSON output has `output_version: 1` and contains both contexts. It performs no remote, authentication, daemon, or mutation operation.

Reset only the keymap overrides in the default or an explicit configuration file:

```console
amsftp config reset-keymap --yes
amsftp config reset-keymap --yes /path/to/config.json
```

`--yes` is mandatory. Before replacement, AMSFTP validates and fully decodes the owner-private source file. It clears only `keymap.bindings`, preserves all other semantic values without redacting configured argv, writes a validated 0600 temporary file, syncs it, atomically replaces the configuration, validates the published file, and syncs the parent directory. A missing/invalid confirmation or invalid source leaves the original bytes unchanged. The normalized replacement may make previously omitted default fields explicit; a running client/daemon is not hot-reloaded.

The stable public exit codes are:

| Code | Meaning |
|---:|---|
| 0 | success |
| 1 | internal/unclassified failure |
| 2 | command usage error |
| 3 | configuration error |
| 4 | authentication error |
| 5 | network error |
| 6 | conflict requiring policy or user action |
| 7 | partial completion |
| 8 | user cancellation |

Only commands that have enough typed evidence emit a specialized code; otherwise they fail as internal rather than guessing a category.

## Current schema

The smallest valid file is:

```json
{
  "schema_version": 1
}
```

Omitted fields inherit these current defaults:

```json
{
  "schema_version": 1,
  "ipc": {
    "max_frame_bytes": 8388608
  },
  "listing": {
    "default_page_size": 256,
    "max_page_size": 4096
  },
  "cache": {
    "global_bytes": 2147483648,
    "global_entries": 4096,
    "workspace_bytes": 1073741824,
    "max_eviction_candidates": 256
  },
  "transfer": {
    "max_concurrent": 4,
    "max_queued": 128,
    "global_bytes_per_second": 0,
    "endpoint_bytes_per_second": 0,
    "job_bytes_per_second": 0
  },
  "preview": {
    "max_input_bytes": 524288,
    "max_json_bytes": 262144,
    "max_json_depth": 64,
    "max_rendered_lines": 10000,
    "max_output_bytes": 524288,
    "max_image_pixels": 40000000,
    "max_style_spans": 4096,
    "image_max_payload_bytes": 4194304,
    "image_max_output_bytes": 6291456,
    "image_chunk_bytes": 4096,
    "image_max_pixels": 1000000
  },
  "search": {
    "filename": {
      "page_items": 256,
      "event_buffer": 64,
      "concurrent_lists": 1,
      "max_depth": 128,
      "max_entries": 1000000,
      "max_results": 10000,
      "max_output_bytes": 8388608,
      "max_duration_ms": 300000
    },
    "content": {
      "page_items": 128,
      "event_buffer": 32,
      "max_depth": 32,
      "max_entries": 10000,
      "max_files": 1000,
      "max_results": 5000,
      "max_matches_per_file": 100,
      "max_file_bytes": 1048576,
      "max_read_bytes": 33554432,
      "max_snippet_bytes": 512,
      "max_output_bytes": 8388608,
      "max_duration_ms": 120000
    }
  },
  "retry": {
    "reconnect_delays_ms": [100, 250, 500],
    "job_retry_delay_ms": 60000
  },
  "integrity": {
    "transfer_policy": "strong"
  },
  "direct_transfer": {
    "enabled": false
  },
  "diagnostic": {
    "log_max_bytes": 4194304,
    "log_backups": 3,
    "ring_records": 1000
  },
  "helper": {
    "enabled": false
  },
  "external": {
    "previewers": []
  }
}
```

`ipc.max_frame_bytes` may be tightened but cannot exceed the protocol maximum of 8 MiB. The daemon validates and freezes it at startup for both inbound and outbound control frames; a frame over that configured bound closes only the offending connection. Listing sizes must be nonzero, `default_page_size` cannot exceed `max_page_size`, and `max_page_size` cannot exceed 4096. The client freezes `default_page_size` at startup and uses it for every page of a directory listing; `max_page_size` is the validated ceiling for that effective request size.

`cache` can only tighten the existing safety ceilings: 2 GiB and 4096 global entries, 1 GiB per workspace, and 256 eviction candidates. A workspace byte limit must not exceed the effective global byte limit. The daemon validates and freezes these values when it constructs the cache manager.

`transfer.max_concurrent` and `transfer.max_queued` can only tighten the existing ceilings of 4 active Jobs and 128 queued Jobs. Global, per-Endpoint, and per-Job byte rates accept `0` for the existing unlimited behavior or a positive value no greater than 1 TiB/s. These settings are read by the daemon and frozen into the manager/scheduler; changing the file does not alter already planned Jobs or a running daemon.

`preview` can only tighten the existing built-in renderer and terminal-image ceilings. Combination checks keep JSON input within total input, terminal-image payload within terminal output, chunks within payload, and terminal-image pixels within both image limits. The client validates and freezes these limits at startup; changing the file does not alter a running client.

`search.filename` and `search.content` can only tighten the existing interactive-search resource envelopes. All page, queue, concurrency, depth, entry, result, byte, and millisecond duration values must remain nonzero and no greater than the documented defaults. Content search also requires `max_file_bytes` not to exceed `max_read_bytes`. The client validates and freezes both envelopes at startup, and each search request carries its frozen budget identity; changing the file does not alter a running client or an already-started search.

`retry.reconnect_delays_ms` controls only transport errors explicitly classified as reconnectable. The array may be empty to disable automatic reconnects or contain at most three nondecreasing delays. A configured delay cannot be shorter than the corresponding default (`100`, `250`, then `500` ms) and no delay may exceed 30 seconds, so configuration cannot add attempts or make them more aggressive. `retry.job_retry_delay_ms` is bounded to 60 seconds through 10 minutes. The client and daemon freeze these values at startup; a Job entering `retry_wait` persists its computed retry time, so later file changes cannot rewrite that decision. Authentication, host-key, configuration, conflict, and uncertain destructive outcomes are not converted into automatic retry by these settings.

`integrity.transfer_policy` accepts only `strong` (the default) or `require_strong`. Both preserve SHA-256 transfer verification; `require_strong` also refuses any route that cannot satisfy the strong-integrity contract. The client freezes the validated policy into every copy Intent and the Planner carries it into the immutable Plan and route evidence. `baseline`, weaker algorithms, and unknown values are rejected rather than silently reducing the existing verification level.

`direct_transfer.enabled` is a read-only representation of the production distribution boundary and must remain `false`. Setting it to `true` is a configuration error. The ordinary client therefore cannot enable the fixture-only Level 2 backend or its workspace/data policy gates; cross-Endpoint transfers continue to use the bounded relay route with `production_distribution_closed` evidence. Opening this boundary requires separate custody, signing, notarization, final-byte manifest, security review, and release gates—not a local configuration change.

`diagnostic.log_max_bytes` sets the owner-private JSON daemon log's per-file limit and must remain between 256 bytes and the existing 4 MiB ceiling. `diagnostic.log_backups` retains between one and the existing three rotated files. `diagnostic.ring_records` bounds the in-memory, already-redacted daemon diagnostic history between one and the existing 1000-record ceiling. The daemon freezes all three values at startup. The ring limit also applies when corrupt or newer persistent state forces the read-only degraded logger; no configuration value enables raw messages, paths, credentials, file content, extra fields, automatic upload, or a broader log level.

`helper.enabled` defaults to `false` and remains gated by the owning production-distribution policy. It cannot be set to `true` until the protected Developer ID/notary, final-byte manifest, offline-signature, custody, and release gates open that policy; opening distribution never changes the explicit-opt-in default. The field cannot inject trust roots, fixture artifacts, remote commands, installation consent, credentials, or Level 2 access. Standard Level 0 SFTP remains available. Per-Endpoint installation and execution still require the separately specified explicit approvals and current-policy/fresh-binding/hash/handshake checks after production distribution exists.

`external.editor` and `external.opener` are structured commands with `executable` and an `argv` array. They are executed directly, never concatenated into a shell command. A previewer adds a unique name, bounded media-type or extension match, structured command, timeout, input limit, and completeness requirement. Command strings reject control characters and all counts and byte sizes have hard ceilings.

This is the complete 1.0 application configuration surface. Job-history retention and migration recovery are owned by M6.2 durable-state policy rather than a user-tunable setting. Host-key, credential, confirmation, Helper trust/custody, direct-transfer, redaction, owner-private storage, and other security boundaries are fixed product policy; configuration cannot weaken them. No undocumented field is accepted.

The `keymap.bindings` section supports the `normal` and `visual` contexts. Each entry contains `context`, a single-rune `input`, and a documented remappable `action`. Exact defaults, reserved dangerous/sequence actions, count behavior, and the deliberate macro/named-register exclusion are in the [keymap reference](keymap.md).
