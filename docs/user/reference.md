# Configuration and Reference

[简体中文](../zh-CN/user/reference.md)

Use this page to look up `config.json` options, default TUI keybindings, and CLI
commands. Run `amsftp --help` or `man amsftp` for the exact command list of your
installed version.

## Configuration file

AMSFTP works with no configuration file at all. Create `config.json` only when
you want to override defaults.

**Default paths:**

- **macOS:** `~/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json`
- **Linux:** `${XDG_CONFIG_HOME:-$HOME/.config}/amsftp/config.json`
- **Custom private data directory:** `<private-dir>/config/config.json`

The file must be a regular, owner-only file (no symlinks) with mode `0600`,
strict JSON, and `"schema_version": 1`. Unknown fields or out-of-range values
are rejected instead of ignored.

Validate and inspect your configuration before restarting:

```sh
amsftp config validate
amsftp config print-effective
```

Both commands accept an optional `/absolute/path/to/config.json` to test a file
without installing it. External command arguments are redacted in the output.
Changes take effect after restarting affected TUI clients—and for daemon-side
settings (`transfer`, `cache`, `retry`, `diagnostic`, `ipc`), restarting the
daemon:

```sh
amsftp daemon stop --confirm stop
amsftp daemon start
```

Existing jobs keep the settings captured when they were created. Do not store
passwords, private keys, or SSH options in `config.json`; put connection details
in `~/.ssh/config`.

## Common configuration examples

### Transfer limits, verification, and sync

```json
{
  "schema_version": 1,
  "transfer": {
    "max_concurrent": 2,
    "max_queued": 64,
    "global_bytes_per_second": 10485760,
    "endpoint_bytes_per_second": 0,
    "job_bytes_per_second": 0,
    "verification": "protocol",
    "durability": "none"
  }
}
```

- **Rates** are in bytes/second (`0` = unlimited). Limits apply to file data
  bytes (excluding SSH framing/encryption); remote-to-remote relays count both
  the read from source and write to destination against global/job limits.
- **`verification`:** `"protocol"` (default for copies) or `"sha256"` (full
  readback hashing). Legacy `integrity.transfer_policy: "require_strong"` also
  forces SHA-256.
- **`durability`:** `"none"` (default), `"completion"` (`fsync` once before
  renaming), or `"checkpoint"` (`fsync` at every checkpoint). Moves and edit
  uploads keep strict verification and synced checkpoints.

### Editor, opener, and external previewers

```json
{
  "schema_version": 1,
  "external": {
    "editor": { "executable": "nvim", "argv": ["-f"] },
    "opener": { "executable": "/usr/bin/open", "argv": [] },
    "previewers": [
      {
        "name": "pdf-viewer",
        "media_types": ["application/pdf"],
        "extensions": [".pdf"],
        "command": { "executable": "/absolute/path/to/previewer", "argv": [] },
        "timeout_ms": 5000,
        "max_input_bytes": 1048576,
        "require_complete": true
      }
    ]
  }
}
```

Commands are executed directly via `exec` (never through a shell). Bare names
are resolved on `PATH`; paths containing `/` must be absolute. AMSFTP appends
the cached file path as the final argument automatically—do not add placeholders
or shell quotes. External previewers run in order after the built-in preview and
fall back to it on failure.

### Key remapping

```json
{
  "schema_version": 1,
  "keymap": {
    "bindings": [
      { "context": "visual", "input": "n", "action": "down" }
    ]
  }
}
```

Remapping supports `normal` and `visual` contexts and moves one remappable
action to a single printable character. Digits, control keys, multi-key
sequences (like `gg`, `gs`), and destructive actions are reserved. Inspect or
reset overrides with:

```sh
amsftp config print-effective-keymap
amsftp config reset-keymap --yes
```

### All setting groups

