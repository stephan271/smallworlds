# Transfer Lifecycle Authority with a Recovery Bundle

Status: complete

## What to build

Let the Lifecycle Authority export a versioned, age-encrypted Recovery Bundle and import it on another Launcher Host. The import journey previews and verifies cluster identity and bundle integrity before assigning authority, restores the Cluster Profile and required protected material, and rejects unsafe duplicate or mismatched authority transfers.

Covers PRD user stories 8–11, 67, and 109–110.

## Acceptance criteria

- [x] Export creates an age-compatible bundle using passphrase/scrypt by default, with advanced age recipients supported through the same journey.
- [x] The encrypted payload contains the required Cluster Profile, workflow snapshot, infrastructure state, kubeconfig, Cluster CA material, and Launcher Vault material while excluding caches, downloaded tools, and detailed logs.
- [x] Only a minimal format/version header is cleartext, and secret scanning finds no protected value outside the encrypted payload.
- [x] Import validates format, integrity, compatibility, and cluster identity and shows a safe preview before the Operator confirms authority transfer.
- [x] Wrong credentials, corruption, duplicate authority, and identity mismatch fail safely without partially importing state.
- [x] Round-trip tests demonstrate transfer between supported operating-system families and preserve resumable workflow history.

## Implementation notes

- The bundle has only the `SWRECOVERY/1` format/version header in cleartext; its JSON payload is age-encrypted.
- Operators can use a 12+ character scrypt passphrase or advanced X25519 age recipients. The web journey never displays encrypted payload material or private identities.
- Import makes the source profile identity visible first; confirmation requires the previewed cluster ID and rejects an existing lifecycle authority before Vault or state changes.
- The Launcher resumes imported active workflow runs after successful state import.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
