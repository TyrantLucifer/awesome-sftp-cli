# Command-line interface

AMSFTP 1.0 uses exit status `0` for success and stable failure classes `1` internal, `2` usage, `3` configuration, `4` authentication, `5` network, `6` conflict, `7` partial completion, and `8` user cancellation. Human output is the default. Commands that accept `--format json` write one versioned JSON object to stdout on success and one versioned JSON error object to stderr on failure.

## Launch and configuration

```text
amsftp [<location> [<location>]]
amsftp --workspace <name>
amsftp daemon <start|status> [--format human|json]
amsftp daemon stop --confirm stop [--format human|json]
amsftp helper <status|disable> <SSH-host> [--format human|json]
amsftp helper <install|upgrade|remove> <SSH-host> --accept-shared-session-stable-home [--format human|json]
amsftp config <validate|print-effective|print-effective-keymap|reset-keymap> [arguments]
amsftp doctor [--endpoint <SSH-host>] [--format human|json]
amsftp support-bundle preview [--format human|json]
amsftp support-bundle create --consent <sha256> --output <absolute-path> [--format human|json]
amsftp completion <bash|zsh|fish>
```

See the [configuration reference](configuration.md) and [keymap reference](keymap.md) for the configuration subcommands. Completion generation is static except at the value immediately following `--workspace`: bash, zsh, and fish invoke the installed binary's hidden completion query to enumerate saved workspace names. That query reads at most 1,024 entries from the resolved owner-private workspace directory, returns at most 256 valid private-file names in lexical order, and treats an absent directory as an empty result without creating it. It does not read workspace contents, start or contact the daemon, connect to an endpoint, prompt for authentication, or perform a write. Shell scripts suppress a rejected unsafe-state query rather than exposing its diagnostic during interactive completion.

## Daemon lifecycle

`amsftp daemon status` is probe-only: it never starts the daemon. A healthy daemon reports its build version and negotiated client-daemon protocol. An absent Socket, or a validated private Unix Socket whose canonical instance lock is definitively absent or unlocked, is a successful `running: false`, `state: "stopped"` result; status does not remove that residual path or create the lock. `amsftp daemon start` may then invoke the ordinary daemon role, but only that process after acquiring the matching instance lock revalidates and removes the exact residual Socket before binding. An unhealthy live daemon, held or uncertain lock, regular file, symbolic link, unsafe Socket, authentication failure, or incompatible protocol remains a network-class failure. Start is idempotent and reports either `started` or `already_running` only after a successful handshake.

Stopping is deliberately disruptive and requires the exact token `amsftp daemon stop --confirm stop`. AMSFTP validates that token and all output options before resolving runtime paths or opening the control socket. The shutdown request travels only over the owner-private local socket after the existing peer-UID and protocol handshake; the daemon acknowledges it before canceling its listener, active connections, transfer manager, database, and log lifecycle. An unconfirmed stop performs no probe or RPC. The private bare daemon role and `--resume-migration` recovery flag remain internal process/upgrade surfaces and are omitted from shell completion.

Daemon JSON success has a stable v1 shape:

```json
{"output_version":1,"daemon":{"running":true,"state":"running","daemon_version":"1.0.0","protocol":"1.0"}}
```

## Helper status and release-gated lifecycle

`amsftp helper status <SSH-host>` validates the host alias and output format before resolving runtime paths or connecting to the daemon. It opens one ordinary SSH Provider session through the daemon, reads the negotiated capability snapshot, and releases that temporary Endpoint whether the snapshot succeeds or fails. It does not install, enable, upgrade, disable, or remove Helper bytes.

The command reports Level 0 with a safe fallback reason/recovery or Level 1 with the negotiated Helper version and independently negotiated capability names. Malformed, duplicate, unbounded, or unknown `helper_status` fields fail as an internal protocol error instead of being trusted or printed raw. Human output states whether production distribution is open; JSON success has this v1 shape:

```json
{"output_version":1,"command":"helper status","host":"work","endpoint_id":"ep_aaaaaaaaaaaaaaaaaaaaaaaaaa","state":"ready","helper":{"level":0,"version":"","capabilities":[],"reason":"not_available","recovery":"continue with Level 0; enable a verified Helper explicitly when available","production_distribution_open":false}}
```

`amsftp helper install` and `amsftp helper upgrade` require the literal `--accept-shared-session-stable-home` assertion. The parser rejects duplicate consent, arbitrary manifest/artifact/path options, option-shaped host aliases, unknown output formats, and all other arguments before resolving runtime paths or opening an RPC/SSH connection. While protected signing, notarization, release-admitted artifact, offline manifest-signing custody, and final-byte gates are incomplete, both commands return configuration exit `3` with `production Helper distribution is closed`; JSON requests receive the same versioned machine-error shape. This closed public surface cannot probe a host, stage local metadata, create a remote directory, or write content.

