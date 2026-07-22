# Preview, Edit, Cache, and Shell Guide

AMSFTP provides bounded Preview/Jobs/Log drawers, an owner-only managed cache, local editor/default-opener workflows, durable edit recovery, and two explicit shell surfaces. It does not add a second write path: every accepted edit upload still becomes the `Intent → Plan → Job → .part → verify → commit` flow described in the [Durable Transfers guide](durable-transfers.md).

## Drawers and built-in preview

| Key | Action |
|---|---|
| `K` | Open Preview and focus it. Press `K` again while focused to close it; `Esc` returns focus to the pane while leaving the drawer visible. |
| `J` | Open/focus/close the bounded Jobs drawer. Its `P`/`U`/`C` and conflict keys retain their durable Job meanings. |
| `L` | Open/focus/close the bounded, redacted Log drawer. |
| `l` on a file | Open Preview directly; on a directory, enter the directory as before. |
| `h`, `l` in Preview | Read the 64 KiB head or tail. |
| `j`, `k` in Preview | Scroll the rendered Preview one line down or up. At a partial range boundary, request the next or previous bounded 64 KiB window. |
| `r` in Preview | Cycle automatic, raw-JSON when available, and metadata views. |

Switching drawer tabs keeps only one bottom drawer active. `K`/`J`/`L` are uppercase and do not collide with lowercase Vim navigation. Preview alone grows with the terminal from the six-row baseline to at most 16 rows, using about half of the available height; a typical 24-row terminal therefore shows ten content lines instead of four. Jobs and Log retain their fixed bounded height. The Jobs drawer uses a side-by-side list and selected-detail region at 96 columns or wider; narrower terminals keep the Job rows contiguous and reserve one bottom line for the selected source-to-target path. Job rows identify the affected file, show speed only while sampled streaming is active, collapse completed transfers to their final size, and describe deletes by item count rather than a byte percentage. Narrow terminals cap every drawer to the safe available area; resize, pane switch, cursor movement, refresh, and endpoint generation changes preserve the UI model while canceling or rejecting stale Preview results.

Every provider Preview read is at most 64 KiB and a Preview retains at most 512 KiB. Built-in rendering is capped at 512 KiB input/output, 256 KiB parsed JSON, JSON depth 64, 10,000 rendered lines, and 4,096 syntax spans. Range navigation is clamped to a readable source window, and empty files render without issuing a zero-length Provider read. Partial ranges show offsets/truncation; binary, NUL, invalid UTF-8, ANSI, and control bytes are sanitized or rendered as bounded hex instead of being written raw to the terminal. A sparse or very large file is never read in full just to preview it.

Metadata is built from bounded Provider facts rather than file-body parsing. It can show Endpoint, canonical path, object kind, size, modification time, permissions, symlink target, fingerprint strength, hash, file/version IDs, media type, captured range, and completeness. A symlink opened with `l` is shown as metadata without following or reading its target. Every external field is converted to one safe line and capped at 1,024 bytes or its smaller share of the 512 KiB output budget.

Image escape sequences are emitted only after an active terminal query returns the exact supported reply within 200 ms and 256 response bytes. Terminal-image encoding is capped at a 4 MiB payload, 6 MiB output, and 1,000,000 pixels; image metadata parsing also rejects dimensions above 40,000,000 pixels. Kitty, iTerm2, and Sixel are capability options, not environment-name assertions. Unsupported, damaged, or oversized images stay on the safe metadata/binary fallback. Capability is probed again after every external foreground handoff, so a stale proof is not reused.

Configured external previewers are considered only after the built-in result, and only for image or unknown-binary automatic views. They require a complete verified materialization and active lease, obey ordered media-type/extension rules, input and timeout limits, and direct-exec the canonical file path as the final argument. Their stdout is discarded; only a redacted single-line stderr diagnostic of at most 4 KiB may be shown in memory. Failure returns to the built-in result.

## Cache policies and cleanup

The status line always shows the current policy in user-facing terms: `Cache: automatic` for `lru`, `Cache: temporary` for `ephemeral`, or `Cache: offline` for `pinned_offline`. The default for a new workspace is `lru`.

While the `g` path prompt is empty, these second keys are cache/shell controls rather than path text:

