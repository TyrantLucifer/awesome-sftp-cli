# Vim-first Two-pane SFTP Commander — Approved Design

- **Status**: Approved; Stage 0 implementation in progress
- **Approval date**: 2026-07-14
- **Last synchronized**: 2026-07-15
- **Audience**: product, implementation, testing, security, and future continuation sessions
- **Product / public command**: `AMSFTP` / `amsftp`
- **Repository working title**: `awesome-mac-sftp`

## 1. Executive summary

This product is a keyboard-first terminal file commander for people whose working files live across a local macOS or Linux machine and one or more Linux development hosts. It combines the speed and muscle memory of a Vim-style two-pane interface with reliable SFTP operations, local-editor and default-application workflows, durable background transfers, and full reuse of the user's existing OpenSSH setup—including Kerberos/GSSAPI authentication.

The central architecture decision is to avoid reimplementing SSH. The local daemon launches an ADR-0001 validated absolute OpenSSH executable—`/usr/bin/ssh` by default, never a `PATH` lookup—with a selected host alias and the SFTP subsystem, then attaches a Go SFTP client to the process's stdin/stdout. OpenSSH remains responsible for `~/.ssh/config`, `Include`, `Match`, host keys, `ProxyJump`, `ProxyCommand`, Kerberos, agents, tokens, and security policy. The application owns structured filesystem operations, presentation, job durability, conflict handling, preview/cache behavior, and optional capability negotiation.

The product is delivered as vertical slices. Standard SFTP always works without remote deployment. Advanced remote behavior is provided by an explicitly approved, versioned, unprivileged helper invoked over SSH stdio; failure or absence of that helper must degrade to normal SFTP rather than break access.

## 2. Users, jobs, and principles

### 2.1 Primary user

A terminal-comfortable developer on macOS or Linux, often a Vim user, who works on local files and multiple SSH development machines. Their environment may rely on Kerberos/GSSAPI, bastions, `ProxyCommand`, hardware-backed keys, agents, or organization-managed OpenSSH configuration. They value keyboard speed but will not trade away transfer integrity or policy compliance.

The first user is the project owner. Defaults therefore optimize for a Vim workflow and real daily use before pursuing broad configurability.

### 2.2 Core jobs

- Browse any pairing of local and remote locations without remounting filesystems.
- Move focus, select items, preview, edit, copy, move, rename, and delete without leaving the keyboard.
- Open a remote file in local Vim or the platform default application, then safely synchronize intentional changes.
- Transfer very large files and trees in the background, survive UI or daemon restarts, and understand exactly what happened.
- Search filenames and content remotely with useful streaming results, while retaining a zero-install baseline.
- Reopen named workspaces that preserve pane locations, endpoints, view state, cache policy, and safe preferences.

### 2.3 Design principles

1. **Vim-first, discoverable second**: efficient Normal/Visual operations are defaults; contextual help and explicit plans make power safe.
2. **System SSH is policy**: never bypass or partially emulate the user's OpenSSH configuration and authentication chain.
3. **Durability before speed**: no optimization may weaken atomic commit, conflict checks, restart recovery, or move safety.
4. **Capability, not assumption**: probe endpoint abilities and choose an explainable route; every optimization has a safe fallback.
5. **Bounded resources**: previews, directory listings, searches, relays, logs, and queues are streamed and bounded.
6. **No unapproved application-managed remote footprint**: the helper is optional, versioned, auditable, unprivileged, and non-resident; SSH server/shell audit side effects remain environment behavior.
7. **Documentation is state**: implementation is incomplete until code, tests, matrix, verification evidence, and handoff state agree.

### 2.4 Non-goals for 1.0

- A mounted POSIX filesystem or Finder integration.
- Windows as a primary platform.
- A general embedded terminal emulator.
- A replacement SSH stack, Kerberos client, agent, secret manager, or credential delegation system.
- Collaborative multi-user job sharing or a network-accessible daemon.
- Full Vim emulation: macros, named registers, Ex commands, arbitrary mappings, and plugins are outside the initial interaction contract.
- Transparent offline editing of arbitrary uncached remote trees.

## 3. User experience

### 3.1 Launch and workspaces

Supported entry paths are equivalent:

```text
amsftp ~/Downloads devbox:/data/output
amsftp devbox:/data/output
amsftp --workspace release
amsftp
```

