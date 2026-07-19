# Select Cluster Capabilities and preview a GitOps Overlay

Status: complete

## What to build

Give an Operator an end-to-end capability-selection journey that distinguishes Platform Services from Community Applications, supports Minimal, Collaboration, Full, and Custom selections, and produces a deterministic, secret-free GitOps Overlay preview. The catalog must become the shared source for selection, dependencies, resource estimates, exposure, protection expectations, and rendering while retaining the behavior required from the legacy repository-preparation script.

Covers PRD user stories 31, 33, 36–42, and 44.

## Acceptance criteria

- [x] Every currently declared Platform Service and Community Application is represented once by a schema-validated catalog entry with stable identity and localized display keys.
- [x] Required Platform Services cannot be deselected, Community Applications are opt-in, and all four selection modes produce explainable dependency and capacity results.
- [x] The selection view shows estimated resources, exposure, and protection implications and supports a valid Platform-Service-only cluster.
- [x] An accepted selection produces a Change Plan containing the exact pinned SmallWorlds release and a readable Git diff for a deterministic GitOps Overlay.
- [x] Generated overlays contain no secrets and pass golden, Kustomize, and schema validation for all three Deployment Modes and representative selections/domains.
- [x] Catalog validation catches missing dependencies, cycles, unsupported modes, missing translations, unknown observers, and absent exposure declarations.
- [x] Characterization fixtures preserve required behavior from the legacy repository-preparation script without requiring accidental formatting parity.

## Implementation notes

- `internal/capability` is the shared catalog, selection assessment, deterministic renderer, and overlay validator.
- The console offers Minimal, Collaboration, Full, and Custom selection modes and presents the resource, exposure, protection, and diff evidence before producing an `ApplyCapabilities` Change Plan.
- The renderer accepts only exact semantic release tags and credential-free HTTPS repository URLs, and emits only declarative, secret-free files.

## Blocked by

- [Issue 01](01-complete-a-fake-mutation-through-the-bootstrap-launcher.md)
