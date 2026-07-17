# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

SmallWorlds is a GitOps-driven, self-hosted "sovereign cloud" for a small community: `smallworlds-init.sh` provisions a single node — either a Terraform-managed Hetzner Cloud VM (target `hetzner`) or an existing LAN machine bootstrapped over SSH via `infrastructure/local/bootstrap-local-node.sh` (target `local`, see `doc/local-deployment.md`) — installs k3s + ArgoCD on it, and ArgoCD then reconciles everything else (identity, storage, databases, and end-user apps like Nextcloud, Immich, Forgejo, Plane, Jitsi, Stalwart mail) from Kubernetes manifests in this repo. The cloud-init template and the local bootstrap script implement the same bootstrap contract (self-signed vs ACME `letsencrypt-prod` issuer, `coredns-custom` override, `/mnt/smallworlds-data` layout, ArgoCD root app) — a change to one usually needs mirroring in the other.

**This repo is the base only — it is never deployed to directly.** A separate private repo (`my-community-config`, not present here) is the Kustomize *overlay* that ArgoCD actually watches. It remote-references this repo's manifests at a pinned semver tag (`?ref=v1.x.x`) and layers operator-specific patches/secrets on top. Upstream changes here only reach a live cluster when someone bumps the pinned tag in the overlay — see the README's "Managing Updates — the two-repo model" section for the full mechanics. Keep this in mind: a change merged here does not go live anywhere on its own.

Read `README.md` first for deployment/maintenance instructions aimed at operators; this file is for code changes within the repo itself.

## Repository layout

```
infrastructure/
  terraform/            Production/dev Hetzner VM + DNS provisioning
  terraform-staging/    Ephemeral staging VM used by admin-tools/test-pr-locally.sh
  cloud-init/           Shared k3s+ArgoCD bootstrap template used by both terraform roots
  local/                LAN-server bootstrap (shell counterpart of cloud-init, no Terraform)
  kubernetes/
    kustomization.yaml  The master base — root list of ArgoCD Applications + core tenants
    argocd-root-app.yaml  The "app of apps" ArgoCD Application, self-healing retry policy
    namespaces.yaml
    apps/                One ArgoCD `Application` CRD per component, pointing at tenants/ or bases/
    bases/               Reusable Kustomize components (init jobs) shared across tenants
    tenants/<name>/      Per-app manifests (Deployment, Service, Ingress, CNPG cluster, etc.)
  golden-image/          Pre-baked k3s+images snapshot definition (speeds up staging boot)
  keycloak-spi/          Custom Keycloak SPI (action-token-generator)
admin-tools/             Operator scripts: PR staging tests, rebuild/restore, bulk invites
e2e-tests/               Playwright smoke tests against a live/staging cluster
doc/                     Architecture notes per subsystem (see below)
plans-and-walkthroughs/  Design docs and implementation walkthroughs
```

## Core architecture: ArgoCD sync waves

`infrastructure/kubernetes/apps/*.yaml` are ArgoCD `Application` resources; the `argocd.argoproj.io/sync-wave` annotation controls deploy order and is the main non-boilerplate config in these files. Current scheme (flattened from 6 tiers to 4 — see `doc/argocd-apps.md` for the full rationale):

- **Wave -10**: `cert-manager`, `cloudnative-pg`, `persistent-storage`, `traefik` — foundational infra.
- **Wave -5**: `garage` — S3 storage, needed early by CNPG/Velero backups.
- **Wave 0**: everything that doesn't need Keycloak — `keycloak` itself, `dashboard`, monitoring/logging stacks, `velero`, `hermes`, `remediation`, `trivy-operator`.
- **Wave 1**: end-user tenant apps (Nextcloud, Forgejo, Immich, Jitsi, Bulwark, Excalidraw, Stalwart) — depend on CNPG/Garage/Keycloak/Traefik being up.