With no explicit locations, the client opens a fuzzy picker over saved workspaces, local locations, and hosts resolved from OpenSSH configuration. A workspace contains two independent pane locations plus view preferences, pinned cache policy, and non-secret endpoint references. Either pane may be local or connected to any remote, including two locations on the same host or two unrelated remotes.

The public binary name is finalized as `amsftp` by ADR-0006. The repository working title remains historical metadata and does not constrain the architecture.

### 3.2 Screen layout

The stable layout has two symmetric file panes and a bottom drawer with `Preview`, `Jobs`, and `Log` modes. The focused pane owns navigation and action context; the other pane is the default transfer target. Status lines expose endpoint, canonical path, selection count/size when known, capability level, connection state, and background-job summary.

Large directories render incrementally with a virtualized visible window. Loading, partial results, degraded capability, stale metadata, and errors are first-class states rather than blank panes.

### 3.3 Vim interaction contract

Initial Normal/Visual behavior:

| Key | Behavior |
|---|---|
| `h j k l` | parent, down, up, enter/navigation according to pane context |
| `Tab` | switch focused pane |
| `v` / `V` | continuous item selection modes |
| `Space` | toggle a disjoint mark |
| `y` | copy selected location references into the internal clipboard |
| `d` | mark selected location references as a move source; no deletion happens yet |
| `p` | plan and enqueue transfer into the current location |
| `D` | request actual deletion through the risk/confirmation path |
| `r` | rename |
| `.` | repeat the last repeatable action after rebuilding and validating its plan |
| `/` | filter the current listing |
| `f` | recursive filename search |
| `g/` | recursive content search |
| `K` | show or focus Preview drawer |
| `J` | show or focus Jobs drawer |
| `L` | show or focus Log drawer |
| `e` | open in `$EDITOR`, with Vim preferred by default |
| `o` | open with the platform default application |
| `!` | run a one-shot command at the focused location |
| `gs` | suspend the TUI and enter a local or remote shell at the focused location |

Counts and dot-repeat are part of the first complete interaction model. Repeat stores semantic intent, not raw keystrokes; it always reruns capability, path, conflict, and risk checks. Destructive actions cannot inherit a stale confirmation.

### 3.4 Preview, editor, and opener

Built-in preview handles bounded text, source, JSON, metadata, and file identification without downloading an entire large file. It supports range/tail reads where the provider allows them. Image preview detects terminal protocols such as Kitty graphics, iTerm2 images, and Sixel; unsupported terminals fall back to metadata or an external preview adapter.

The editor flow is:

1. acquire a cache lease for the remote object identity and baseline metadata;
2. materialize or reuse content in the local content-addressed cache;
3. suspend the terminal UI and resolve explicit config, `VISUAL`, `EDITOR`, then `nvim`/`vim`/`vi`; environment text uses a restricted no-expansion/operator lexer, PATH is discovery-only, and the selected absolute executable is direct-exec'd with the canonical cache path as an independent final argument;
4. after exit, compare local content and current remote identity/metadata;
5. if only local changed, show an upload plan; if both changed, enter conflict handling with diff/save-as/overwrite choices; if neither changed, do nothing;
6. upload through the normal temporary-file, verify, and atomic-commit pipeline.

The default-application flow uses the same lease and conflict rules. macOS uses absolute `/usr/bin/open`, Ubuntu uses `/usr/bin/xdg-open`, and custom openers use structured executable/argv configuration resolved to an absolute direct-exec target; the file path is never shell text. Because GUI launch semantics differ, platform adapters may use a watcher and an explicit return-to-application event, but they may never silently overwrite a concurrently modified remote file.

### 3.5 Shell commands

`!` executes one user-confirmed command at the focused location. Local execution sets process cwd and passes the command as the shell `-c` argument. Remote execution uses ADR-0001's fresh exact SSH argv and a byte-safe cwd bootstrap; no command bytes are sent until a byte-zero marker succeeds, then one bounded line is sent over stdin. Stdout/stderr use independent bounded rings that continue draining; cancellation may leave detached remote effects unknown and always triggers a refresh. `gs` either opens the local shell from process cwd or uses fresh `ssh -tt`: remote home has no command, while focused-cwd mode requires a separate capability probe and explicitly offers home retry on failure. The TUI transfers and restores termios, foreground process group, alternate screen, cursor, and SIGWINCH; it does not implement terminal emulation.

