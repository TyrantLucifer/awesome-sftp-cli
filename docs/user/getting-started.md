# First Run

This path starts with local, read-only validation and uses the system OpenSSH configuration unchanged. It does not open production Helper or Level 2 distribution.

## Before the first run

1. Confirm the supported system client with `/usr/bin/ssh -V`. AMSFTP does not replace or bypass system OpenSSH and does not accept a user-selected SSH executable.
2. Put the endpoint in `~/.ssh/config` under a plain alias such as `work`. Use `/usr/bin/ssh work` once to resolve host-key and authentication prompts directly. Keep strict host-key verification enabled; verify a changed host key out of band instead of deleting the trust store.
3. If the alias uses Kerberos/GSSAPI, run `klist` and renew the ticket through the operating environment when necessary. AMSFTP does not copy Kerberos tickets, keys, passwords, Askpass answers, or Agent contents into configuration, Jobs, logs, or support bundles.
4. Verify the installed release with `amsftp --version`, then validate defaults and the owner-private user file with `amsftp config validate`.
5. Start the user daemon with `amsftp daemon start --format json`. Confirm the returned build and protocol identity rather than inferring health from the socket file.
6. Run `amsftp doctor --endpoint work --format json`. The endpoint check expands the validated OpenSSH configuration and performs only its documented bounded probe; it does not authenticate through a proxy or mutate remote state.
7. Launch with two explicit locations, for example `amsftp /absolute/local work:/absolute/remote`, or reopen a saved workspace with `amsftp --workspace <name>`. A workspace stores pane locations and view state, not credentials.

An alias may use keys, Agent, ProxyJump, ProxyCommand, or Kerberos exactly as system OpenSSH supports them. AMSFTP deliberately disables credential delegation for its own restricted remote roles and never turns a successful configuration expansion into a claim that authentication has succeeded.

## Default keymap and transfer safety

The complete [default keymap](keymap.md) is Vim-first: `h/j/k/l` navigate, `Space` or `v` selects, `y` copies a reference, `d` cuts a reference, `p` plans a paste, `D` begins the separately confirmed delete flow, `J` opens Jobs, and `q` quits. Print the effective map with `amsftp config print-effective-keymap` before changing a binding.

Every mutation becomes a durable Job through the Planner and Worker; the TUI does not write a LocalFS or SFTP path directly. Review source, destination, count, route, conflict policy, verification level, and reversibility before confirmation. New bytes remain in a Job-owned part until verification and commit. Overwrite, irreversible delete, move, and direct-transfer choices retain their preview and explicit confirmation even when keys are remapped.

Cross-endpoint copy defaults to a bounded local relay. Standard SFTP remains the Level 0 fallback when Helper is absent or rejected. Production Helper and production Level 2 remain closed; configuration cannot open them.

For the first safe exercise, follow the [minimal local walkthrough](durable-transfers.md#minimal-local-walkthrough) before using a remote endpoint. Use the [operations runbook](../operations/runbook.md) if daemon, authentication, recovery, or integrity status is unclear.
