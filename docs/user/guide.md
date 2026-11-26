# User Guide

[简体中文](../zh-CN/user/guide.md)

This guide walks through daily work in AMSFTP: connecting to SSH hosts,
navigating two panes, running resumable transfers, and previewing, editing, or
searching remote files. For config syntax, full keybindings, and CLI flags, see
the [Reference](reference.md).

## 1. Connect and launch

AMSFTP delegates all SSH configuration, authentication, and host-key checks to
your system OpenSSH (`/usr/bin/ssh`).

1. Add a concrete host alias in `~/.ssh/config` (wildcard `Host *` entries are
   not listed as selectable servers):

   ```sshconfig
   Host work
       HostName dev.example.com
       User alice
       IdentityFile ~/.ssh/id_ed25519
   ```

   Existing OpenSSH features—`ssh-agent`, `ProxyJump`, `ProxyCommand`, MFA
   prompts, and Kerberos/GSSAPI—work as usual.

2. Connect once with OpenSSH to accept the host key and verify login, then check
   your setup with `doctor`:

   ```sh
   /usr/bin/ssh work
   amsftp daemon start
   amsftp doctor --endpoint work
   ```

   `doctor` is read-only and never modifies files or SSH trust.

3. Launch AMSFTP with zero, one, or two locations:

   ```sh
   amsftp                                           # open the startup picker
   amsftp /local/dir                                # one local pane
   amsftp /local/dir work:/srv/project              # local + remote
   amsftp host-a:/data host-b:/backup               # remote + remote
   amsftp --workspace project                       # reopen a saved workspace
   ```

   Every path must be absolute (`/local/path` or `<ssh-alias>:/remote/path`).

### Save a workspace

Press `S` to save the current two-pane setup (both locations, active pane, sort
and filter settings, hidden-file toggle, and cache policy). Workspaces store
only the SSH alias—never passwords, private keys, or tickets. Reopen later with
`amsftp --workspace <name>` or from the startup picker.

## 2. Browse, select, and run shells

AMSFTP displays two independent file panes. The highlighted pane is active.

- **Move and open:** `h`/`j`/`k`/`l` (or arrow keys) navigate directories; `gg`
  and `G` jump to the first/last loaded entry; `g` enters an absolute path; `/`
  fuzzy-jumps within the current directory; `s` cycles sort order; `H` toggles
  hidden files; `R` refreshes. Safe movement keys accept numeric counts such as
  `12j`.
- **Switch panes and hosts:** `Tab` switches panes. `c` opens the host picker
  (`local` plus named `~/.ssh/config` aliases) for the active pane. The
  existing pane stays intact if the new host fails to connect.
- **Select files:** `Space` marks individual entries; `v` and `V` start visual
  selection; `Esc` clears selection or closes the innermost prompt. Without a
  selection, actions target the entry under the cursor.
- **Bottom drawers:** `K` opens Preview, `J` opens Jobs, and `L` opens the
  redacted diagnostic log. Press the same key while focused to close it, or
  `Esc` to return focus to the file pane.
- **Independent reconnects:** Each pane owns its own SSH session. If one remote
  server disconnects, the other pane remains usable. If a saved directory no
  longer exists after reconnecting, AMSFTP falls back to the nearest accessible
  parent and notifies you.
- **One-off commands and shells:** Press `!` to run a single confirmed command
  in the active directory, `gs` for an interactive shell in that directory, or
  `gS` for the remote home shell if the working directory cannot be entered.
  Remote shells open a fresh SSH session with agent forwarding disabled. Shell
  commands run directly under your account permissions and are not wrapped in
  AMSFTP transfer safeguards.
- **Quit safely:** Press `q` or `Ctrl-C` to exit the TUI. Active background jobs
  keep running in the daemon.

## 3. Copy, move, rename, and delete

Every file modification runs as a persistent background job managed by the local
daemon, not by the TUI process.

### Copy and move

1. Select files (`Space`, `v`, `V`) or place the cursor on an item.
2. Press `y` to copy or `d` to cut (lowercase `d` **never** deletes anything by
   itself).
3. Press `Tab` to focus the destination pane, then `p` to paste.
4. Review the source, destination, item count, and conflict choice, then
   confirm. Press `J` to watch progress.

**Safety guarantees:**

- **No half-written files:** Transfers write to a job-specific temporary file
  first and rename it to the final path only after completion.
- **Moves protect the source:** When a safe same-host rename is not available, a
  move copies, verifies, and renames the destination into place before checking
  and deleting the source. If source removal cannot be confirmed, AMSFTP keeps
  the source and marks the job *completed with source retained* so you can
  inspect both copies.
- **Symlinks are not followed:** Recursive directory copies and deletes treat
  symlinks as symlinks rather than traversing their targets.
- **Remote-to-remote copies:** Copying between two SSH hosts streams data in
  small memory chunks through your local daemon. No full local copy is stored in
  the preview cache, and no SSH credentials are copied to either server.

### Rename and delete

- **Rename (`r`):** Renames the single item under the cursor. Multi-selection is
  not accepted.
- **Delete (`D`):** Uppercase `D` is the only delete shortcut. Because standard
  SFTP has no trash bin (and AMSFTP deletes directly rather than using OS trash
  locally), deletion is permanent and requires two confirmations (reviewing the
  target list, then confirming irreversible removal). Root or changed targets
  are rejected.