| Sequence | Action |
|---|---|
| `gp` | Cycle `lru → ephemeral → pinned_offline → lru` for this workspace. The choice is used for new preview/edit/open materializations and is persisted when `S` saves the workspace. |
| `gc` | Review and confirm removal of eligible managed content for the current workspace. |
| `gC` | Review and confirm removal of eligible managed content across workspaces. |

`lru` uses the normal quota; `ephemeral` marks workspace content for eligible cleanup; `pinned_offline` protects newly materialized items from normal eviction but does not make a stale remote copy authoritative. Pinned content still carries fresh/stale/unknown source identity.

The default hard limits are 2 GiB globally, 4,096 managed objects counted as entries plus materializations, 1 GiB of non-shared bytes per workspace, and 256 eviction candidates per reconciliation. Even an empty materialization consumes one object slot. Before publishing an entry or handing off a materialization, AMSFTP serializes admission through the catalog commit, includes the incoming object in the calculation, and may replan/evict existing clean LRU state for at most four batches. If protected content leaves insufficient room, the request fails as `resource_exhausted` with bounded required/pinned/dirty/leased/referenced counts; content is not admitted over quota first and cleaned later. Provider short reads or size/hash mismatches are rejected while bytes are still private staging data.

Preview/edit/open/upload references, dirty or pinned materializations, active or uncertain leases, shared blobs, and transfer parts are protected. Lease heartbeat is 30 seconds, normal expiry is 2 minutes, and an immediately returning opener receives a 15-minute grace period. A PID alone is not proof that a lease owner survived; restart recovery also verifies process birth identity. A Preview whose exact PID+birth identity is already dead can be reclaimed immediately without waiting for expiry, but only with exactly one matching Preview reference. Startup uses pages of at most 256 and at most 64 batches (16,384 theoretical ceiling; the managed-object ceiling is 4,096). Edit/opener/upload references, ambiguous matches, and uncertain owners remain retained.

When an exact `pinned_offline` materialization request receives a transport-interrupted error, AMSFTP may reopen one retained entry for the same workspace and Location. It requires exactly one historical fingerprint and revalidates the derived entry ID, published blob, canonical manifest/catalog binding, strong embedded fingerprint, content SHA-256, and size. Selection, online publication, and handoff admission are serialized; handoff rechecks uniqueness, so a newly published second fingerprint makes the request fail ambiguous. The selected catalog entry and the response are both durably `unknown` freshness. No retained match leaves the original transport error, and unpinned/LRU/ephemeral requests do not use this fallback. Do not request upload/change evaluation until the endpoint has reconnected: an unavailable remote cannot authorize sync-back and leaves the local copy retained for recovery. A successful online reopen creates a fresh remote-bound entry rather than silently promoting the offline copy.

Cleanup deletes only catalog-selected objects that are revalidated immediately before deletion. It never recursively deletes unknown files and never deletes a remote file or the user's original local file. If filesystem manifests are corrupt, the catalog is damaged, the scan is truncated, or the two inventories disagree, cache mutation/cleanup fails closed with an operator-attention diagnostic while bytes are preserved. Current manifests do not contain policy, dirty, reference, and lease metadata, so AMSFTP deliberately does **not** claim to reconstruct the catalog automatically from them. Browsing and existing durable Jobs remain available while the cache is unavailable.

## Edit with `e`

1. Select a regular file and press `e`.
2. AMSFTP downloads or reuses complete verified content, creates a separate materialization, and durably records its baseline, edit session, reference, and lease. For an online file, the baseline also streams the Provider file under its expected fingerprint, computes complete SHA-256/size within the 2 GiB cache ceiling, and requires it to equal the materialization. The content-addressed blob itself is never edited.
3. Review the direct-exec confirmation. It displays the resolved absolute executable and arguments; `Enter` launches it, while `Esc` retains the materialization without launching.
4. The TUI yields the terminal to the editor. Normal, non-zero, signaled, and failed exits all use the same terminal recovery path and still cause a bounded local/remote observation when possible.
5. Choose the result explicitly. No choice means no upload.

Editor resolution order is explicit JSON config, `VISUAL`, `EDITOR`, then `nvim`, `vim`, and `vi` discovered through `PATH`. Environment editor strings use a restricted quote/backslash lexer: expansion, substitution, globbing, pipes, redirection, operators, controls, CR/LF, and NUL are rejected. `PATH` is discovery-only; the chosen regular executable is frozen as an absolute identity, revalidated before launch, and direct-executed without a shell. The canonical materialization path is always one independent final argument.

