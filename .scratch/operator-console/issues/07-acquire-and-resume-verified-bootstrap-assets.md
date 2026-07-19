# Acquire and resume verified bootstrap assets

Status: ready-for-agent

## What to build

Resolve the release-specific bootstrap asset decision and implement one managed acquisition journey through the Bootstrap Launcher. An Operator sees what internet resources are required, downloads a pinned asset through the selected internal asset source, safely resumes interruption, and receives evidence that its version and integrity match the selected SmallWorlds release. The design must leave a clean future seam for an Offline Bundle without claiming offline support now.

Covers PRD user stories 1, 33–34, and 127–130.

## Acceptance criteria

- [ ] The OD-001 experiment compares embedded and separately signed asset distribution and records an accepted decision before production acquisition behavior is merged.
- [ ] The launcher diagnoses required network destinations and clearly distinguishes prerequisite-free setup from offline setup.
- [ ] A selected release resolves only explicit compatible asset versions and refuses downgrade, substitution, or incompatible mutation.
- [ ] Downloads are pinned, checksum/signature verified, cached in private application storage, and resumed after interruption without trusting partial content.
- [ ] Cache and download status are visible through the Setup Journey without exposing arbitrary URL or executable input surfaces.
- [ ] Managed assets are never selected from ambient `PATH` except through an explicit developer-only override.
- [ ] The versioned asset-source boundary can later accept an Offline Bundle, and the UI labels that path as future work rather than a disabled working control.

## Blocked by

- [Issue 01](01-complete-a-fake-mutation-through-the-bootstrap-launcher.md)
