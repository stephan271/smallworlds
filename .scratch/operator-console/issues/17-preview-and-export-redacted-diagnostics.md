# Preview and export redacted diagnostics

Status: ready-for-agent

## What to build

Let an Operator assemble a local diagnostics bundle, inspect exactly what it will contain, review a redaction report, and explicitly export it for support. Normal logs, metrics, and Activity Records remain on the Operator's systems, and no analytics or automatic crash report is sent by default.

Covers PRD user stories 107–110 and 125.

## Acceptance criteria

- [ ] The diagnostics preview lists versions, platform, catalog/asset digests, redacted plan/run summaries, validation results, and selected local or referenced cluster logs.
- [ ] Launcher Vault values, Kubernetes Secret data, private keys, kubeconfig credentials, raw OpenTofu sensitive outputs, and unredacted browser payloads are excluded.
- [ ] The Operator can inspect a redaction report and deselect optional diagnostic categories before creating an archive.
- [ ] Export occurs only after explicit confirmation and never uploads or transmits the bundle automatically.
- [ ] Credential information is represented only by presence, source, expiry, and rotation status.
- [ ] The preview and export flow is usable on an enrolled phone as well as a larger screen.
- [ ] Seeded canary secrets and structured sensitive fields are absent in generated diagnostics under automated secret-scanning tests.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