## 4. Process architecture

### 4.1 One distribution, four dispatch roles

The Go distribution supports four explicit dispatch roles:

- **TUI/CLI client**: input, rendering, fuzzy selection, external editor/opener coordination, and user prompts.
- **Local per-user daemon**: sessions, workspace state, directory/search streams, cache, persistent jobs, scheduling, checkpoints, and logs.
- **Short-lived askpass broker entry**: carries one bounded OpenSSH authentication challenge between the daemon/client boundary; it is not a resident service and never persists the answer.
- **Remote helper invocation**: an optional versioned executable run on demand through SSH stdio; it never listens or remains resident.

One Go program exposes explicit client, daemon, askpass, and remote-helper roles. Releases may contain OS/architecture-specific builds of that same program so a macOS client can install the matching Linux helper build, but there is no separate helper codebase or independently designed helper application. Role entry points do not change the trust boundaries.

### 4.2 Client-daemon IPC

The daemon auto-starts on demand and listens only on a per-user Unix domain socket with owner-only permissions. Runtime paths and peer checks are frozen by ADR-0007. ADR-0005 freezes the wire format as a 4-byte uint32 big-endian length prefix, an 8 MiB maximum JSON payload, base64 for raw path bytes, and epoch+sequence event cursors; changing that encoding requires a new versioned ADR and compatibility tests rather than an implementation-time choice.

The TUI contains no durable job authority. Closing or crashing the TUI does not cancel background work. On reconnect, the client fetches snapshots and resumes event streams from a durable or declared replay cursor.

### 4.3 Daemon services

- **Workspace Service**: endpoints, pane locations, view state, safe preferences, cache mode.
- **Session Manager**: lifecycle of local providers, SSH/SFTP child processes, helper negotiation, reconnect, and auth state.
- **Operation Planner**: canonicalization, capability checks, route choice, conflict/risk analysis, and immutable plan snapshots.
- **Transfer Engine**: persistent queue, bounded workers, checkpoints, retries, verification, commit, cleanup, and move source retention.
- **Preview and Cache Service**: bounded reads, leases, content addressing, LRU/pinning, quota, and safe materialization.
- **Search Service**: current-list filters plus cancellable streaming recursive filename/content search.
- **Auth Broker**: bridges OpenSSH prompts to an attached trusted client without retaining responses.
- **Observability Service**: structured, redacted events and diagnosable route/capability decisions.

### 4.4 Authentication broker

The daemon spawns the ADR-0001 validated absolute system OpenSSH binary; it never parses, imports, or stores private keys, Kerberos tickets, agent material, passwords, or one-time codes. To support a background process without a controlling terminal, the distribution exposes a short-lived `SSH_ASKPASS` broker entry point and launches SSH with the platform-required environment. The broker forwards a structured prompt over the owner-only local IPC channel to an attached TUI, receives one response, writes it to SSH, then discards it.

When no trusted client is attached, a job or session enters `waiting_auth`. It does not retry prompts in a loop, write secrets to logs, or fall back to an application credential store. Host-key prompts and changed-host-key failures remain OpenSSH decisions and are displayed distinctly.

## 5. Domain model

### 5.1 Core entities

- **Endpoint**: stable ID, provider kind (`local`, `sftp`, future provider), connection reference, display label, discovered capabilities, and policy metadata. It contains no credentials.
- **Location**: `EndpointID` plus provider-canonical absolute path. Equality never relies on display strings.
- **PaneState**: location, focus, sort/filter, cursor anchor, marks, listing generation, and view preferences.
- **Workspace**: two PaneStates, endpoint references, drawer state, cache policy, and safe defaults.
- **ItemRef**: endpoint, canonical path, object type, and best-known identity/metadata snapshot used for planning.
- **Clipboard**: ordered ItemRefs plus semantic operation (`copy` or `move`). `d` creates a move intent; it does not mutate the source.
- **OperationIntent**: user request before route and risk are resolved.
- **OperationPlan**: frozen sources, destination, route, capabilities, conflict policy, verification policy, risk class, and estimated work.
- **Job**: durable execution of a plan with state, phase, progress, checkpoints, attempts, and structured events.
- **CacheLease**: ownership and lifetime contract for materialized content, baseline remote identity, and dirty state.