### Conflicts and job controls

When a destination already exists, you can choose **Overwrite** (verifies the
target hasn't changed first), **Skip**, **Auto-rename**, or **Ask** (pauses the
job until you decide).

In the Jobs drawer (`J`), use `j`/`k` to pick a job:

- **Control:** `P` pause, `U` resume/retry, `C` cancel.
- **Resolve conflicts:** `w` overwrite, `x` skip, `a` auto-rename; uppercase
  `W`/`X`/`A` apply that choice to matching conflicts within the same job only
  (never becomes a global default).

Common job states:

- **Queued / Running / Verifying:** waiting, transferring, or checking content.
- **Waiting for auth:** renew your OpenSSH login/agent/Kerberos ticket, then
  press `U`.
- **Waiting for conflict:** pick `w`/`x`/`a` in the Jobs drawer.
- **Paused / Retry wait:** progress is saved; press `U` when ready.
- **Completed with source retained:** destination is verified, but source
  deletion could not be proven—review both sides.

You can also manage jobs from the shell via `amsftp job list|events|pause|resume|cancel`.

### Verification, sync, and resuming

Regular copies balance speed and safety using two independent settings (see
[Reference](reference.md)):

- **Default copy (`verification: "protocol"`, `durability: "none"`):** Requires
  all SFTP writes to be acknowledged, exact byte count, clean file close, and
  successful rename from the temporary file. It does not read the file back or
  force `fsync` on success.
- **Content verification (`verification: "sha256"`):** Reads back and hashes the
  temporary and final files (adds 2× read I/O on uploads).
- **File sync (`durability: "completion"` or `"checkpoint"`):** Calls `fsync`
  before renaming (`completion`, similar to `sftp put -f`) or at every
  checkpoint (`checkpoint`, roughly every 64 MiB or 5 seconds).

Moves and remote edit uploads always keep stricter SHA-256 verification and
synced checkpoints.

If a transfer is interrupted or the daemon restarts, AMSFTP does not blindly
append or truncate. It hashes the partial file against the saved source digest
before resuming—and if the final rename response was lost, it requires full
content verification even in default mode. If state is ambiguous, the job pauses
or reports a conflict rather than guessing.

## 4. Preview, edit, open, and search

### Preview files

Press `K` (or `l` on a regular file) to open Preview. Use `j`/`k` to scroll,
`h`/`l` or `gg`/`G` to inspect the head or tail of large files, and `r` to cycle
between automatic, raw JSON, and metadata views.

Preview reads small ranges rather than downloading whole files, escapes unsafe
terminal control characters, shows symlinks as metadata, and renders images only
when the terminal actively proves image-protocol support.

### Edit or open locally

- **Edit (`e`):** Downloads a temporary copy to the private cache, confirms the
  editor command, hands over the terminal, and waits for the editor to exit.
  Editor lookup order: `external.editor` in `config.json` → `VISUAL` → `EDITOR`
  → `nvim` / `vim` / `vi` on `PATH`.
- **Open (`o`):** Opens the cached copy with `/usr/bin/open` (macOS),
  `/usr/bin/xdg-open` (Linux), or `external.opener`. Because desktop apps often
  return immediately, AMSFTP never auto-uploads when the opener exits—press
  `Enter` when ready to check for changes, or `Esc` to keep the session for
  later.

Before uploading any edit, AMSFTP performs a 3-way check (original downloaded
version vs. your local edit vs. the current remote file):

| Local copy | Remote file | What AMSFTP offers |
| --- | --- | --- |
| Unchanged | Unchanged | Close cleanly (no job) |
| Changed | Unchanged | Upload, save as, skip, or keep for recovery |
| Unchanged | Changed | Refresh remote view (no upload) |
| Changed | Changed / deleted | Review conflict: explicit overwrite, save as, skip, or keep |

Before overwriting a remote file during edit upload, AMSFTP saves a hidden
backup copy of the existing remote file in the same directory. If an upload is
interrupted, press `E` to open edit recovery and inspect or resume pending
edits.

### Search filenames and contents

| Key | Behavior |
| --- | --- |
| `/` | Fuzzy-jump among entries already loaded in the current directory |
| `f` | Recursively search filenames under the active directory |
| `g/` | Recursively search file contents under the active directory (asks confirmation first over SFTP; skips binaries by default) |

Recursive searches enforce hard caps on depth, entries, bytes, time, and result
count. When a cap is hit, AMSFTP shows the matches found so far and marks the
result as **partial**. You can open, preview, or copy directly from search
results.

### Local cache policies

Cached copies used by preview, edit, and open are stored per workspace (default
limits: 2 GiB total, 1 GiB per workspace). Files currently open in a preview,
editor, opener, or pending recovery are protected from automatic cleanup.

When the `g` path input is empty, use:

- `gp` to cycle the workspace cache policy: **automatic** (`lru`, default) →
  **temporary** (`ephemeral`) → **offline** (`pinned_offline`, keeps copies for
  offline viewing; uploads still require rechecking the remote server).
- `gc` / `gC` to review and clean eligible cache entries for this workspace or
  all workspaces.

Press `S` to save the cache policy with your workspace.
