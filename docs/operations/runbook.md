# Operations and Recovery Runbook

Use stable JSON fields and codes for automation. Preserve the reported state before repair, and change only the boundary named by the diagnostic result.

## Read-only triage

1. Record the installed identity with `amsftp --version` and probe the daemon with `amsftp daemon status --format json`. Status does not start the daemon.
2. Run `amsftp doctor --format json`. Doctor validates configuration, owner-private paths, socket/daemon state, system OpenSSH, host-key policy, database, cache, Helper status, and disk space without repair or migration.
3. If an endpoint is involved, run `amsftp doctor --endpoint work --format json` and compare its stable code with the [troubleshooting code map](../user/troubleshooting.md).
4. Preview local diagnostic evidence with `amsftp support-bundle preview --format json`. Creation requires a matching consent digest and an owner-private destination; there is no automatic upload.

Do not paste raw paths, commands, credentials, tickets, file contents, or unreviewed logs into an issue. A support bundle is still private evidence and must be reviewed before it is shared.

## Job recovery

1. List bounded durable state with `amsftp job list --format json`.
2. Inspect one Job with `amsftp job events <job-id> --format json`; use the returned sequence as the exclusive cursor for later reads.
3. Resolve the named external condition—authentication, network, space, conflict, or capability—without changing unrelated state.
4. Resume only through `amsftp job resume <job-id> --format json`. Cancel requires the exact Job ID as confirmation and may intentionally retain a matching resumable part.

After an uncertain commit, move, delete, daemon crash, or migration failure, do not delete the database, cache, socket, part file, or recovery record. Stop new mutation, preserve the exact release identity and state directory, and use the recorded Job/recovery status. AMSFTP revalidates source, part, final, and checkpoint identities before continuing; it does not blindly replay an uncertain destructive effect.

## Workspace migration recovery

1. Stop workspace mutation with `amsftp daemon stop --confirm stop --format json` before inspecting or copying a workspace document.
2. Locate the owner-private workspace root. On macOS it is `~/Library/Application Support/io.github.tyrantlucifer.amsftp/state/workspaces/`; on Linux it is `${XDG_STATE_HOME:-~/.local/state}/amsftp/workspaces/`.
3. Preserve `<name>.json`. A successful schema-v1 migration also preserves the exact original as `<name>.schema-v1.backup` before publishing schema v2. An invalid source is never overwritten, and the migration backup is never overwritten.
4. Never edit the original or migration backup in place. To export evidence or begin repair, copy the exact bytes to a separate owner-private location and keep the copy at mode `0600`.
5. Repair only the separate copy against the strict workspace schema and retain the original evidence. With the daemon still stopped, install the repaired document under a new workspace name (ASCII letter or digit first; then only letters, digits, `.`, `_`, or `-`) and mode `0600`; do not replace either preserved file. Restart the daemon and select the new name only after it appears without a problem in the workspace picker.

If the original and `<name>.schema-v1.backup` differ, or a migration reports a backup conflict, stop. Preserve both files and escalate with the stable error and reviewed evidence rather than choosing one automatically.

## Helper and direct-transfer fallback

Use `amsftp helper status work --format json` to inspect the negotiated capability snapshot. Level 0 standard SFTP remains safe and supported when Helper is absent, disabled, incompatible, or rejected. Same-host optimizations degrade to normal SFTP behavior; a direct-transfer candidate degrades to the bounded local relay when its frozen identity, authentication, capability, or policy checks do not pass.

Production Helper remains **CLOSED**. Production Level 2 remains **CLOSED**. Do not install fixture artifacts, inject trust roots, enable credential delegation, loosen host-key policy, or copy tickets/keys to force either path.

## Escalation and rollback

Escalate with the product version, stable doctor/error code, Job ID only when its disclosure is appropriate, and a reviewed support-bundle manifest. Preserve the previous archive/extraction and state backup until the new daemon, doctor checks, and representative Jobs are healthy. For a suspected bad version or withdrawn release, stop mutation and follow the exact [upgrade and rollback procedure](../release/UPGRADE.md); do not downgrade persistent state or run an older binary against a newer database for mutation.

Binary removal follows the [uninstall guide](../release/UNINSTALL.md). It keeps user state by default so recovery and audit evidence remain available.