Canonical paths preserve provider semantics and raw names. Display escaping is separate from path identity. No command is assembled by concatenating display paths.

### 5.2 Provider and capability model

Providers expose behavior-oriented operations such as list stream, stat, range read, write temp, rename, remove, and optional search/hash/copy/watch operations. Capability discovery is explicit and scoped by endpoint/session. The planner queries capabilities; UI code never assumes them based on provider type alone.

Capability levels are a presentation summary, not an inheritance hierarchy:

- **Level 0 — Standard SFTP**: browse, metadata, basic mutation, streaming transfer/resume where safe, local relay, and bounded recursive walking.
- **Level 1 — Helper enhanced**: fast walk, content search, strong hash, disk statistics, tail/watch, and same-host server-side copy.
- **Level 2 — Direct transfer eligible**: additional route/network/auth preconditions for carefully controlled remote-to-remote direct execution.

Any helper protocol/version/capability error removes only the affected capability and leaves Level 0 available.

## 6. SSH and SFTP transport

For an SSH-config host alias, the daemon launches this exact argv contract:

```text
<validated-absolute-ssh-path> -T -oEscapeChar=none -oForwardAgent=no -oForwardX11=no -oPermitLocalCommand=no -oClearAllForwardings=yes -oRemoteCommand=none -oStdinNull=no -oForkAfterAuthentication=no -oTunnel=no -oGSSAPIDelegateCredentials=no -s <validated-host-alias> sftp
```

The default executable is `/usr/bin/ssh`; an enterprise override must be an explicit real absolute path whose complete owner/mode/ACL/no-symlink chain and final special bits are revalidated before every launch. Poisoned `PATH` cannot select it. Stdin/stdout connect to a Go SFTP client and stderr is bounded/redacted. Fixed arguments prevent TTY, escape processing, forwarding, local/remote commands, backgrounding, tunnels, null stdin, and new GSS credential delegation. They retain host-key, endpoint, GSSAPI/Kerberos authentication, local-agent use, IdentityFile, proxy, Match/Include, and ControlMaster policy. `ForwardAgent=no` does not prevent local-agent authentication; `GSSAPIDelegateCredentials=no` does not disable GSSAPI authentication. Standard SFTP may reuse a user ControlMaster; delegation/forwards already present on that transport remain an explicit trusted-user-config boundary and cannot be claimed as revoked. Helper and remote `!`/`gs` instead force fresh transports with ControlMaster/Path/Persist and GSS delegation disabled, as frozen by ADR-0001/0010.

This design supports Kerberos/GSSAPI when the user's OpenSSH client and environment support it. The application does not promise to make an invalid or expired ticket work; it reports OpenSSH's auth state and can wait for user action. Connection pooling is endpoint- and policy-aware. Idle sessions may close without invalidating durable jobs; reconnect resumes only from a safe checkpoint.

Alternatives rejected:

- Driving interactive `sftp`, `scp`, or `rsync` text output is too brittle for structured progress, conflict semantics, and recovery.
- A pure-Go SSH stack would duplicate OpenSSH config evaluation, Kerberos, proxy, agent, token, and host-key behavior and would create a second policy surface.

## 7. Operations, jobs, and safety

### 7.1 Planning and confirmation

`p`, delete, rename, editor upload, and repeated actions create an OperationIntent. The daemon canonicalizes current objects, probes capabilities, chooses a route, evaluates conflicts, assigns a risk class, and returns an OperationPlan. Ordinary non-overwriting copy may enqueue immediately after a compact plan display. Overwrite, irreversible delete, direct cross-host execution, ambiguous identity, and policy-defined high-risk operations require explicit confirmation.

The plan is frozen when queued. Environmental drift either pauses the job for a new decision or triggers only a predeclared safe fallback. It never silently broadens destructive scope.

### 7.2 Job state model

Primary progression:

```text
draft -> awaiting_confirmation -> queued -> running -> verifying -> completed
```

Additional control, wait, and terminal states are `paused`, `waiting_auth`, `waiting_conflict`, `retry_wait`, `failed`, and `canceled`. A safely copied move whose source cannot be verified/deleted ends as `completed_with_source_retained`, not `completed`. State transitions and phase checkpoints are transactional in SQLite.