After the editor, AMSFTP streams the remote file again to derive current SHA-256/size. The result matrix is conservative, and a same-size/same-mtime replacement with different bytes is still a remote change:

| Local since baseline | Remote since baseline | Result |
|---|---|---|
| unchanged | unchanged | Finish without a Job. |
| changed | unchanged | `Enter` confirms upload; `a` chooses an absolute save-as path; `x` skips; `Esc` retains for recovery. |
| unchanged | changed/deleted/replaced | `Enter` refreshes; no upload is inferred. |
| changed | changed/deleted/replaced | Conflict: `K` inspects the remote→local diff, `w` explicitly overwrites, `a` saves as, `x` skips, and `Esc` retains. |
| unknown/unreliable | any | No upload; `x` abandons or `Esc` retains for recovery. |

The conflict view revalidates the retained local SHA-256/no-follow identity and the observed remote fingerprint. It reads at most 32 KiB from each side, renders at most 512 lines and 64 KiB, and cannot itself authorize a mutation. An overwrite is audited on the same edit session and freezes the just-observed destination as a new precondition; another remote change conflicts again.

After an upload/save-as/overwrite decision, the UI shows a frozen sync-back step. A second `Enter` queues the exact durable Plan/Job. For an existing destination, the worker never performs hash-then-replace: it first moves the remote original atomically to a job-owned hidden sibling such as `.document.txt.amsftp-original-<job-id>`, verifies that preserved file under the exact fingerprint/SHA/size and 2 GiB/15-minute bounds, then publishes the edited part no-replace. A remote change before the move is restored or retained as conflict; a new final after the move also conflicts; a writer holding the old inode continues into the preserved path. Successful and conflicting Jobs report that path, and AMSFTP does not automatically delete the safety copy. Upload failure, authentication interruption, client/daemon restart, or conflict also preserves the dirty materialization and its baseline.

Press uppercase `E` to open durable edit recovery. Use `j`/`k`, `K` to inspect the remote/diff where available, and `Enter` to resume/check the selected session. An unusable record remains listed with its diagnostic rather than being guessed or erased.

## Open with `o`

`o` uses the same verified materialization, durable session, lease, observation, conflict, and durable sync-back path. The default executable is fixed `/usr/bin/open` on macOS and `/usr/bin/xdg-open` on Linux; an explicit structured opener may replace it.

Desktop openers often return before the application closes. AMSFTP therefore shows `Opener lease retained`; `Enter` explicitly checks for changes, while `Esc` keeps the session for recovery. The 15-minute grace prevents premature eviction but is never upload authorization. A changed mtime, a returning process, or a GUI application exit never silently writes to the source.

## External configuration

With no file, safe defaults are used. A custom file is strict JSON with unknown fields rejected and must be a no-symlink, owner-private regular file (`0600`) beneath its private configuration directory:

- macOS: `~/Library/Application Support/io.github.tyrantlucifer.amsftp/config.json`
- Linux: `${XDG_CONFIG_HOME:-$HOME/.config}/amsftp/config.json`

Example:

```json
{
  "schema_version": 1,
  "ipc": {"max_frame_bytes": 8388608},
  "listing": {"default_page_size": 256, "max_page_size": 4096},
  "external": {
    "editor": {"executable": "vim", "argv": ["-f"]},
    "opener": {"executable": "/absolute/path/to/opener", "argv": []},
    "previewers": [
      {
        "name": "pdf-viewer",
        "media_types": ["application/pdf"],
        "extensions": [".pdf"],
        "command": {"executable": "/absolute/path/to/previewer", "argv": []},
        "timeout_ms": 5000,
        "max_input_bytes": 1048576,
        "require_complete": true
      }
    ]
  }
}
```

Commands allow at most 128 arguments, 4,096 bytes per item, and 32,768 bytes total. At most 32 previewer rules and 64 combined match items per rule are accepted; `timeout_ms` is at most 120,000 and `max_input_bytes` at most 1 GiB. A bare executable is resolved once through `PATH`; a configured value containing a slash must be absolute. Resolution follows it to a canonical regular executable, freezes that final identity, and revalidates the final path before launch. The file path is appended automatically—do not add a path placeholder or shell quoting.

