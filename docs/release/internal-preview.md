# AMSFTP 0.1.0-internal

AMSFTP INTERNAL PREVIEW  
not for redistribution  
Unsigned  
Production Helper: CLOSED  
Level 2: CLOSED

This is an owner-only, unsigned internal preview. It is not for redistribution and is not the public AMSFTP 1.0 release.

Production Helper: **CLOSED**. Level 2 direct transfer: **CLOSED**. Standard Level 0 SFTP remains the supported data path.

## Install

1. Download `amsftp-internal-preview-<commit>` from the successful CI run for the exact commit you want.
2. Verify the archive with `sha256sum -c checksums.txt` from the downloaded bundle.
3. Extract the archive matching the local OS and architecture.
4. Copy `amsftp` to an owner-controlled directory on `PATH`; optionally copy the bundled man page and shell completion file.
5. Run `amsftp --version`, then `amsftp doctor --format json` before connecting.

macOS archives are unsigned and not notarized. Do not bypass Gatekeeper for a downloaded archive whose commit and checksum you have not independently verified.

## Upgrade and rollback

Stop the daemon explicitly before replacing the binary. Preserve the state and cache directories. If the replacement cannot read persistent state, keep the database unchanged and use the documented read-only/degraded recovery path; do not delete or replace the control socket to force startup.

## Uninstall

Stop the daemon, remove only the installed binary, man page, and completion files, and retain state/cache until rollback is no longer needed.
