# Inspect dataset protection and recovery evidence

Status: in-progress

## Implementation progress

Landed incrementally (tracer-per-commit), building on issue 11's assessment
engine and console.

- [x] **Protection inventory domain + console endpoint** (`internal/protection`)
  — covers acceptance criteria 1, 2, 4, 5, 7. Models the repository's real
  two-hop backup chain (app data → in-cluster Garage S3 → offsite mirror, per
  `doc/storage-and-backup.md` §4) as a declared dataset inventory, each dataset
  naming its owning capability, data type, expected producer (CNPG Barman /
  Velero / pv-backup rclone), schedule, and retention. Per-dataset `Assess`
  encodes the central honesty rule (storage doc §3): Garage is a staging tier on
  the **same disk** as primary data, so it keeps producer-Job-completion, a local
  (same-disk) Recovery Point, and an offsite Recovery Point as three distinct
  facts and reports `DisasterProtected` **only** when a fresh offsite Recovery
  Point exists — a local-only Garage Recovery Point is `local-only`, never
  disaster protection. A `Source` seam lets observers gather evidence without
  deciding status; `CapabilityEvidence` aggregates a capability's datasets
  (worst-case) into `assessment.ProtectionEvidence`, so a missing/stale offsite
  leg degrades the stateful capability through the existing engine. Console
  `GET /api/v1/protection` (Observe) serves the inventory. Tests prove the
  two-hop chain, that same-disk Garage data is not described as disaster
  protection, that Job completion is distinct from a Recovery Point, unknown on
  read failure, and the capability-degradation bridge. `gofmt`, `go build/vet/
  test ./...` all pass. (The production Source reading live CNPG/Velero/PV/Garage/
  replicator resources is deferred with the other live observers; the inventory
  screen and roadmap-only restore/delete/retention controls — criteria 3, 6 —
  land in the screens tracer.)

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
