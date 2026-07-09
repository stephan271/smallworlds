# Tier 1 Remediation Service

Deterministic first-responder for known alert patterns. It sits between
Alertmanager and Hermes (the Tier 2 AI SRE):

```
Prometheus ──▶ Alertmanager ──▶ Tier 1 (this service) ──▶ overlay PR (fix)
                                      │
                                      └── can't handle / fix didn't hold ──▶ Hermes (Tier 2)
```

Alertmanager POSTs matching alerts to `/webhook`. Each alert runs through the
**escalation gate** and the **handler registry**. A handler either takes one
bounded, deterministic action (open a PR — never mutate the cluster) or declines
so the alert escalates. Everything unmatched or unresolved goes to Hermes.

## Why a bespoke service (not Robusta)

Single 16 GB node + strict "every change is a PR" GitOps/selfHeal policy means
Robusta's direct-remediation actions get reverted by ArgoCD anyway, so we'd only
use its PR-generating custom actions — ~20 % of a heavy chart. Revisit Robusta
0.44 only if the handler catalog outgrows hand-rolling (~8–10 alert classes).

## Handlers

| Handler   | Trigger                                   | Action |
|-----------|-------------------------------------------|--------|
| `oomkill` | `KubePodCrashLooping` + last term OOMKilled | Size a new memory limit from the workload's P95 working-set and open an overlay PR bumping `limits.memory` |

`oomkill` is deliberately conservative: it confirms OOMKilled via
kube-state-metrics, sizes from `P95 × HEADROOM_FACTOR`, and **declines** (→
escalate) when it can't confirm OOM, has no metrics, or the current limit
already covers P95 (i.e. the OOM is a leak/spike, not undersizing).

## PR target: the overlay, not the base

Right-sizing PRs go to the private overlay (`my-community-config`) as a kustomize
strategic-merge patch on the tenant's Deployment. That's the only ref that
deploys on merge (the base needs a re-tag + pin bump) and it matches where
per-community tuning belongs — same repo Renovate already targets.

## Escalation gate

Alertmanager can't remember "already tried". This service does: on first
sighting it runs the handler and starts a `RESOLVE_DEADLINE_SECONDS` clock; a
re-fire past the deadline (the fix didn't hold) escalates to Hermes. State is
in-process (resets on restart — idempotent, since re-running just finds the PR
already open). Persist to a ConfigMap when hardening.

## Layout

```
src/
  main.py         stdlib HTTP server: POST /webhook, GET /healthz
  registry.py     dispatch: escalation gate + handler selection
  escalation.py   fingerprint tracking + Hermes hand-off
  prometheus.py   instant-query client (OOM confirm, P95, current limit)
  overlay_pr.py   GitHub API: branch + patch + kustomization ref + PR
  units.py        k8s memory quantity <-> bytes
  handlers/
    base.py       Handler contract + Result/Outcome
    oomkill.py    the OOMKill right-sizing handler
```

Source runs straight from ConfigMap volumes on a stock `python:3.14-slim`
(same scaffold pattern as the Hermes stub). It is pure Python stdlib — no
third-party deps and no pip at startup — so the pod starts offline and
deterministically. Graduate to a built, pinned image once handlers stabilise.

### Known limitations

- **Deployments only.** `_deployment_of` derives the workload from the pod name
  using the ReplicaSet hash shape, so StatefulSet/DaemonSet pods (e.g. the redis
  StatefulSets) don't match — those OOMs fail safe (decline → escalate to
  Hermes) rather than being right-sized. Resolving workloads via ownerReferences
  (needs k8s API read + RBAC) is the fix.
- **Sizing needs a known current limit.** If the current memory limit can't be
  read from Prometheus during the crashloop, the handler declines (→ escalate)
  rather than risk proposing a decrease.

## Arming (currently DRY_RUN)

`DRY_RUN: "true"` in `config.yaml` — handlers compute and log the intended fix
but open no PRs and escalate to no one. To arm:

1. Provision the `repo-git-creds` Secret (GitHub PAT, `repo` scope — same one
   Renovate uses) in the `remediation` namespace.
2. Set `DRY_RUN: "false"` in `config.yaml`.
3. Tune `HEADROOM_FACTOR`, `P95_WINDOW`, `RESOLVE_DEADLINE_SECONDS` if needed.
4. Once confident, drop the duplicate `email-admin` route for
   `KubePodCrashLooping` in `apps/alertmanager-config.yaml` so handled alerts
   stop paging.