Intra-wave dependencies are handled by init jobs' poll-and-retry loops and ArgoCD sync retries, not by more waves. When adding a new ArgoCD Application, pick the wave based on what it actually depends on, not by copying an unrelated app's wave.

## Kustomize bases (`infrastructure/kubernetes/bases/`)

Shared, reusable resources consumed by tenants via Kustomize, mostly ArgoCD sync-hook init Jobs:

- **`keycloak-client-job`** (sync-wave `-1`): registers a tenant's OIDC client in Keycloak. **Contract**: consumers MUST read credentials from the `keycloak-secret` Secret, keys `clientId` and `client-secret` — this is enforced project-wide, see `.agents/AGENTS.md`. Do not invent per-tenant secret names.
- **`garage-init-job`** (sync-wave `-2`): provisions a tenant's S3 bucket + access key (`garage-secret`), plus a separate dedicated `postgres-backups` bucket/key (`garage-secret-cnpg`) for CNPG — kept separate for least-privilege.
- **`velero-garage-init-job`**: same pattern but for Velero's cluster-scoped `velero-backups` bucket.
- **`backup-job`**: generic `rclone` CronJob template replicating S3 backups off-cluster.
- **`setup-rbac`** (sync-wave `-3`, strictly before the `-2` init jobs): ServiceAccount/RoleBindings the init jobs run as. Deliberately has **no** ArgoCD hook — a prior `PreSync` hook caused retries to delete the ServiceAccount out from under running jobs (see `doc/bases.md`).
- **`staging-test/smoke-test-job`**: post-deploy `/healthz` poll gate used by the staging pipeline, self-deletes on success (`HookSucceeded`).

Init job images are pinned to `alpine/k8s:<version>` (not `bitnami/kubectl:latest`, which Bitnami stopped maintaining).

## The two Python agents (Hermes and Remediation)

Both live under `infrastructure/kubernetes/tenants/{hermes,remediation}/src/` and share an unusual deployment pattern: **pure Python 3.14 stdlib, no third-party deps, no Dockerfile** — source runs straight from a ConfigMap-mounted volume (`kustomize configMapGenerator`) inside a stock `python:3.14-slim` image via `python -u /app/main.py`, with `PYTHONPATH=/app` set explicitly (ConfigMap volumes symlink through a timestamped `..data` dir, which breaks default import resolution otherwise). This means editing a handler is just editing the `.py` file and letting Kustomize regenerate the ConfigMap — no image build/push step, and the pod starts offline/deterministically.

- **`remediation`** (Tier 1): deterministic first-responder. Alertmanager POSTs to `/webhook`; alerts run through an escalation gate (in-process fingerprint + `RESOLVE_DEADLINE_SECONDS`, resets on restart) and a handler registry (`src/registry.py`, `src/handlers/`). A handler either opens a bounded overlay PR (e.g. `handlers/oomkill.py` right-sizes `limits.memory` from Prometheus P95) or declines, which escalates to Hermes. Handlers never mutate the cluster directly — PRs only, targeting the **private overlay** repo (not this base), since that's the only ref ArgoCD actually deploys from. Currently runs with `DRY_RUN: "true"` in `config.yaml` — see `infrastructure/kubernetes/tenants/remediation/README.md` for the arming checklist and known limitations (Deployments only, needs a readable current memory limit).
- **`hermes`** (Tier 2): the AI SRE agent everything else escalates to; calls Claude over raw HTTP (no `anthropic` SDK) so it stays stdlib-only too.

## Local end-to-end staging (`admin-tools/test-pr-locally.sh`)

`./admin-tools/test-pr-locally.sh <branch-name>` gives a full ephemeral test of a PR branch: provisions a throwaway Hetzner VM via `infrastructure/terraform-staging/`, diffs the branch against `main` to decide whether to deploy *all* apps (core `apps/`/`bases/`/terraform changed) or only the affected tenants, rewrites `infrastructure/kubernetes/kustomization.yaml` and `targetRevision` fields to point at the branch, deploys via `kubectl apply -k`, waits for every ArgoCD Application to go Healthy, patches `/etc/hosts`, and runs the Playwright e2e suite. It always restores git state and destroys the VM on exit (`trap cleanup EXIT`); set `KEEP_VM=1` to skip teardown for debugging. Requires `HCLOUD_TOKEN`. This script — not manual `kubectl apply` — is the standard way to validate a change against a real cluster before merging.