| Group | Controls |
| --- | --- |
| `listing` | Directory page sizes |
| `cache` | Global and per-workspace cache quotas |
| `transfer` | Concurrency, queue size, bandwidth limits, verification, sync |
| `preview` | Preview read, render, JSON, and image limits |
| `search.filename` / `search.content` | Depth, entry, byte, time, and result limits |
| `retry` | Reconnect and job retry delays |
| `integrity` | Transfer integrity requirement |
| `diagnostic` | Private log rotation and in-memory log buffer |
| `external` | Editor, opener, and external previewers |
| `keymap` | Normal and Visual key overrides |
| `ipc` | Max message size between client and daemon |

Run `amsftp config print-effective` to inspect all defaults. Most numeric limits
can only be tightened below built-in safety limits. `helper.enabled` and
`direct_transfer.enabled` must stay `false` in public builds.

## Default keybindings

**File pane:**

| Key | Action |
| --- | --- |
| `h` / `j` / `k` / `l` (or arrows) | Parent / down / up / enter or preview |
| `gg` / `G` | Jump to first / last loaded item |
| `Tab` | Switch active pane |
| `g` | Enter an absolute path |
| `c` | Switch host (`local` or SSH alias) |
| `/` | Fuzzy-jump in current directory |
| `f` / `g/` | Recursive filename / content search |
| `s` / `H` / `R` | Cycle sort / toggle hidden / refresh |
| `Space` / `v` / `V` | Mark / Visual / Visual Line select |
| `y` / `d` / `p` | Copy / cut / paste |
| `r` / `D` | Rename / delete (confirmed) |
| `.` | Repeat last repeatable action |
| `e` / `o` / `E` | Edit / open / edit recovery |
| `K` / `J` / `L` | Preview / Jobs / Log drawer |
| `S` | Save workspace |
| `!` | Run one confirmed command in active directory |
| `gs` / `gS` | Interactive shell / remote home shell |
| `gp` / `gc` / `gC` | Cycle cache policy / clean workspace / clean all cache |
| `Esc` / `q` | Close prompt or unfocus drawer / quit TUI |

**Preview drawer (`K`):** `j`/`k` scroll, `h`/`l` or `gg`/`G` head/tail range,
`r` cycle view, `Esc` unfocus, `K` close.

**Jobs drawer (`J`):** `j`/`k` select job, `P`/`U`/`C` pause/resume/cancel,
`w`/`x`/`a` resolve one conflict (overwrite/skip/auto-rename), `W`/`X`/`A` apply
to matching conflicts in the same job.

## CLI commands

```text
amsftp [<location> [<location>]]
amsftp --workspace <name>
amsftp --help | --version

amsftp daemon start | status [--format human|json]
amsftp daemon stop --confirm stop [--format human|json]
amsftp upgrade [--format human|json]

amsftp job list [--limit <1..100>] [--format human|json]
amsftp job events <job-id> [--after <seq>] [--limit <1..100>] [--format human|json]
amsftp job pause | resume <job-id> [--format human|json]
amsftp job cancel <job-id> --confirm <same-job-id> [--format human|json]

amsftp config validate | print-effective | print-effective-keymap [<abs-path>]
amsftp config reset-keymap --yes [<abs-path>]

amsftp doctor [--endpoint <ssh-alias>] [--format human|json]
amsftp helper status <ssh-alias> [--format human|json]
amsftp support-bundle preview [--format human|json]
amsftp support-bundle create --consent <sha256> --output <abs-path> [--format human|json]
amsftp completion bash | zsh | fish
```

Commands that accept `--format json` emit one versioned JSON document to stdout
on success, or a versioned JSON error to stderr on failure.

**Exit codes:**

| Code | Meaning |
| ---: | --- |
| `0` | Success |
| `1` | Internal or unclassified error |
| `2` | Invalid command usage |
| `3` | Configuration error |
| `4` | Authentication error |
| `5` | Network error |
| `6` | Conflict requiring a choice |
| `7` | Partial completion |
| `8` | Canceled by user |

See also: [User guide](guide.md), [Troubleshooting](../help/troubleshooting.md),
[`amsftp(1)`](../man/amsftp.1).
