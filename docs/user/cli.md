# Command-line interface

AMSFTP 1.0 uses exit status `0` for success and stable failure classes `1` internal, `2` usage, `3` configuration, `4` authentication, `5` network, `6` conflict, `7` partial completion, and `8` user cancellation. Human output is the default. Commands that accept `--format json` write one versioned JSON object to stdout on success and one versioned JSON error object to stderr on failure.

## Launch and configuration

```text
amsftp [<location> [<location>]]
amsftp --workspace <name>
amsftp daemon <start|status> [--format human|json]
amsftp daemon stop --confirm stop [--format human|json]
amsftp helper status <SSH-host> [--format human|json]
amsftp helper <install|upgrade> <SSH-host> --accept-shared-session-stable-home [--format human|json]
amsftp config <validate|print-effective|print-effective-keymap|reset-keymap> [arguments]
amsftp completion <bash|zsh|fish>
```

See the [configuration reference](configuration.md) and [keymap reference](keymap.md) for the configuration subcommands. Completion generation is static: it does not connect to a daemon or endpoint, prompt for authentication, or perform a remote write.

## Daemon lifecycle

`amsftp daemon status` is probe-only: it never starts the daemon. A healthy daemon reports its build version and negotiated client-daemon protocol; an absent socket is a successful `running: false`, `state: "stopped"` status result, while an existing but unhealthy, untrusted, or incompatible socket is a network-class failure rather than being misreported as stopped. `amsftp daemon start` is idempotent and reports either `started` or `already_running` after a successful handshake.

Stopping is deliberately disruptive and requires the exact token `amsftp daemon stop --confirm stop`. AMSFTP validates that token and all output options before resolving runtime paths or opening the control socket. The shutdown request travels only over the owner-private local socket after the existing peer-UID and protocol handshake; the daemon acknowledges it before canceling its listener, active connections, transfer manager, database, and log lifecycle. An unconfirmed stop performs no probe or RPC. The private bare daemon role and `--resume-migration` recovery flag remain internal process/upgrade surfaces and are omitted from shell completion.

Daemon JSON success has a stable v1 shape:

```json
{"output_version":1,"daemon":{"running":true,"state":"running","daemon_version":"1.0.0","protocol":"1.0"}}
```

## Helper status and release-gated install/upgrade

`amsftp helper status <SSH-host>` validates the host alias and output format before resolving runtime paths or connecting to the daemon. It opens one ordinary SSH Provider session through the daemon, reads the negotiated capability snapshot, and releases that temporary Endpoint whether the snapshot succeeds or fails. It does not install, enable, upgrade, disable, or remove Helper bytes.

The command reports Level 0 with a safe fallback reason/recovery or Level 1 with the negotiated Helper version and independently negotiated capability names. Malformed, duplicate, unbounded, or unknown `helper_status` fields fail as an internal protocol error instead of being trusted or printed raw. Human output states whether production distribution is open; JSON success has this v1 shape:

```json
{"output_version":1,"command":"helper status","host":"work","endpoint_id":"ep_aaaaaaaaaaaaaaaaaaaaaaaaaa","state":"ready","helper":{"level":0,"version":"","capabilities":[],"reason":"not_available","recovery":"continue with Level 0; enable a verified Helper explicitly when available","production_distribution_open":false}}
```

`amsftp helper install` and `amsftp helper upgrade` require the literal `--accept-shared-session-stable-home` assertion. The parser rejects duplicate consent, arbitrary manifest/artifact/path options, option-shaped host aliases, unknown output formats, and all other arguments before resolving runtime paths or opening an RPC/SSH connection. While protected signing, notarization, release-admitted artifact, offline manifest-signing custody, and final-byte gates are incomplete, both commands return configuration exit `3` with `production Helper distribution is closed`; JSON requests receive the same versioned machine-error shape. This closed public surface cannot probe a host, stage local metadata, create a remote directory, or write content.

The restricted remote process role `amsftp helper serve` remains separate from public lifecycle commands and is never offered by shell completion. No public command accepts a remote command string, local artifact/manifest path, fixture trust, or custom remote install path. Standard SFTP remains Level 0 regardless of this release gate.

## Durable Job query and control

```text
amsftp job list [--limit <1..100>] [--format human|json]
amsftp job events <job-id> [--after <sequence>] [--limit <1..100>] [--format human|json]
amsftp job pause <job-id> [--format human|json]
amsftp job resume <job-id> [--format human|json]
amsftp job cancel <job-id> --confirm <same-job-id> [--format human|json]
```

`list` and `events` use a default limit of 50 and a hard limit of 100. `events` uses an exclusive lower-bound cursor from the existing daemon API: only records with a sequence greater than `--after` are returned. Job IDs must have the canonical `job_` plus 26-character lowercase base32 form.

Arguments, bounds, Job ID syntax, and exact cancellation confirmation are all validated before AMSFTP connects to or starts the local daemon. A rejected command therefore cannot issue a Job RPC. `pause`, `resume`, and confirmed `cancel` use only the existing high-level durable Job RPCs; the CLI exposes no raw provider mutation route. Cancellation is rejected unless the value following `--confirm` exactly equals the target Job ID.

Human `list` output is tab-separated with `JOB_ID`, `STATE`, `KIND`, `ROUTE`, `BYTES`, and `TOTAL` columns. Human `events` output is tab-separated with `JOB_ID`, `SEQUENCE`, `CREATED_AT`, `KIND`, and `PAYLOAD` columns. Scripts should select JSON instead of parsing the human tables.

Successful JSON uses `output_version: 1` and exactly one result member (the snapshot example below is shortened):

```json
{"output_version":1,"jobs":[]}
{"output_version":1,"events":[]}
{"output_version":1,"snapshot":{"job_id":"job_aaaaaaaaaaaaaaaaaaaaaaaaaa","state":"paused"}}
```

Snapshot, Job, Location, and event fields use explicit snake_case v1 names rather than Go implementation field names. Empty lists are encoded as `[]`, never `null`; event `payload` is embedded as a JSON value rather than a quoted storage string. Times use RFC 3339 JSON representation.

JSON failures are written only to stderr and retain the process exit status:

```json
{"output_version":1,"error":{"exit_code":4,"class":"authentication","message":"auth_required: authentication required","request_id":"req_aaaaaaaaaaaaaaaaaaaaaaaaaa","error_code":"auth_required","retry":"after_auth","effect":"none"}}
```

The structural fields are always present. Local validation failures leave the RPC diagnostic fields empty. Daemon failures expose only the daemon's safe request ID, error code, retry advice, effect status, and public message; transport causes and credentials are not serialized.

## Help, man page, and completion parity

`amsftp --help`, [amsftp(1)](../man/amsftp.1), and bash/zsh/fish completion scripts are rendered from the same ordered command facts and checked for drift. Restricted `askpass` and `helper serve` roles are not public daily-use commands and are deliberately absent from completions.
