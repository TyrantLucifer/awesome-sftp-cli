# Configuration

[简体中文](../zh-CN/user/configuration.md)

AMSFTP works without a configuration file. Create one only when you want to change
resource limits, retry timing, integrity policy, external programs, or remappable
keys.

The file is strict JSON with schema version `1`. Unknown fields and unsafe values
are rejected instead of being ignored.

## Find the configuration file

Default paths are:

- macOS:
  `~/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json`
- Linux:
  `${XDG_CONFIG_HOME:-$HOME/.config}/amsftp/config.json`
- Managed standalone installation: `config/config.json` below the managed root
  recorded by the installer.

The file must be a regular, owner-private file and must not be a symlink. Use mode
`0600`:

```sh
chmod 600 /absolute/path/to/config.json
```

The smallest valid file is:

```json
{
  "schema_version": 1
}
```

Omitted settings use built-in defaults.

## Validate before restarting

Validate the normal user file:

```sh
amsftp config validate
```

Validate another file without installing it:

```sh
amsftp config validate /absolute/path/to/config.json
```

Inspect the effective, redacted configuration:

```sh
amsftp config print-effective
amsftp config print-effective /absolute/path/to/config.json
```

External command arguments are redacted in this output. The command does not
connect to an endpoint or start a transfer.

Configuration is read when the client or daemon starts. After changing the file,
close affected TUI clients and restart your daemon when the changed group applies
to it:

```sh
amsftp daemon stop --confirm stop
amsftp daemon start
```

Existing Jobs keep the choices recorded when they were created.

## Common settings

### Limit concurrent transfers or bandwidth

```json
{
  "schema_version": 1,
  "transfer": {
    "max_concurrent": 2,
    "max_queued": 64,
    "global_bytes_per_second": 10485760,
    "endpoint_bytes_per_second": 0,
    "job_bytes_per_second": 0
  }
}
```

Rates are bytes per second. `0` means unlimited. Configuration can tighten the
built-in safety ceilings but cannot expand them.

### Require strong transfer integrity

```json
{
  "schema_version": 1,
  "integrity": {
    "transfer_policy": "require_strong"
  }
}
```

The default `strong` policy verifies transferred content with SHA-256.
`require_strong` additionally refuses a path that cannot satisfy the strong
integrity contract. Weaker policies are not accepted.

### Select an editor and opener

```json
{
  "schema_version": 1,
  "external": {
    "editor": {
      "executable": "nvim",
      "argv": ["-f"]
    },
    "opener": {
      "executable": "/absolute/path/to/opener",
      "argv": []
    }
  }
}
```

Commands are executed directly, never concatenated into a shell command. A bare
executable name is resolved through `PATH`; a value containing `/` must be
absolute. AMSFTP appends the materialized file as the final argument.

### Add an external previewer

```json
{
  "schema_version": 1,
  "external": {
    "previewers": [
      {
        "name": "pdf-viewer",
        "media_types": ["application/pdf"],
        "extensions": [".pdf"],
        "command": {
          "executable": "/absolute/path/to/previewer",
          "argv": []
        },
        "timeout_ms": 5000,
        "max_input_bytes": 1048576,
        "require_complete": true
      }
    ]
  }
}
```

External previewers are tried in order after the built-in preview. The
`require_complete` setting decides whether a rule needs a complete
materialization; input size and execution time remain bounded either way. Their
output is not copied into the terminal, and a failed previewer falls back to the
built-in result.

### Remap a key

```json
{
  "schema_version": 1,
  "keymap": {
    "bindings": [
      {
        "context": "visual",
        "input": "n",
        "action": "down"
      }
    ]
  }
}
```

Keymaps have `normal` and `visual` contexts. A binding moves one remappable action
to one single-character input; it does not create a second binding. Digits,
dangerous actions, multi-key sequences, control characters, and conflicting
inputs are reserved.

Inspect the complete result:

```sh
amsftp config print-effective-keymap
```

Reset only keymap overrides:

```sh
amsftp config reset-keymap --yes
```

The reset keeps every unrelated configuration setting.

## Setting groups

The current schema groups settings by purpose:

| Group | What it controls |
| --- | --- |
| `listing` | Directory page sizes |
| `cache` | Global and per-workspace cache quotas |
| `transfer` | Concurrent/queued Jobs and bandwidth rates |
| `preview` | Input, rendered output, JSON, and image limits |
| `search.filename` | Recursive filename-search limits |
| `search.content` | Recursive content-search limits |
| `retry` | Safe reconnect delays and Job retry delay |
| `integrity` | Transfer integrity requirement |
| `diagnostic` | Private log rotation and in-memory history size |
| `external` | Editor, opener, and external previewers |
| `keymap` | Normal and Visual remappable bindings |
| `ipc` | Advanced local control-message size limit |

Use `amsftp config print-effective` to see all current defaults. Most numeric
settings can only be reduced from those defaults; the validator explains values
outside the accepted range.

You may also see `helper.enabled` and `direct_transfer.enabled` in effective
output. Both must remain `false` in public builds. Configuration cannot open the
closed production acceleration or direct-transfer paths.

## Precedence and privacy

Startup locations or `--workspace` choose what to open. A workspace supplies pane
locations and view state. The user configuration supplies application settings,
and built-in defaults fill anything omitted.

AMSFTP-specific environment variables are not a general configuration layer.
`VISUAL`, `EDITOR`, `PATH`, and the environment inherited by the validated system
OpenSSH are used only for their documented purposes.

Do not put passwords, private keys, authentication answers, or arbitrary SSH
options in `config.json`. Remote connection behavior belongs in
`~/.ssh/config`, where the system OpenSSH client interprets it.
