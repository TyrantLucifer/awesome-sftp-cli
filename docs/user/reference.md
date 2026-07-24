# Keys and Commands

[简体中文](../zh-CN/user/reference.md)

This page is a compact lookup for the default TUI keys, location syntax, and public
commands. Run `amsftp --help` and `man amsftp` for the command surface installed
with your exact version.

## Location syntax

```text
/absolute/local/path
SSH-alias:/absolute/remote/path
```

Examples:

```sh
amsftp
amsftp /Users/alice/Downloads
amsftp /home/alice work:/srv/project
amsftp host-a:/data host-b:/backup
amsftp --workspace project
```

Local and remote paths must be absolute. The SSH alias comes from
`~/.ssh/config`.

## File-pane keys

| Key | Action |
| --- | --- |
| `h` / `j` / `k` / `l` | Parent / down / up / enter or preview |
| `←` / `↓` / `↑` / `→` | Fixed aliases for the same navigation |
| `Tab` | Switch active pane |
| `g` | Enter an absolute path |
| `c` | Choose `local` or an OpenSSH endpoint |
| `/` | Fuzzy-jump in the current loaded directory |
| `f` | Recursive filename search |
| `g/` | Recursive content search |
| `s` | Cycle sort order |
| `H` | Toggle hidden entries |
| `R` | Refresh |
| `Space` | Toggle a discrete mark |
| `v` / `V` | Start or end Visual / Visual Line selection |
| `y` / `d` / `p` | Copy / cut / paste |
| `r` | Rename one entry |
| `D` | Delete through the confirmed delete flow |
| `.` | Repeat the last eligible frozen operation |
| `e` / `o` | Edit / open a regular file |
| `E` | Open edit recovery |
| `K` / `J` / `L` | Preview / Jobs / Log drawer |
| `S` | Save the workspace |
| `!` | Run one reviewed command in the active directory |
| `gs` / `gS` | Shell in the active directory / explicit remote home shell |
| `gp` | Cycle the workspace cache policy |
| `gc` / `gC` | Review workspace / global cache cleanup |
| `Esc` | Cancel the innermost prompt or return focus |
| `q` | Leave the TUI; Jobs continue |

Digits are count prefixes for the safe actions that support them. Counts do not
remove conflict or destructive confirmations. Arrow keys remain navigation aliases
even when letter bindings are remapped.

Print the exact current map:

```sh
amsftp config print-effective-keymap
```

## Preview and Job keys

When Preview has focus:

| Key | Action |
| --- | --- |
| `j` / `k` | Scroll down / up |
| `h` / `l` | Read the head / tail range |
| `r` | Cycle available preview views |
| `Esc` | Return to the file pane |
| `K` | Close Preview |

When Jobs has focus:

| Key | Action |
| --- | --- |
| `j` / `k` | Select a Job |
| `P` / `U` / `C` | Pause / resume or retry / cancel when available |
| `w` / `x` / `a` | Overwrite / skip / auto-rename one conflict |
| `W` / `X` / `A` | Apply the matching conflict choice within this Job |

The drawer header is authoritative: it hides actions that are invalid for the
selected state.

## Launch and lifecycle commands

```text
amsftp [<location> [<location>]]
amsftp --workspace <name>
amsftp daemon start [--format human|json]
amsftp daemon status [--format human|json]
amsftp daemon stop --confirm stop [--format human|json]
amsftp upgrade [--format human|json]
amsftp --help
amsftp --version
```

`daemon status` only inspects the daemon; it does not start one. An explicit stop
requires the literal `--confirm stop`.

## Job commands

```text
amsftp job list [--limit <1..100>] [--format human|json]
amsftp job events <job-id> [--after <sequence>] [--limit <1..100>] [--format human|json]
amsftp job pause <job-id> [--format human|json]
amsftp job resume <job-id> [--format human|json]
amsftp job cancel <job-id> --confirm <same-job-id> [--format human|json]
```

The default list/event limit is 50 and the maximum is 100. `events --after N`
returns events with a sequence greater than `N`.

## Configuration and diagnostics

```text
amsftp config validate [<absolute-config-path>]
amsftp config print-effective [<absolute-config-path>]
amsftp config print-effective-keymap [<absolute-config-path>]
amsftp config reset-keymap --yes [<absolute-config-path>]
amsftp doctor [--endpoint <SSH-alias>] [--format human|json]
amsftp helper status <SSH-alias> [--format human|json]
```

`doctor` is read-only. `helper status` reports whether the endpoint is using
standard SFTP or an available enhancement; public builds do not provide a
supported production Helper installation path.

## Support bundle

Preview the exact local diagnostic snapshot:

```text
amsftp support-bundle preview [--format human|json]
```

The preview returns a consent digest. Review it, then create a new private archive:

```text
amsftp support-bundle create --consent <sha256> --output <absolute-path> [--format human|json]
```

The output target must not already exist and must be below an owner-private
directory. AMSFTP recomposes the snapshot and refuses creation if it changed after
preview. No command uploads the archive.

## Shell completion

```sh
amsftp completion bash
amsftp completion zsh
amsftp completion fish
```

The generated script completes commands and saved workspace names. Regenerate it
after upgrading a manual installation so that it matches the installed binary.

## Human and JSON output

Human-readable output is the default. Commands that accept `--format json` write
one versioned JSON document. Scripts should use JSON instead of parsing human
tables.

On success JSON is written to stdout. On failure the versioned error is written to
stderr and the process keeps its meaningful exit status.

## Exit status

| Code | Meaning |
| ---: | --- |
| `0` | Success |
| `1` | Internal or unclassified failure |
| `2` | Command usage error |
| `3` | Configuration error |
| `4` | Authentication error |
| `5` | Network error |
| `6` | Conflict requiring a choice |
| `7` | Partial completion |
| `8` | User cancellation |

Only commands with enough evidence select a specialized failure class. An
unclassified problem returns `1` rather than guessing.

## Related guides

- [Configuration](configuration.md)
- [Transfers and Jobs](transfers.md)
- [Troubleshooting](../help/troubleshooting.md)
- [`amsftp(1)`](../man/amsftp.1)