Retries are automatic only for idempotent phases. After an uncertain commit, rename, or delete result, the engine inspects postconditions before deciding whether to continue. It never repeats a destructive call merely because the response was lost.

### 7.3 Destination commit protocol

Unless a provider exposes an equivalent stronger primitive:

1. create a destination sibling with a job-scoped `.part-<job-id>` name using exclusive or safely reconciled semantics;
2. stream bounded chunks while persisting resumable checkpoints at controlled intervals;
3. flush/close and verify size plus the configured integrity level;
4. revalidate target conflict assumptions;
5. atomically rename the temporary object into the final name;
6. record commit before cleanup or move-source mutation.

Cancellation keeps a validated resumable partial by default, subject to cache/remote cleanup policy. The user can explicitly discard it. Orphan reconciliation is deterministic and never treats an unverified partial as complete.

### 7.4 Move semantics

Same-endpoint moves prefer an atomic provider rename when semantics and target policy permit. Otherwise every move is copy, verify, commit, then source delete. Cross-endpoint source deletion starts only after the destination commit is durable and the source identity still matches the planned object. If strong verification is unavailable, the source is retained and the result says why.

### 7.5 Conflict and delete behavior

Target conflicts pause with options such as skip, replace, keep both, compare when possible, and apply to remaining conflicts in this job. “Apply all” is job-scoped and constrained to the same conflict class.

Delete prefers an endpoint trash capability when available. Standard SFTP deletion is clearly labeled irreversible and requires a preview of scope plus confirmation appropriate to risk. Recursive deletion is bounded, cancellable, and records partial outcomes.

### 7.6 Route selection

The planner considers routes in this order:

1. same-endpoint atomic rename or helper/server-side copy;
2. explicitly approved direct helper transfer when every preflight passes;
3. daemon-mediated bounded-memory relay.

Direct preflight covers source/destination capability versions, network reachability from the source, destination space/permissions, target conflict policy, and authentication that already exists noninteractively on the source. The product does not enable agent forwarding, forward Kerberos credentials, or copy keys by default. A missing direct prerequisite is a normal, observable reason to relay.

If direct execution fails before final commit and the source remains unchanged, the job may downgrade to relay according to the frozen plan. Otherwise it pauses for inspection. Direct and relay routes share the same observable conflict, verification, and final-state contract.

## 8. Cache and persistence

SQLite stores schema-versioned workspaces, endpoint references, immutable job plans, job state/checkpoints, cache metadata/leases, and bounded diagnostic events. Durable updates use transactions that make restart recovery unambiguous. Database migrations are forward-tested and backed up/recoverable under the release policy.

Content bytes live in a separate owner-only cache, addressed by verified content identity when available. Default cache mode is short-lived LRU with quotas. A workspace may select ephemeral isolation or explicitly pin content for offline use. Pinned content is never evicted by normal LRU. Leases prevent eviction while preview, editor, opener, or upload owns a materialization. Startup reconciles stale leases and incomplete materializations without discarding dirty content.

The application stores no passwords, private keys, tokens, agent sockets, Kerberos tickets, or askpass responses. Paths, filenames, commands, and server messages are sensitive operational data and must be redacted or bounded in diagnostics according to configuration.

## 9. Search and large-scale behavior

`/` filters the current listing in the daemon and preserves cursor anchors as results change. `f` streams recursive filename results. `g/` streams content matches with file, line/range, bounded excerpt, and partial/degraded markers.

Without a helper, recursive walking uses bounded SFTP concurrency, cycle-safe rules, cancellation, result budgets, and explicit “slow/limited” status. Content search may be restricted by file size/type and remote-read budget. With a helper, `rg`-compatible structured output is used when safely available, with an internal scanner fallback; user patterns and paths are protocol fields, never interpolated shell fragments.

The target envelope is:

- a single directory with tens of thousands of entries;
- a tree with millions of paths;
- files of hundreds of gigabytes;
- continuous job and search activity without unbounded TUI, daemon, helper, or log memory.

Meeting the envelope requires incremental listing, virtualized rendering, bounded queues, backpressure, cancellation propagation, batched persistence, rate-limited progress events, and scale fixtures. It does not require loading a complete tree or file into memory.

## 10. Optional helper lifecycle

Helper installation is an explicit operation:

