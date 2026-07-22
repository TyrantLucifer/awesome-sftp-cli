# Uninstall AMSFTP

Uninstall is explicit and keeps user data unless the user separately chooses to remove it.

1. Stop the user daemon with `amsftp daemon stop --confirm stop --format json` and confirm `amsftp daemon status --format json` reports stopped.
2. If a final channel installed a service entry, unload only launchd label `io.github.tyrantlucifer.amsftp.daemon` or disable only systemd user unit `amsftp-daemon.service`; remove the exact channel-owned file after it is unloaded. Do not use wildcard service deletion.
3. Remove the exact installed `amsftp` binary, its exact `amsftp.1` man page, and only the `amsftp` completion files generated during installation, or uninstall Homebrew formula `amsftp`. Remove now-empty AMSFTP-owned leaf directories only; removing an extracted preview directory must not remove any parent directory.
4. Keep configuration, workspaces, Job history, recovery records, logs, cache, and a managed root marker by default. They may be needed for rollback, recovery, reinstall, or support evidence. For a managed installation, removing `bin`, man, and completion files does not imply permission to remove sibling `config`, `state`, or `cache` directories.

Optional data deletion is destructive and is not part of binary uninstall. Before deleting it, inspect and back up the platform paths documented by AMSFTP. On macOS these are below `~/Library/Application Support/io.github.tyrantlucifer.amsftp`, `~/Library/Caches/io.github.tyrantlucifer.amsftp`, and `~/Library/Logs/io.github.tyrantlucifer.amsftp`. On Linux they are the `amsftp` children of the effective XDG config, state, cache, and runtime directories. A managed installation instead uses the exact `config`, `state`, and `cache` children of its validated root; remove the root only after confirming those data and `.amsftp-root` are no longer needed. Never delete a broad home, XDG, `/tmp`, `/var/lib/amsftp-users`, or another user's managed root.

Remote Helper removal is a separate authenticated lifecycle operation. Binary uninstall must not guess remote hosts, delete an active version referenced by a Job, lower the Helper high-water mark, or remove production trust material. Until the final production distribution gates are opened, Production Helper and Level 2 remain closed.