An editor/opener/previewer is user-authorized same-UID code. It can read the handed-off cache file and whatever that program normally has permission to access, but AMSFTP does not pass the daemon socket or remote credentials to it.

## One-time command `!` and interactive shell `gs`

Press `!`, enter one non-empty UTF-8 line of at most 32,768 bytes, press `Enter` to review Endpoint/CWD/command, then press `Enter` again to run. NUL, CR, and LF are rejected. Only one one-time command can run in a client: another request is rejected without replacing it. While it runs, Normal-mode `Esc` cancels its context/process group. Local and remote commands also stop at an absolute 15-minute timeout.

For a local pane, AMSFTP resolves an absolute `$SHELL` or `/bin/sh`, revalidates it, and direct-execs `[shell, "-c", user-text]` with the pane directory in the process `Dir`; the cwd is never prepended to command text. Stdout and stderr are continuously drained into separate 1 MiB rings. Extra bytes are discarded with a count, not allowed to block the child, and raw output is not persisted.

For an SSH pane, `!` creates a fresh `ssh -T` transport with forwarding, GSS credential delegation, and ControlMaster/Path/Persist disabled. It first verifies the required root-owned remote utility and a byte-safe quoted cwd bootstrap. AMSFTP sends zero command bytes until the stdout byte-0 marker proves cwd setup. A marker/preflight failure refuses execution. Cancellation or transport loss after bytes were sent is reported as `remote effect unknown`, and both panes are refreshed. The native acceptance harness drives the real two-Enter confirmation through a temporary `sshd`, proves the remote cwd marker and exit status, then enters and exits `gs` in the same TUI.

`gs` is the key sequence `g` then lowercase `s` while the path prompt is empty. Locally it hands the controlling TTY to the resolved interactive shell in the pane cwd, direct-executed without `-c` or generated shell text. For SSH, AMSFTP first proves the remote cwd on a separate fresh transport and then starts fresh `ssh -tt`; it does not silently fall back. If the cwd probe or shell fails, use the explicit `gS` sequence for a remote home shell (host followed by no remote command).

Interactive shell terminal bytes are never captured or logged. Foreground process group, termios, alternate/raw mode, cursor, resize state, panes, drawers, and background Job display are recovered after normal, non-zero, signal, spawn, and PTY-loss paths. If a child has spawned but process-group validation or foreground transfer fails, AMSFTP kills the complete child group and waits to reap it before restoring the TUI. That cleanup has a 2-second safety bound: an abnormal stalled adapter produces a diagnostic and the TUI restores while the one cleanup worker continues reaping in the background. These are user-only TUI surfaces: Provider operations, Preview/cache code, workspace restore, and daemon RPC cannot inject a command into them.

The Ubuntu Hosted native gate checks for `sshd`, `nvim`, `vim`, and `xdg-open`; if the runner image lacks any of them, it installs `neovim`, `openssh-server`, `vim`, and `xdg-utils` before running the real editor, isolated opener, one-time command, and foreground-PTY tests. A missing package therefore cannot turn those Ubuntu checks into a passing skip. macOS separately exercises the fixed `/usr/bin/open` path and native terminal/editor combinations.

## Privacy and current delivery boundary

AMSFTP does not persist passwords, private keys, agent contents, Kerberos tickets, askpass answers, full Preview/file bodies, command output, or interactive terminal bytes. Cache manifests contain bounded identity metadata, not credentials. Persistent logs retain only registered IDs/status/error codes; operational paths and immediate external diagnostics remain sensitive local information and should still be handled accordingly.

The Preview/Edit/Cache surfaces continue to work without a remote Helper. Recursive filename/content search and test-fixture Helper install/handshake/fast walk/hash/watch/tail contracts exist, as do unified fast-route evidence, declared server-copy, fixture-only Level 2 control/data contracts, and bounded scale gates. Ordinary builds still record `production_distribution_closed` and relay because production Helper distribution and production Level 2 remain **CLOSED**. Production signing/notarization, real Helper custody and process/network-isolated direct-data proof remain future release work. Test public-key/data fixtures do not authorize distribution.
