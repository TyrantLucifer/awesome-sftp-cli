# Stage 6 Security Finding Ledger

This ledger records concrete findings discovered during Stage 6 and their evidence-backed disposition. It is not a final security-review approval: unexecuted review areas are listed separately so absence of a finding row cannot be read as completed coverage.

| ID | Severity | Status | Boundary | Finding | Disposition | Evidence | Owner |
|---|---|---|---|---|---|---|---|
| M6-SEC-001 | high | fixed | persistent diagnostics and support export | Lexically valid, safe-shaped values could carry usernames, hostnames, private-key text, Askpass answers, Kerberos tickets, environment secrets, command arguments, or file content into persistent JSON or a support archive. | Persistent component/event/error values now require reviewed semantic registries; every exported structured field uses a domain allowlist/registry and unknown safe-shaped values become `<redacted>`. Expanded archives are scanned with an eight-class corpus. | `ba334b8d8968f5b09c91c0185996994b9a307ff4` plus focused race/vet and dual-Go full local gates | M6.3 diagnostics/support owner |

## Open review coverage

These areas remain unexecuted or incomplete; they are release scope rather than silently accepted findings:

- The production Helper final-byte trust, signing/notarization credentials, offline-key procedure, real artifact secret scan, distribution, rollback, and recovery remain open. The production Helper remains **CLOSED**.
- Production Level 2 isolated-network identity/authentication, containment, integrity, cancellation, negative routing, and relay fallback. Level 2 remains **CLOSED**.
- Actual Askpass/Kerberos authentication artifacts and production package/Helper artifacts have not yet been scanned with the release corpus.
- Real hardware security-key authentication, macOS Kerberos, vendor SFTP servers, Linux arm64 native smoke, and a physical 100 GiB execution remain outside current native evidence.
- Independent correctness/security reviews, final finding disposition, project LICENSE owner/legal choice, and protected public-channel evidence remain open.

No open area above is claimed as verified, supported, or release-approved by this ledger.
