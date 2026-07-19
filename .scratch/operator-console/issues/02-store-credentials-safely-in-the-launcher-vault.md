# Store credentials safely in the Launcher Vault

Status: ready-for-agent

## What to build

Add the first credential-custody journey to the Bootstrap Launcher. An Operator unlocks the Launcher Vault, submits a representative credential, restarts the launcher, and sees useful credential metadata without the stored value ever returning to the browser or ordinary profile storage. The design must exercise the operating-system wrapping boundary and the passphrase fallback needed on systems without a usable credential store.

Covers PRD user stories 7–8 and 109–110.

## Acceptance criteria

- [x] The Setup Journey detects whether an operating-system credential store is usable and offers the documented passphrase-unlocked fallback when it is not.
- [x] A submitted credential is referenced by the Cluster Profile but its value is absent from SQLite, browser responses, plans, events, logs, and diagnostics fixtures.
- [x] After restart and unlock, the Operator sees only presence, source, expiry, and rotation status and can replace or remove the credential deliberately.
- [x] Unlock failures and unavailable platform facilities produce stable, translated remediation errors without exposing secret material.
- [x] Restrictive filesystem permissions or ACL behavior is verified for persisted launcher state on supported platform adapters.
- [x] Integration tests cover the real vault interface with an isolated test adapter and prove redaction at the HTTP boundary.

## Blocked by

- [Issue 01](01-complete-a-fake-mutation-through-the-bootstrap-launcher.md)

## Comments

### 2026-07-19 — Implementation complete

Implemented under `operator-console/` through red-green-refactor cycles at the public HTTP/browser seam.

Evidence:

- The Launcher Vault uses age encryption with atomic replacement and keeps credential values separate from SQLite credential references.
- The native wrapping adapter targets macOS Keychain, Windows Credential Manager, and Linux/BSD Secret Service; an isolated in-memory adapter verifies the same vault interface, and the passphrase fallback remains available when the native facility cannot be used.
- HTTP integration tests cover capability detection, unlock failures, passphrase strength, restart locking/unlocking, metadata-only reads, deliberate replacement/removal, and sentinel scanning across persisted files, profiles, plans, Workflow Runs, and events.
- Unix permission tests require `0700` directories and `0600` files; the Windows adapter installs a protected current-user-only DACL and includes a Windows-specific ACL test. Windows amd64 and macOS arm64 cross-builds pass.
- The OpenAPI-generated Svelte 5 journey passes Playwright in English and German with keyboard operation, translated remediation, write-only secret fields, axe checks, replacement/removal, and no rendered credential value.
