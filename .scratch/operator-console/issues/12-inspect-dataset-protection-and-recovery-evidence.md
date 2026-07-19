# Inspect dataset protection and recovery evidence

Status: ready-for-agent

## What to build

Add a protection inventory that explains whether every declared dataset has recent local and offsite Recovery Points and what restore experience exists. The view must distinguish producer Job success from actual protection evidence and integrate stale or missing protection into affected Capability Assessments without pretending that future restore controls already exist.

Covers PRD user stories 94, 97–99, and 105–106.

## Acceptance criteria

- [ ] Every protected dataset is associated with its owning Cluster Capability, data type, expected producer, schedule, and declared retention.
- [ ] Observers collect evidence from current CNPG, Velero, PV backup, Garage, and offsite-replication resources without deriving presentation state themselves.
- [ ] The UI clearly distinguishes Job completion, local Recovery Point, offsite Recovery Point, age/freshness, retention confidence, and stale or unknown evidence.
- [ ] Stateful capabilities become degraded when required protection evidence is stale or absent according to declared policy.
- [ ] The most recent manual Restore Drill date and result are displayed per relevant dataset or capability.
- [ ] Restore execution, deletion, and retention mutation appear only as an honest roadmap with no usable-looking inactive controls.
- [ ] Tests cover the two-hop local-to-Garage-to-offsite chain and prove that same-disk Garage data is not described as disaster protection.

## Blocked by

- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
