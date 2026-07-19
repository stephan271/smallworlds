# Transfer Lifecycle Authority with a Recovery Bundle

Status: ready-for-agent

## What to build

Let the Lifecycle Authority export a versioned, age-encrypted Recovery Bundle and import it on another Launcher Host. The import journey previews and verifies cluster identity and bundle integrity before assigning authority, restores the Cluster Profile and required protected material, and rejects unsafe duplicate or mismatched authority transfers.

Covers PRD user stories 8–11, 67, and 109–110.

## Acceptance criteria

- [ ] Export creates an age-compatible bundle using passphrase/scrypt by default, with advanced age recipients supported through the same journey.
- [ ] The encrypted payload contains the required Cluster Profile, workflow snapshot, infrastructure state, kubeconfig, Cluster CA material, and Launcher Vault material while excluding caches, downloaded tools, and detailed logs.
- [ ] Only a minimal format/version header is cleartext, and secret scanning finds no protected value outside the encrypted payload.
- [ ] Import validates format, integrity, compatibility, and cluster identity and shows a safe preview before the Operator confirms authority transfer.
- [ ] Wrong credentials, corruption, duplicate authority, and identity mismatch fail safely without partially importing state.
- [ ] Round-trip tests demonstrate transfer between supported operating-system families and preserve resumable workflow history.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
