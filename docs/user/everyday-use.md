# Everyday Use

[简体中文](../zh-CN/user/everyday-use.md)

AMSFTP keeps two independent file panes on screen. The highlighted pane is active;
navigation and commands apply there unless an action explicitly uses the other
pane as its destination.

## Open and switch locations

Launch with zero, one, or two locations:

```sh
amsftp
amsftp /absolute/local/path
amsftp /left/path work:/remote/path
amsftp host-a:/path/a host-b:/path/b
```

Press `Tab` to switch panes. Press `c` to choose a new endpoint for only the active
pane. The endpoint picker contains `local` and concrete OpenSSH aliases; type to
filter it, use the arrow keys to select, and press `Enter`.

Changing an endpoint is transactional from the user's point of view: the old pane
stays usable unless the new endpoint has connected and listed its first page.

## Navigate a directory

| Key | What it does |
| --- | --- |
| `j` / `k`, `↓` / `↑` | Move down or up |
| `h`, `←` | Open the parent directory |
| `l`, `→` | Enter a directory or preview the selected file |
| `g` | Enter an absolute path for the active pane |
| `/` | Fuzzy-find an entry already loaded in this directory |
| `s` | Cycle the sort order |
| `H` | Show or hide hidden entries |
| `R` | Refresh the current directory |
| `Tab` | Switch the active pane |

For `/`, type a query and use Up/Down to choose a match. `Enter` moves the cursor
to that entry in the complete listing; `Esc` returns to the previous cursor. This
is a quick jump inside the current directory, not a recursive search.

A numeric prefix works with safe navigation and selection actions. For example,
`12j` moves down twelve entries. Counts never remove a confirmation from a
destructive action.

Directory listing and rendering are intentionally paged. Very large directories
remain responsive instead of being loaded and rendered all at once.

## Select files

- `Space` toggles a discrete mark.
- `v` starts or ends a visual selection.
- `V` starts or ends a line-oriented visual selection.
- `Esc` leaves the current selection or closes the innermost prompt.

With no selection, file actions use the entry under the cursor. When several items
are selected, the action bar shows only operations that apply to the whole
selection.

`y` captures a copy, `d` captures a move, and `p` pastes into the other pane.
Lowercase `d` never deletes a file. Uppercase `D` is the separate, confirmed
delete action.

See [Transfers](transfers.md) before moving or deleting important data.

## Use the action bar and drawers

The action bar above the status line follows the selected object and your effective
keymap. It is often the quickest way to discover what can be done next.

| Key | Drawer |
| --- | --- |
| `K` | Preview |
| `J` | Background Jobs |
| `L` | Redacted diagnostic log |

Press the drawer key again while it has focus to close it. `Esc` returns focus to
the file pane while leaving the drawer visible. Only one bottom drawer is active
at a time.

The Jobs drawer adapts to terminal width. On wide terminals it shows the selected
Job's details beside the list; on narrow terminals it uses a compact path summary.
The header shows only controls that are valid for the selected Job.

## Save a workspace

Press `S` to save the current two-pane setup. Workspaces remember locations, the
active pane, sort/filter choices, hidden-file visibility, and cache policy.

Open one later with:

```sh
amsftp --workspace <name>
```

The startup picker also lists saved workspaces. If a workspace file is damaged,
AMSFTP leaves it untouched and returns to the picker rather than overwriting it.

Remote locations store the OpenSSH alias, not credentials or expanded SSH
configuration.

## Reconnect and refresh

Each pane owns its own connection. If one SSH server goes away, that pane reports
the problem and retries only failures that are safe to retry. The other pane stays
available.

After reconnecting, AMSFTP checks that the directory still exists. If necessary,
it opens the nearest accessible parent and reports the fallback. Press `R` whenever
you want an authoritative relist.

If the local daemon restarts, the TUI waits for the private instance handoff,
reconnects both panes, and ignores late results from the old connection. Durable
Jobs are recovered separately from the screen state.

## Run a command or shell

- `!` runs one reviewed command in the active pane's directory.
- `gs` opens an interactive shell in that directory.
- `gS` explicitly opens the remote account's home shell when a remote working
  directory cannot be established.

AMSFTP shows a confirmation before a one-time command. Remote commands use a new
OpenSSH connection and do not enable agent forwarding or credential delegation.
Shell output is not added to persistent logs.

Treat these as normal shell access: the command can change files using your
account's permissions and is not protected by AMSFTP's transfer confirmations.

## Leave AMSFTP

Press `q` or `Ctrl-C` to restore the terminal and leave the client. This does not
cancel background Jobs. Use the Jobs drawer or `amsftp job` commands when you
intend to pause, resume, or cancel work.

The minimum usable terminal is 20 columns by 5 rows; a larger terminal provides a
much clearer two-pane view.