1. validate the current-policy canonical manifest/signature, revocation/denylist, protocol/min-client, and release floor without reading artifact bytes or touching the remote install tree;
2. obtain preliminary consent for the Endpoint, `shared-session-stable-home` assertion, read-only binding probe, and possible sshd/audit side effects, explicitly stating that actual uid/home/path are not yet known; cancellation means zero probe;
3. use fresh validated-absolute SSH and root-owned utilities to obtain non-root uid/cwd/OS/arch, treating cwd/RealPath only as compatibility preflight; then match target and apply Endpoint high-water;
4. stream-verify the local artifact, derive the safe home/content-addressed path, enforce 255-byte components/1000-byte app paths, and read-only validate ancestors/create plan;
5. obtain final consent showing the fresh observed non-root numeric uid (without inventing a username), target/path/create list/modes/hash/key/capabilities, and trust limits; cancellation or plan drift means zero app-tree create/content and requires a new probe/confirmation;
6. only then create directories and exclusive-create the 44-byte CSPRNG temp, verify handle/path mode `0600` before the first byte, upload, client-readback expected+1, chmod `0700`, and standard no-replace publish; before every exec rerun current policy, fresh binding/high-water, ancestor/final/hash;
7. launch with ADR-0010's fresh-transport restricted command string—GSS delegation and ControlMaster reuse disabled—then require byte-zero preface and the 65536-byte stderr cap. User paths/search options travel only as framed stdin data; same-uid remote environment remains a trust boundary.

The helper refuses remote uid 0 and never listens on a port, installs a service, escalates privileges, edits shell startup files, or runs outside the non-root SSH user's permissions. Multiple compatible versions may coexist during upgrades. Version mismatch, missing executable, custom/forced shell behavior, namespace mismatch, banner, crash, truncated output, or unsupported capability is reported and downgraded without breaking SFTP browsing. Remote root/admin, same-uid processes, login shell/startup configuration, shared-home mapping, ACL/LSM/export policy, and a malicious server remain explicit trust boundaries.

Helper messages are length-bounded, versioned, cancellable, and treated as untrusted remote input. The daemon validates every path, size, checksum, sequence, and capability claim.

## 11. Security and observability

Trust boundaries are the interactive user, local client, owner-only local daemon, system OpenSSH process, remote SFTP server, optional remote helper, external editor/opener, and filesystem content. The design applies least authority at each boundary.

Minimum controls:

- owner-only socket, database, cache, logs, and helper installation;
- peer-credential checks where the platform exposes them;
- no network listener in the local daemon or helper;
- safe process argument vectors rather than shell concatenation;
- bounded protocol frames, stderr, previews, search excerpts, and logs;
- explicit host-key/auth failures from OpenSSH;
- secrets excluded from persistence and structured events;
- job-scoped confirmation and immutable destructive scope;
- checksum/signature verification for distributed helper artifacts;
- visible capability level, route, downgrade reason, retry, and partial result state.

Every job exposes a human-readable plan plus structured events: chosen route, capability evidence, phase transitions, bytes/items, retry reason, conflict decision, verification level, final commit, cleanup, and retained source/partial artifacts. Diagnostics must help reproduce failures without requiring secret-bearing trace logs.

## 12. Delivery stages

### Stage 0 — Foundation and Knowledge

Create the Go/project skeleton, macOS/Linux CI, protocol envelope, provider contracts and fakes, decision/document checks, and test harness foundations. Exit when both platforms build and the contracts can drive isolated tests.

### Stage 1 — Read-only Explorer

Deliver daemon auto-start, Unix IPC, local and system-OpenSSH/SFTP providers, SSH host picker, arbitrary endpoint panes, saved workspaces, Vim navigation, and bounded text preview. Exit with local/local, local/remote, and remote/remote browsing plus real SSH/ProxyCommand/Kerberos recovery evidence.

### Stage 2 — Durable Transfers

Deliver `y/d/p`, persistent jobs, safe copy/move, local relay, conflicts, temporary atomic commit, pause/cancel/resume, and daemon restart recovery. Exit under kill, disconnect, short write, permission, and disk-full faults without target corruption or premature source deletion.

### Stage 3 — Preview, Edit, and Cache

Deliver Preview/Jobs/Log drawers, syntax/image adapters, cache modes and leases, Vim/default-app workflows, upload conflict handling, `!`, and `gs`. Exit with macOS/Linux workflow evidence, quota/recovery tests, and no silent concurrent overwrite.

