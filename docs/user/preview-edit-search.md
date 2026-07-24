# Preview, Edit, Open, and Search

[简体中文](../zh-CN/user/preview-edit-search.md)

AMSFTP can inspect a remote file without downloading it in full, materialize a
verified local copy for another program, and search a remote tree through standard
SFTP. These workflows share a managed local cache, but none of them silently
uploads a changed file.

## Preview a file

Select a file and press `K`, or press `l` while the file is selected. Directories
still open normally with `l`.

Inside Preview:

| Key | Action |
| --- | --- |
| `j` / `k` | Scroll one rendered line |
| `h` / `l` | Read the head or tail range |
| `r` | Cycle automatic, raw JSON when available, and metadata views |
| `Esc` | Return focus to the file pane |
| `K` | Close Preview while it has focus |

Preview reads files in small ranges and clearly marks partial content. Large text
files are not loaded in full. Binary and unsafe terminal-control bytes are
sanitized or shown as metadata/hex instead of being written directly to the
terminal.

Image display is used only when the terminal proves that it supports a compatible
protocol. Unsupported, damaged, or oversized images fall back to metadata rather
than emitting guessed escape sequences.

Symlinks are previewed as metadata without following the target.

## Edit with a terminal editor

1. Select a regular file and press `e`.
2. Review the resolved editor command and confirm it.
3. Edit the local materialized copy and exit the editor.
4. Review what changed locally and remotely.
5. Choose upload, save as, skip, refresh, or retain for recovery.
6. Confirm the final sync-back step if you chose to write.

Editor selection follows this order:

1. `external.editor` in AMSFTP configuration;
2. `VISUAL`;
3. `EDITOR`;
4. `nvim`, `vim`, then `vi` found on `PATH`.

The file path is passed as one final argument. AMSFTP does not build a shell command
from the filename.

After the editor exits, AMSFTP compares both the local materialization and the
remote file with the original. Typical outcomes are:

| Local file | Remote file | What AMSFTP offers |
| --- | --- | --- |
| Unchanged | Unchanged | Finish without a Job |
| Changed | Unchanged | Upload, save as, skip, or retain |
| Unchanged | Changed | Refresh; no upload is inferred |
| Changed | Changed/deleted/replaced | Review the conflict, overwrite explicitly, save as, skip, or retain |
| Cannot be verified | Any state | No upload; retain or abandon explicitly |

For an overwrite, AMSFTP preserves the observed remote original under a hidden,
Job-owned sibling name before publishing the edited version. It does not
automatically delete that safety copy. A second remote change causes another
conflict instead of being overwritten.

If an upload is interrupted, the dirty local copy and its baseline remain available
for recovery. Press uppercase `E` to open edit recovery, select a session with
`j`/`k`, inspect it with `K`, and press `Enter` to check or resume it.

## Open with the default application

Press `o` to materialize the file and open it with the platform application:

- macOS: `/usr/bin/open`;
- Linux: `/usr/bin/xdg-open`.

A configured `external.opener` can replace that default.

Desktop openers often return before the application closes. AMSFTP therefore does
not treat process exit or a changed timestamp as permission to upload. Press
`Enter` when you are ready to check for changes, or `Esc` to keep the session for
later recovery. Any write-back uses the same comparison, confirmation, and Job
flow as terminal editing.

## Find an entry or search a tree

AMSFTP provides three different searches:

| Key | Scope |
| --- | --- |
| `/` | Fuzzy-jump among entries already loaded in the current directory |
| `f` | Recursively search filenames below the active directory |
| `g/` | Recursively search file contents below the active directory |

Filename and content results stream into a search view. You can open, preview, or
copy a result using the normal file actions.

Standard SFTP content search must read file ranges over the connection, so AMSFTP
shows a warning and asks for confirmation before starting it. Binary files are
skipped by default.

Every recursive search has limits for depth, entries, files, bytes, duration, and
result count. Reaching a limit returns the results found so far and marks them as
partial. Canceling also keeps the results already displayed. Search results are
snapshots; opening or changing a result checks the current file again.

Public builds do not distribute the optional remote search accelerator. Filename
and content search continue to work through the standard, bounded SFTP path.

## Choose a cache policy

The status line shows one policy for the current workspace:

- **automatic** (`lru`): the normal default; old clean content can be evicted.
- **temporary** (`ephemeral`): content becomes eligible for cleanup sooner.
- **offline** (`pinned_offline`): newly materialized content is protected from
  normal eviction and may be reopened when the endpoint is unavailable.

While the `g` path prompt is empty:

| Sequence | Action |
| --- | --- |
| `gp` | Cycle automatic → temporary → offline |
| `gc` | Review and clean eligible content for this workspace |
| `gC` | Review and clean eligible content across workspaces |

Press `S` to persist the selected policy with the workspace.

Offline content is a retained copy, not proof that the remote file is unchanged.
AMSFTP will not authorize upload while it cannot recheck the endpoint. If more than
one remote version could match, it refuses to guess.

Cached content currently defaults to a 2 GiB global limit and 1 GiB per workspace.
Files in active preview, edit, open, upload, or recovery sessions are protected
from normal cleanup. Cleanup removes only managed objects and never a remote file
or your original local file.

## Configure an editor or opener

Use structured commands in `config.json`:

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

AMSFTP runs these commands directly, not through a shell. The materialized file
path is appended automatically, so do not add a placeholder or shell quoting.

An editor, opener, or external previewer runs with your user permissions and can
read anything that program normally can. AMSFTP does not pass it remote
credentials or the daemon socket.

See [Configuration](configuration.md) for paths, validation, and external
previewer rules.

## One-time commands and interactive shells

Press `!` to enter one command. AMSFTP shows the endpoint, working directory, and
text for a second confirmation before running it. Press `Esc` while it runs to
cancel the local process group or SSH session.

Press `gs` to open an interactive shell in the active directory. On a remote pane,
`gS` explicitly falls back to the account's home directory when the selected
working directory cannot be established.

These commands use a fresh process or SSH connection. Remote execution disables
agent forwarding, credential delegation, and persistent connection sharing.
Interactive terminal bytes and one-time command output are not persisted.

Unlike file actions, shell commands are not wrapped in transfer Jobs. Review them
as carefully as commands entered in any other shell.
