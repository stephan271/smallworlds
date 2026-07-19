# Inspect a remote or same-host Local Cluster Node

Status: ready-for-agent

## What to build

Let an Operator select either a remote Linux Cluster Node over SSH or, on supported Linux Launcher Hosts, an explicit same-host target and perform a read-only inspection before mutation. The result must explain identity, trust, privileges, capacity, occupied resources, foreign installations, and whether a recognized same-profile installation can be resumed.

Covers PRD user stories 40 and 54–62.

## Acceptance criteria

- [ ] Remote setup supports SSH agent, passphrase-protected key, and username/password authentication with direct root or separate sudo credentials.
- [ ] First contact displays the Cluster Node host-key fingerprint for confirmation and then pins it to the Cluster Profile; a mismatch blocks later access.
- [ ] Supported Linux launchers offer an explicit same-host option with a narrowly scoped elevation boundary, while macOS and Windows do not present it.
- [ ] Read-only inspection reports operating system, architecture, systemd support, CPU, memory, disk, required ports/paths, network/kernel conditions, and privilege availability.
- [ ] Capability-derived requirements are compared with observed capacity and produce an explainable verdict rather than a simple pass/fail.
- [ ] Foreign Kubernetes installations, unidentified SmallWorlds data, port/path collisions, and another profile's installation block ordinary setup without alteration.
- [ ] A recognized interrupted installation belonging to the selected Cluster Profile is offered as resumable, and a dedicated per-profile SSH key can be planned after initial trust.
- [ ] Contract tests cover each authentication path, sudo, host-key mismatch, interruption, foreign installations, and secret redaction.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 04](04-select-cluster-capabilities-and-preview-a-gitops-overlay.md)
