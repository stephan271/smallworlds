# Hermes Tenant (`infrastructure/kubernetes/tenants/hermes/*`)

Hermes is the **Tier 2 AI SRE agent**: the escalation target that the Tier 1
`remediation` service (see `tenants/remediation/README.md`) and Alertmanager
hand alerts to when no deterministic handler applies. On an escalation it runs
a bounded tool-use loop with Claude (`claude-opus-4-8`, adaptive thinking,
effort high) over the **raw Anthropic Messages API** — no `anthropic` SDK, no
third-party packages — diagnoses the incident with read-only tools, and emails
the operator a report.

> **History**: this tenant was completely rewritten in `bf13408`
> ("implement the Tier 2 Claude-powered SRE agent", 2026-07-09). Before that
> it was a `tail -f /dev/null` stub left over from the earlier runbook-based
> "hybrid auto-remediation" phases (`db1ae5b`, `fe1c5d1`, `1caa0e0`); the
> rewrite deleted the runbooks, the notification-channels ConfigMap, the
> unused GitHub PAT secret, and the orphan status-page ingress. An older
> revision of this document described that runbook architecture — none of it
> exists anymore.

## Deployment pattern: source from a ConfigMap, no image build

Hermes shares the `remediation` tenant's unusual deployment model: **pure
Python 3.14 stdlib** running straight out of a ConfigMap inside a stock
`python:3.14-slim` image.

- `kustomization.yaml` uses a `configMapGenerator` (`hermes-src`) over the
  flat `src/` package — flat because ConfigMap keys cannot contain `/`, so a
  single ConfigMap can only hold one directory level.
- The Deployment mounts it at `/app` and runs `python -u /app/main.py` with
  `PYTHONPATH=/app` (ConfigMap volumes symlink through a timestamped `..data`
  dir, which breaks default import resolution otherwise).
- Editing a handler is just editing the `.py` file; Kustomize regenerates the
  hash-suffixed ConfigMap and the pod rolls. No registry, no pip at startup,
  fully offline/deterministic boot.
- The system prompt lives in its own `hermes-system-prompt` ConfigMap
  (`system-prompt.txt`), so prompt tuning also needs no image work.

## Runtime behaviour (`src/`, `hermes-config.yaml`)

- **Entry point** (`main.py`): a stdlib `ThreadingHTTPServer` exposing
  `GET /healthz` and `POST /webhook` (Alertmanager-format payloads, whether
  from the Tier 1 escalation or Alertmanager directly).
- **Storm protection**: a bounded worker pool (max 2 concurrent
  investigations) plus an in-flight fingerprint dedup, so an alert storm
  cannot spawn one paid Opus loop per alert; already-resolved alerts are
  skipped.
- **Diagnostic tools** (`tools.py`): read-only — Prometheus queries
  (`prometheus.py`), Loki queries (`loki.py`), and pod status/events via the
  Kubernetes API (`k8s.py`). Query errors from Prometheus/Loki are surfaced
  back to the model verbatim so it can correct its own query. The terminal
  tool is `send_report`, which emails the admin via Stalwart's internal relay
  (`mailer.py`, same SMTP path Alertmanager uses).
- **Diagnose-and-report only**: this first cut never mutates the cluster.
  `open_pr` against the overlay repo is the planned next tool; until then a
  failed or abandoned investigation still emails the admin — never silent.
- **API hardening**: 429/529/5xx retries with backoff, `max_tokens` 16000
  shared with thinking, truncation flagged in the report.

## Configuration & arming (`hermes-config.yaml`, `hermes-rbac.yaml`)

- Ships with **`DRY_RUN: "true"`**: escalations are logged with the intended
  investigation, but no Claude API calls and no email. Arming = provisioning
  the `hermes-anthropic` Secret (API key) in the overlay and flipping
  `DRY_RUN` to `"false"`; a startup guard refuses to arm without a key.
- `ADMIN_EMAIL` is a placeholder each operator overrides in their overlay.
- RBAC is scoped to **pods + events, read-only** (`bf13408` deliberately cut
  it down from a cluster-wide role that included Secrets).

## Sync wave

Deployed at wave 0 (`apps/hermes.yaml`) — it needs nothing but the
monitoring stack's in-cluster endpoints, which its calls simply retry for.
