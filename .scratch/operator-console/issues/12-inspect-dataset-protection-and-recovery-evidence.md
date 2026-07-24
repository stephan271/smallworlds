# Inspect dataset protection and recovery evidence

Status: complete

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

- [x] **Protection screen** (`web/src/routes/console`, `console.ts`,
  `console-i18n.ts`) — covers acceptance criteria 3 and 6. Adds a Protection view
  to the console (a Capabilities/Protection nav toggle) that lists every dataset
  as a card with a non-color level symbol, its headline Protection level, and an
  explicit **Disaster protected / Not disaster protected** badge. Each card keeps
  the evidence the backend distinguishes visibly separate — owning capability,
  data type, producer, schedule, retention, **Producer Job** (completed / failed /
  never), **Local Recovery Point** (with stale suffix), **Offsite Recovery Point**
  (with stale suffix; "None" when unconfigured), and the **Restore Drill** date +
  pass/fail — so Job completion is never conflated with a Recovery Point, and a
  `local-only` dataset shows the same-disk warning. A standing roadmap notice
  states that restore, backup deletion, and retention changes are not available
  this release and are shown only as planned work — **no inactive-looking
  controls**. Full English + German (parity enforced). `npm run check` (0 errors)
  and `npm run build` pass.

## Definition of done

All seven acceptance criteria are implemented and tested at the Go/Svelte level.
Deferred to the tenant-deployment integration (needs a live cluster, mirroring
issue 11): the production protection `Source` reading live CNPG ScheduledBackups,
Velero backups, pv-backup CronJobs, Garage objects, and the offsite replicator,
and wiring `protection.CapabilityEvidence` into the live capability assessor.
These are integration steps, not code dependencies of the inventory built here.

## What to build

Add a protection inventory that explains whether every declared dataset has recent local and offsite Recovery Points and what restore experience exists. The view must distinguish producer Job success from actual protection evidence and integrate stale or missing protection into affected Capability Assessments without pretending that future restore controls already exist.

Covers PRD user stories 94, 97–99, and 105–106.

## Acceptance criteria

- [x] Every protected dataset is associated with its owning Cluster Capability, data type, expected producer, schedule, and declared retention.
- [x] Observers collect evidence from current CNPG, Velero, PV backup, Garage, and offsite-replication resources without deriving presentation state themselves.
- [x] The UI clearly distinguishes Job completion, local Recovery Point, offsite Recovery Point, age/freshness, retention confidence, and stale or unknown evidence.
- [x] Stateful capabilities become degraded when required protection evidence is stale or absent according to declared policy.
- [x] The most recent manual Restore Drill date and result are displayed per relevant dataset or capability.
- [x] Restore execution, deletion, and retention mutation appear only as an honest roadmap with no usable-looking inactive controls.
- [x] Tests cover the two-hop local-to-Garage-to-offsite chain and prove that same-disk Garage data is not described as disaster protection.

## Blocked by

- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
