# Perform full decommissioning safely

Status: ready-for-agent

## What to build

Give the Lifecycle Authority a full decommission journey for irreversible removal of profile-owned cluster resources. A new inspection must inventory Recovery Bundle and protection evidence, distinguish retained/shared/unknown resources, require stronger typed confirmation, and allow an explicit informed override when protection is insufficient so paid resources can still be stopped safely.

Covers PRD user stories 113–117.

## Acceptance criteria

- [ ] Full decommission always performs a fresh ownership, protection, and Recovery Bundle inspection and cannot reuse stale approval from preserve-data planning.
- [ ] The plan lists backup freshness, offsite Recovery Points, Recovery Bundle status, all proposed deletions, all retained/shared resources, and irreversible data consequences.
- [ ] Strong typed confirmation is bound to the Cluster Profile and current plan digest; ordinary button confirmation is insufficient.
- [ ] Missing or stale protection produces a prominent warning and requires an explicit Owner override but does not make paid infrastructure impossible to stop.
- [ ] Only resources proven profile-owned are deleted; shared DNS zones and the GitOps Overlay are always retained, and unknown ownership defaults to retention.
- [ ] Interruption after each deletion stage is recoverable through reinspection and never causes the remaining deletion scope to expand.
- [ ] Completion produces a final redacted Activity Record that can be exported before the separate optional profile-forget operation.
- [ ] Tests cover all Deployment Modes and inject failure after compute, storage, networking, and DNS stages.

## Blocked by

- [Issue 03](03-transfer-lifecycle-authority-with-a-recovery-bundle.md)
- [Issue 12](12-inspect-dataset-protection-and-recovery-evidence.md)
- [Issue 22](22-perform-preserve-data-decommissioning.md)
