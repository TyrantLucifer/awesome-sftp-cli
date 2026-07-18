# Upgrade, Rollback, and Release Withdrawal

This procedure applies to immutable archives. A package manager may automate exact file placement, but it must preserve the same identity checks, daemon ordering, and state safety.

## Prepare

1. Record `amsftp --version`, `amsftp daemon status --format json`, and the current archive, `VERSION.json`, checksum, and release identity.
2. Download the new archive, `checksums.txt`, SBOM, provenance/attestation, and release notes from the same release. Verify the selected archive against `checksums.txt`; on macOS also verify the published signing/notarization identity.
3. Extract into a new directory. Keep the previous extraction and do not merge old and new files.
4. Finish or pause important Jobs, then stop the user daemon with `amsftp daemon stop --confirm stop --format json`. Do not kill an unconfirmed process or delete its socket as a substitute.

## Upgrade

1. Replace only the installed binary, man page, and generated completion using the verified new extraction. Regenerate completion from that exact binary.
2. Run `amsftp --version` and require it to match the new `VERSION.json` and release record.
3. Start the daemon with `amsftp daemon start --format json`; require the expected build and compatible protocol response.
4. Run `amsftp doctor --format json`, inspect database/migration/cache results, and exercise a representative read-only workspace before resuming mutation.
5. Keep the previous extraction and recovery evidence until important Jobs and state health are confirmed.

A migration may create a verified backup and recovery hold. Do not remove it merely because startup returned. An older binary that encounters a newer database must not mutate it; use the current binary or the documented read-only diagnosis and restore path.

## Roll back

If the new binary fails before persistent state changes, stop it, restore the previous binary/man/completion from the previous extraction, start it, and rerun daemon status plus doctor.

If migration began, state format is newer, or the effect is uncertain, stop mutation and preserve the complete owner-private state plus migration/backup evidence. Do not delete or downgrade persistent state. Use read-only diagnosis to identify the recorded restore point, then follow the migration recovery decision for that exact source/target version. Never copy a live SQLite database without its WAL-safe procedure and never overwrite an unverified destination.

Rollback is complete only when the selected binary identity, daemon protocol, database state, representative workspace, and relevant durable Jobs are consistent. Keep the failed version and reviewed support evidence for investigation.

## Withdraw a release

Stop distributing the affected immutable URLs/hashes and identify the exact versions and platforms in scope. Tell users to pause mutation, preserve state, and follow the appropriate pre-migration or post-migration rollback branch above. Do not silently replace bytes at an existing URL, reuse a tag, lower Helper high-water state, or redirect users to fixture/preview artifacts. Publish the replacement or rollback release as a new immutable identity and rehearse install, daemon, doctor, Job, and uninstall behavior before reopening the channel.
