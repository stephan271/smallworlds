# Store credentials safely in the Launcher Vault

Status: ready-for-agent

## What to build

Add the first credential-custody journey to the Bootstrap Launcher. An Operator unlocks the Launcher Vault, submits a representative credential, restarts the launcher, and sees useful credential metadata without the stored value ever returning to the browser or ordinary profile storage. The design must exercise the operating-system wrapping boundary and the passphrase fallback needed on systems without a usable credential store.

Covers PRD user stories 7–8 and 109–110.

## Acceptance criteria

- [ ] The Setup Journey detects whether an operating-system credential store is usable and offers the documented passphrase-unlocked fallback when it is not.
- [ ] A submitted credential is referenced by the Cluster Profile but its value is absent from SQLite, browser responses, plans, events, logs, and diagnostics fixtures.
- [ ] After restart and unlock, the Operator sees only presence, source, expiry, and rotation status and can replace or remove the credential deliberately.
- [ ] Unlock failures and unavailable platform facilities produce stable, translated remediation errors without exposing secret material.
- [ ] Restrictive filesystem permissions or ACL behavior is verified for persisted launcher state on supported platform adapters.
- [ ] Integration tests cover the real vault interface with an isolated test adapter and prove redaction at the HTTP boundary.

## Blocked by

- [Issue 01](01-complete-a-fake-mutation-through-the-bootstrap-launcher.md)