### Stage 4 — Search and Helper

Deliver approved helper lifecycle, recursive filename/content search, hash, disk stats, tail/watch, same-host copy, cancellation, and budgets. Exit with version mismatch/crash downgrade and million-path bounded search evidence.

### Stage 5 — Direct Transfer and Scale

Deliver safe same-endpoint fast paths, cross-host preflight/direct execution, relay fallback, integrity policies, concurrency/bandwidth controls, huge-directory virtualization, and performance work. Exit with direct/relay semantic parity and bounded 100 GB/million-tree fixtures.

### Stage 6 — Hardening and 1.0 Release

Deliver complete configuration/keymaps, migrations, compatibility/security review, diagnostics, completions/man pages, macOS/Linux packages, install/upgrade paths, and release documentation. Exit only with the full matrix accounted for and all release gates green.

Detailed scope and commands live in [the implementation plan](../../../IMPLEMENTATION_PLAN.md) and [stage specifications](../../stages/).

## 13. Verification strategy

The validation ladder combines:

- deterministic unit and provider contract tests;
- job/state-machine property tests and protocol/parser fuzzing;
- `go test -race` for concurrency-sensitive packages;
- integration fixtures with ephemeral OpenSSH SFTP servers and `ProxyCommand`;
- a reproducible MIT Kerberos realm/client/server path for GSSAPI authentication;
- helper protocol/version/capability matrices;
- macOS and Linux editor/opener/shell smoke tests;
- fault injection for disconnects, short writes, stalls, disk full, permission changes, stale metadata, daemon/helper kill, uncertain commit responses, and corrupted checkpoints;
- scale fixtures for 50k-entry directories, million-path trees, and sparse 100 GB files;
- clean install, migration, upgrade, downgrade-policy, and package verification.

Tests assert behavior and invariants rather than internal implementation. The detailed policy is in [testing strategy](../../testing/strategy.md).

## 14. Documentation continuity contract

The durable truth chain is:

```text
Vision -> Approved Design -> Feature Matrix -> Active Stage Spec
       -> Implementation + Verification Evidence -> Project State
```

Stable feature IDs connect user promises to stages, tests, and release evidence. A feature may be marked `Verified` only when code, focused tests, stage gates, feature-matrix evidence, and `PROJECT_STATE.md` agree. Every stage completion updates all five surfaces in the same reviewable change.

New sessions follow [the documentation map](../../README.md). They do not reconstruct intent from chat history. Architecture changes use additive ADRs that identify superseded decisions; old ADRs remain as history.

## 15. Decisions intentionally gated to Stage 0

These choices do not alter the approved product design, but they depend on repository setup or current ecosystem evidence and therefore must be resolved before Stage 1:

- public product/binary name and Go module path;
- exact supported Go version and pinned dependency versions;
- TUI/rendering library and structured logging package;
- versioned IPC framing details and storage/config paths;
- SQLite driver and migration mechanism;
- CI providers, packaging identifiers, and minimum macOS/Linux versions;
- helper artifact signing/distribution mechanism before helper work begins.

Each choice requires a short comparison, a testability/security check, and an ADR when it constrains compatibility. None authorizes a change to the approved system-OpenSSH, daemon, transfer-safety, or helper trust boundaries.

These choices were resolved on 2026-07-15 by [ADR-0006 through ADR-0010](../../architecture/overview.md). The ADRs freeze public identity and exact dependency pins, tcell/slog/SFTP boundaries, platform paths and IPC endpoint, pure-Go SQLite/migrations, supported OS/CI/package identifiers, and Helper Ed25519 distribution trust. They do not claim that the owning Stage 1–4 runtime features have been implemented.

## 16. Design acceptance

The design is accepted when the durable document set contains:

- this complete approved specification;
- product vision and exhaustive feature matrix with stage ownership;
- architecture overview and ADRs for the four foundational decisions;
- a seven-stage implementation plan and per-stage exit specifications;
- a cross-platform, fault, security, and scale testing strategy;
- a current project handoff stating the next action and last green evidence.

Implementation planning may start only after the written set is reviewed for contradictions, unresolved placeholders, broken links, and missing approved capabilities. Product implementation remains blocked until that review is accepted.