## E2E tests (`e2e-tests/`)

Playwright + TypeScript, run against a **live** cluster (staging or production), simulating SSO login and exercising each app.

```bash
cd e2e-tests && npm ci && npx playwright install chromium   # setup
./run-smoke-tests.sh <domain> [keycloak-admin-password]      # run against a live domain
npx playwright test                                          # run directly once provisioned
npx playwright test tests/02-nextcloud.spec.ts                # single spec
npx playwright show-report reports/html                       # view last HTML report
```

Tests run at one of two depths, controlled by `FULL_OIDC=1`:
- **Shallow (default)**: only asserts each app redirects into Keycloak's authorize endpoint (proves OIDC wiring/secrets/DNS) — full login roundtrips are reported `skipped`, which is expected, not a failure.
- **Full OIDC** (`FULL_OIDC=1`): runs complete login roundtrips + authenticated-UI assertions. Requires app-trusted TLS certs (i.e. Let's Encrypt / production), structurally impossible on ephemeral staging.

Other env vars: `DOMAIN`, `KC_ADMIN_PASS`, `HEADED=1`, `SLOWMO=<ms>`, `SKIP_PROVISION=1`, `KUBECONFIG`. Test specs are numbered by app (`01-keycloak-login`, `02-nextcloud`, ...); `auth.setup.ts` provisions the shared auth state.

## Adding a new tenant application

Follow the integration checklist in the README's "Developer Guide: Adding a New Application" section — it covers: pinning image versions, adding an e2e spec, Homepage dashboard annotations, adding to `OPTIONAL_APPS` in `admin-tools/prepare-community-repo.sh`, updating the README app table, adding a `doc/tenant-*.md`, DNS records in `infrastructure/terraform/main.tf`, and the `cert-manager.io/cluster-issuer` + `tls` block on the Ingress.

## Validating manifest changes

```bash
bash -n <script.sh>                                          # syntax-check a shell script before running it
kubectl kustomize --enable-helm infrastructure/kubernetes/tenants/<name>   # render a tenant's manifests locally
terraform validate                                            # from infrastructure/terraform or terraform-staging
python3 -c 'import json;json.load(open("renovate.json"))'    # validate renovate.json after editing
```

Cluster access for read-only inspection: kubeconfigs live in `~/.smallworlds/kubeconfigs/<production|dev|staging>.yaml` (the default `~/.kube/config` is stale) — `export KUBECONFIG=~/.smallworlds/kubeconfigs/production.yaml`. They are written there by `smallworlds-init.sh` (production/dev) and `test-pr-locally.sh` (staging); see `kubeconfig_path()` in `admin-tools/lib/cluster-env.sh`.

## Documentation map (`doc/`)

`argocd-apps.md` (sync waves) and `bases.md` (init job bases) are summarized above. `local-deployment.md` covers the LAN/local-server target (requirements, DNS/TLS differences, lifecycle). Also present: `bases.md`, `plane-architecture.md`, `tenant-dashboard.md`, `tenant-forgejo.md`, `tenant-hermes.md`, `tenant-immich.md`, `tenant-keycloak.md`, `tenant-nextcloud.md`, `tenant-other.md`, `tenant-stalwart.md` — check the relevant one before making non-trivial changes to that subsystem, as several encode hard-won fixes (version incompatibilities, ordering bugs) that aren't obvious from the manifests alone.

## Project-wide contracts

- **OIDC client secrets**: any tenant using `bases/keycloak-client-job` must read `keycloak-secret` (`clientId`, `client-secret`) — never define a custom secret name for this (`.agents/AGENTS.md`).
