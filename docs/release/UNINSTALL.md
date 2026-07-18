# Uninstall AMSFTP

Uninstall is explicit and keeps user data unless the user separately chooses to remove it.

1. Stop the user daemon with `amsftp daemon stop --confirm stop --format json` and confirm `amsftp daemon status --format json` reports stopped.
2. If a final channel installed a service entry, unload only launchd label `io.github.tyrantlucifer.amsftp.daemon` or disable only systemd user unit `amsftp-daemon.service`; remove the exact channel-owned file after it is unloaded. Do not use wildcard service deletion.
3. Remove the exact installed `amsftp` binary or uninstall Homebrew formula `amsftp`. Removing an extracted preview directory must not remove any parent directory.
4. Keep configuration, workspaces, Job history, recovery records, logs, and cache by default. They may be needed for rollback, recovery, or support evidence.

Optional data deletion is destructive and is not part of binary uninstall. Before deleting it, inspect and back up the platform paths documented by AMSFTP. On macOS these are below `~/Library/Application Support/io.github.tyrantlucifer.amsftp`, `~/Library/Caches/io.github.tyrantlucifer.amsftp`, and `~/Library/Logs/io.github.tyrantlucifer.amsftp`. On Linux they are the `amsftp` children of the effective XDG config, state, cache, and runtime directories. Never delete a broad home, XDG, `/tmp`, or parent directory.

Remote Helper removal is a separate authenticated lifecycle operation. Binary uninstall must not guess remote hosts, delete an active version referenced by a Job, lower the Helper high-water mark, or remove production trust material. Until the final production distribution gates are opened, production Helper and Level 2 remain closed.