`amsftp helper disable` is a host-scoped local-selection operation and therefore rejects the shared-session/stable-home assertion. `amsftp helper remove` is the exact remote-artifact lifecycle and requires that literal assertion before any runtime activity. Until daemon lifecycle composition is wired to the existing durable Helper state/removal-lease owners, both commands return configuration exit `3` with `production Helper lifecycle is closed` before runtime-path resolution, RPC, SSH, probing, state mutation, or remote deletion. Exposing their syntax does not claim that disable/removal has executed; production Helper and Level 2 remain closed.

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

## Read-only doctor

`amsftp doctor` runs the fixed config, runtime-directory, control-socket, daemon, system-OpenSSH, known-host policy, database, cache, Helper, and disk-space checks. It does not prepare missing directories, start or repair the daemon, migrate or checkpoint SQLite, change Helper state, prompt for credentials, or upload a report. A stopped database without WAL/SHM sidecars is opened only through SQLite immutable read-only validation and must match the compiled schema head. A database with live sidecars is reported as `database_active`; doctor does not open or alter it.

`--endpoint <SSH-host>` adds one endpoint result. AMSFTP validates the host alias and the trusted `/usr/bin/ssh`, then runs bounded `ssh -G -T` configuration expansion. Duplicate security-relevant fields, unknown host-key policy values, failed expansion, and stdout/stderr beyond 64 KiB fail closed without exporting command output. The known-host result checks effective strict-host-key and known-host-file policy; it does not authenticate or claim that a particular key already exists. For a direct endpoint, doctor performs only a five-second TCP connect and sends no application bytes. ProxyJump or ProxyCommand configurations return `endpoint_proxy_not_probed` rather than invoking the proxy or misreporting direct reachability.

Human output contains only stable tab-separated check, status, detail, and remediation tokens. JSON success is a versioned ordered report; raw errors, endpoint names, hostnames, ports, absolute paths, and subprocess output are excluded:

```json
{"output_version":1,"results":[{"code":"config","status":"pass","severity":"info","detail_code":"config_valid","remediation":"troubleshooting/config"}]}
```

`pass`, `warn`, `fail`, and `skipped` are per-check results; a completed diagnostic report exits `0` even when it contains warnings or failures. Invalid arguments use the normal usage exit `2`, and `--format json` preserves the versioned machine-error channel. Shell completion for doctor is static and offers only `--endpoint` and `--format`.

## Local support bundle

`amsftp support-bundle preview` composes the exact local-only diagnostic snapshot that a later create may publish. The preview lists `manifest.json` plus eight reviewed sources: version, platform, configuration shape, doctor report, bounded recent diagnostics, bounded Job summaries, database health, and daemon/protocol capabilities. Every entry includes its sensitivity label, exact byte size, and SHA-256. The final `consent_digest` binds the ordered file metadata and content hashes; it is not permission to collect a later, changed snapshot.

The sources deliberately omit file content, diagnostic messages, endpoint/Job/request IDs, locations, command executables and arguments, waiting/error text, credentials, authentication prompts, and raw probe/RPC failures. Configuration contributes only schema/status/count/boolean shape. Diagnostics contribute sequence/time plus reviewed level/component/event/error-code tokens. Jobs contribute state, kind, route, phase, counters, and control booleans. Unsafe version, capability, event, database, or diagnostic strings become `<redacted>`. An absent, unhealthy, or incompatible daemon produces stable unavailable/empty summaries rather than starting it; invalid configuration is represented by a safe status and defaults-shaped counts. Preview never prepares application paths, repairs state, prompts, connects to an endpoint, or uploads anything.

Creation is an explicit second step:

```text
amsftp support-bundle create --consent <preview-consent-digest> --output /absolute/private/directory/support.tar.gz
```

AMSFTP recomposes all sources and refuses exit `6` if any file differs from the preview. The output must be a canonical absolute path beneath an existing owner-private `0700` directory; the target must not exist. Publication uses no-follow/no-replace creation, writes a regular `0600` file, synchronizes the file and parent directory, and removes the exact partial target after cancellation or publication failure. It never overwrites a file or follows an output symlink. The deterministic gzip/USTAR archive is limited to 512 KiB per source, 3 MiB expanded, and 4 MiB compressed. No command uploads it, and success output reports only status, size, and archive SHA-256—not the destination path.

Invalid command, format, digest, or output-path arguments are rejected before source composition or publication. JSON errors use the normal versioned machine-error channel without embedding the output path. Shell completion exposes only `preview`, `create`, `--format`, `--consent`, and `--output`.

## Help, man page, and completion parity

`amsftp --help`, [amsftp(1)](../man/amsftp.1), and bash/zsh/fish completion scripts are rendered from the same ordered command facts and checked for drift. All three scripts complete commands and arguments, and dynamically complete the value after `--workspace` through the bounded read-only query described above. The query itself, restricted `askpass`, and `helper serve` roles are not public daily-use commands and are deliberately absent from the public help/man command inventory.
