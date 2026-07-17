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

`print-effective` never prints configured command arguments: each argument is replaced with `<redacted>`. It writes one JSON object to stdout with `output_version: 1` and the configuration under `config`. Diagnostics go to stderr. The command does not contact an endpoint, prompt for authentication, start a transfer, or mutate configuration.

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
  "external": {
    "previewers": []
  }
}
```

`ipc.max_frame_bytes` may be tightened but cannot exceed the protocol maximum of 8 MiB. Listing sizes must be nonzero, `default_page_size` cannot exceed `max_page_size`, and `max_page_size` cannot exceed 4096.

`external.editor` and `external.opener` are structured commands with `executable` and an `argv` array. They are executed directly, never concatenated into a shell command. A previewer adds a unique name, bounded media-type or extension match, structured command, timeout, input limit, and completeness requirement. Command strings reject control characters and all counts and byte sizes have hard ceilings.

The schema is not yet complete for every Stage 6 setting. Cache quota, concurrency, bandwidth, integrity, retry, Helper, direct-transfer, retention, diagnostic, and security-policy sections remain under M6.1 implementation and must not be inferred from internal constants. This page and `config print-effective` will be extended with the implementation; no undocumented field is accepted.

The `keymap.bindings` section is now implemented for the `normal` and `visual` contexts. Each entry contains `context`, a single-rune `input`, and a documented remappable `action`. Exact defaults, reserved dangerous/sequence actions, count behavior, and the deliberate macro/named-register exclusion are in the [keymap reference](keymap.md). The other sections listed above remain open.
