# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

SmallWorlds is a GitOps-driven, self-hosted "sovereign cloud" for a small community: `smallworlds-init.sh` provisions a single node — either a Terraform-managed Hetzner Cloud VM (target `hetzner`) or an existing LAN machine bootstrapped over SSH via `infrastructure/local/bootstrap-local-node.sh` (target `local`, see `doc/local-deployment.md`) — installs k3s + ArgoCD on it, and ArgoCD then reconciles everything else (identity, storage, databases, and end-user apps like Nextcloud, Immich, Forgejo, Plane, Jitsi, Stalwart mail) from Kubernetes manifests in this repo. The cloud-init template and the local bootstrap script implement the same bootstrap contract (self-signed vs ACME `letsencrypt-prod` issuer, `coredns-custom` override, `/mnt/smallworlds-data` layout, ArgoCD root app) — a change to one usually needs mirroring in the other.

**This repo is the base only — it is never deployed to directly.** A separate private repo (`my-community-config`, not present here) is the Kustomize *overlay* that ArgoCD actually watches. It remote-references this repo's manifests at a pinned semver tag (`?ref=v1.x.x`) and layers operator-specific patches/secrets on top. Upstream changes here only reach a live cluster when someone bumps the pinned tag in the overlay — see the README's "Managing Updates — the two-repo model" section for the full mechanics. Keep this in mind: a change merged here does not go live anywhere on its own.

Read `README.md` first for deployment/maintenance instructions aimed at operators; this file is for code changes within the repo itself.

## Repository layout

```
infrastructure/
  terraform/            Production/dev Hetzner VM + DNS provisioning (see "Node
                        placement and DNS" below — location and records are inputs,
                        not constants)
  terraform-staging/    Ephemeral staging VM used by admin-tools/test-pr-locally.sh
  cloud-init/           Shared k3s+ArgoCD bootstrap template used by both terraform roots
  bootstrap-pins.env    K3s + Argo CD versions, URLs and SHA-256s every bootstrap path installs
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
- **Wave 1**: end-user tenant apps (Nextcloud, Forgejo, Immich, Jitsi, Bulwark, Excalidraw, Stalwart) — all depend on CNPG/Garage/Keycloak/Traefik being up. `operator-console` was in this wave and is **deactivated** (2026-08-23): its code and manifests are untouched but nothing deploys it — see `docs/adr/0051-a-single-use-setup-wizard-replaces-the-bootstrap-launcher.md`.

Intra-wave dependencies are handled by init jobs' poll-and-retry loops and ArgoCD sync retries, not by more waves. When adding a new ArgoCD Application, pick the wave based on what it actually depends on, not by copying an unrelated app's wave.

## Kustomize bases (`infrastructure/kubernetes/bases/`)

Shared, reusable resources consumed by tenants via Kustomize, mostly ArgoCD sync-hook init Jobs:

- **`keycloak-client-job`** (sync-wave `-1`): registers a tenant's OIDC client in Keycloak. **Contract**: consumers MUST read credentials from the `keycloak-secret` Secret, keys `clientId` and `client-secret` — this is enforced project-wide, see `.agents/AGENTS.md`. Do not invent per-tenant secret names.
- **`garage-init-job`** (sync-wave `-2`): provisions a tenant's S3 bucket + access key (`garage-secret`), plus a separate dedicated `postgres-backups` bucket/key (`garage-secret-cnpg`) for CNPG — kept separate for least-privilege.
- **`velero-garage-init-job`**: same pattern but for Velero's cluster-scoped `velero-backups` bucket.
- **Object storage is in-house or external** (`docs/adr/0053`): the three init jobs above use an external S3 provider instead of Garage when the installer planted `default/smallworlds-object-storage`. Endpoint, region and bucket travel in the `garage-secret*` Secrets next to the key — **new S3 consumers must read them from there, never hard-code a Garage address.** Fields that cannot read a Secret (barman `ObjectStore`, Velero's `BackupStorageLocation`, Nextcloud's env) are patched into the overlay by `admin-tools/generate_storage_patches.py`; a new literal-only consumer needs a patch there and a case in `admin-tools/test-object-storage.py`.
- **`backup-job`**: generic `rclone` CronJob template replicating S3 backups off-cluster.
- **`setup-rbac`** (sync-wave `-3`, strictly before the `-2` init jobs): ServiceAccount/RoleBindings the init jobs run as. Deliberately has **no** ArgoCD hook — a prior `PreSync` hook caused retries to delete the ServiceAccount out from under running jobs (see `doc/bases.md`).
- **`staging-test/smoke-test-job`**: post-deploy `/healthz` poll gate used by the staging pipeline, self-deletes on success (`HookSucceeded`).

Init job images are pinned to `alpine/k8s:<version>` (not `bitnami/kubectl:latest`, which Bitnami stopped maintaining).

## Photos: Immich or Gallery (`tenants/gallery`)

`tenants/gallery` is **not a second application** — it is `tenants/immich` plus patches (Gallery's images, S3 storage read from `garage-secret`, a bucket copy instead of `pv-backup`, no pod export), in the same `immich` namespace and Application (`docs/adr/0054`). An overlay selects it by pointing its `immich/` directory at that path; `prepare-community-repo.sh` rewrites the one resource line. A change to `tenants/immich` therefore changes Gallery too — render both. Do not add `gallery` to `OPTIONAL_APPS`, the DNS maps or the wizard catalog.

## The two Python agents (Hermes and Remediation)

Both live under `infrastructure/kubernetes/tenants/{hermes,remediation}/src/` and share an unusual deployment pattern: **pure Python 3.14 stdlib, no third-party deps, no Dockerfile** — source runs straight from a ConfigMap-mounted volume (`kustomize configMapGenerator`) inside a stock `python:3.14-slim` image via `python -u /app/main.py`, with `PYTHONPATH=/app` set explicitly (ConfigMap volumes symlink through a timestamped `..data` dir, which breaks default import resolution otherwise). This means editing a handler is just editing the `.py` file and letting Kustomize regenerate the ConfigMap — no image build/push step, and the pod starts offline/deterministically.

- **`remediation`** (Tier 1): deterministic first-responder. Alertmanager POSTs to `/webhook`; alerts run through an escalation gate (in-process fingerprint + `RESOLVE_DEADLINE_SECONDS`, resets on restart) and a handler registry (`src/registry.py`, `src/handlers/`). A handler either opens a bounded overlay PR (e.g. `handlers/oomkill.py` right-sizes `limits.memory` from Prometheus P95) or declines, which escalates to Hermes. Handlers never mutate the cluster directly — PRs only, targeting the **private overlay** repo (not this base), since that's the only ref ArgoCD actually deploys from. Currently runs with `DRY_RUN: "true"` in `config.yaml` — see `infrastructure/kubernetes/tenants/remediation/README.md` for the arming checklist and known limitations (Deployments only, needs a readable current memory limit).
- **`hermes`** (Tier 2): the AI SRE agent everything else escalates to; calls Claude over raw HTTP (no `anthropic` SDK) so it stays stdlib-only too.

## The setup wizard (`admin-tools/setup-wizard/`)

`./smallworlds-setup` starts a single-use local web wizard that collects the install
settings in five panels and then drives `admin-tools/prepare-community-repo.sh` and
`smallworlds-init.sh` — it never reimplements install logic, so there stays exactly one
install code path and the terminal route keeps working. Python 3 standard library only,
no dependencies and no build step (same constraint as the Hermes/Remediation agents);
the page is plain HTML/CSS/JS with no framework or bundler.

Both scripts run headless under `SMALLWORLDS_NONINTERACTIVE=1` via the shared
`admin-tools/lib/ask.sh`: a known value is taken, a missing required one exits **78**
printing `!!! missing:<VAR>`, and progress markers (`>>> step:<id>`) are emitted. Nothing
about interactive mode changed, and `admin-tools/test-noninteractive-install.sh` asserts
that over a pty.

It collects, validates, shows the plan, installs, and hands over the credentials — the
scripts' `sw_result` markers carry the credentials, addresses, kubeconfig path and hosts
line, so the handover never scrapes their prose. A failure is answered with advice
rather than a log: the scripts emit `sw_guidance` markers (`>>> guidance\t<heading>\t<detail>`,
also in `lib/ask.sh`), the wizard renders them as "What you can do now", and a script
that stops without saying anything falls back to per-stage advice in `runner.py`. Read `docs/adr/0051-a-single-use-setup-wizard-replaces-the-bootstrap-launcher.md` for the decision and
`plans-and-walkthroughs/design-setup-wizard.md` for the design before changing it.
Tests: `admin-tools/test-setup-wizard.sh` (the browser half needs `npm ci` in `e2e-tests/`).

## Node placement and DNS (`hetzner` target)

Two things used to be constants in `infrastructure/terraform/main.tf` and are now
decided per install:

- **Where the node goes.** `smallworlds-init.sh`'s `ensure_primary_ip()` asks the Hetzner
  API which of `nbg1`/`fsn1`/`hel1` can currently supply `$HETZNER_SERVER_TYPE` (default
  `cx43`), creates the Primary IP `smallworlds-ip<env-slug>` there, and passes the location
  to Terraform as `var.location`. It is the same idempotent-precondition pattern as
  `ensure_dns_zone`/`ensure_ssh_key`, and for the same reason — the IP must outlive any one
  Terraform state. **The scan runs once**: a Primary IP is datacenter-bound and is the
  community's public address, so once it exists it *is* the answer, and a location that
  has since sold out fails the placement check (before anything is created) rather than
  silently moving the cluster. Terraform re-checks the same thing in a `precondition` on
  the server, so a bare `terraform apply` gets the same message.

  The size is resolved by `resolve_server_type()`, not hard-coded: `HETZNER_SERVER_TYPE` in
  this run's environment, then **the size the running node actually is**, then the cached
  value, then `cx43`. An existing node wins over the default for the same reason an
  existing Primary IP wins over the location scan — changing `server_type` is a resize, and
  re-running the installer is not a request for one. It is cached in
  `.smallworlds-cache.env` like every other answer, and `runner.write_installer_cache()`
  carries it forward (the wizard never asks for it, so dropping it there would send the
  next run back to the default).

  Two traps, both of which have already bitten. The scan is deliberately three-valued
  (`SW_CAPACITY_STATUS`: `ok` / `no-type` / `unknown`) and only a definite, answered
  "nowhere" blocks — a misspelled type name and a lookup that failed are each reported as
  themselves, because treating either as "sold out" stopped an install while the Console
  plainly showed the size in stock. And the Primary IP's location must be read through
  `location_of()`: Hetzner reports `location` on the Primary IP itself, while the nested
  `datacenter` object is the older form and is not always sent — reading only the old one
  silently put the IP *address* into `$HETZNER_LOCATION`.
- **Which DNS records exist.** `var.apps` carries the selected applications; `main.tf`'s
  `local.app_subdomains` maps them to subdomains and only those get an A record, alongside
  `local.core_subdomains` (always deployed) and the apex on production. The map mirrors
  `admin-tools/generate_domain_patches.py` and `plan.py`'s `_SUBDOMAIN_BY_APP` — change one,
  change all three. The PTR follows too: no Stalwart means no `mail.<domain>` to point at.
  `var.apps` defaults to every optional app, so a `terraform apply` that does not pass it
  keeps the zone it had.

**A VPS at another provider** is not a third target: it is `local` with internet exposure and `HOST_FIREWALL` (`docs/adr/0055`, `doc/local-deployment.md`). The bootstrap's nftables allow-list mirrors the `hcloud_firewall` rules in `main.tf` — a port opened in one needs opening in the other (`FIREWALL_TCP_PORTS`/`FIREWALL_UDP_PORTS` in `smallworlds-init.sh`).

**DNS on an exposed `local` machine is `hetzner` or `manual`** (`LOCAL_DNS`, `docs/adr/0056`). Manual means one operator-kept wildcard record, `ACME_CHALLENGE=http01` in the bootstrap (same issuer name, different solver), no DDNS job, no zone creation and no Hetzner token in any Secret. Anything new that writes a DNS record must have a manual-mode answer — print the record, as `setup-vpn.sh` does for `admin.<domain>`.

**Rebuilding a node:** `SMALLWORLDS_REPLACE_NODE=1 ./smallworlds-init.sh` passes
`-replace=hcloud_server.smallworlds_pilot_node` to the apply (guarded on the resource
actually being in state — `-replace` on a missing one aborts the whole apply). This is the
only way a cloud-init change reaches an existing node: cloud-init runs on first boot only,
and the server has `ignore_changes = [user_data]`. It must go through Terraform, not the
Console: Hetzner refuses to delete a server with volumes attached, and Terraform already
orders the detach/delete/create/re-attach. Volumes (`prevent_destroy`) and the Primary IP
(not Terraform-managed) survive.

## What a node installs: `infrastructure/bootstrap-pins.env`

The K3s version, the K3s install script and the Argo CD manifest are pinned in one file,
each with an immutable URL and a SHA-256 (`docs/adr/0057`). Every bootstrap path reads it
and verifies the downloads **on the node**: `bootstrap-local-node.sh` sources it (it is
copied to the machine beside the script), both Terraform roots parse it into
`local.bootstrap_pins` for the cloud-init template, and `build-golden-image.sh` sources it.
There is no bootstrap archive, lock file per release, or signing step any more — a release
is a tag.

- **Never fetch `https://get.k3s.io` or a branch URL in a bootstrap path.** A moving
  address under a fixed digest is what stopped every install from `v1.2.43` when upstream
  edited the script. `admin-tools/test-bootstrap-pins.sh` rejects one; `--online` re-checks
  both digests against upstream and belongs before a tag.
- The file is plain `KEY=value` only (no quotes, no expansion) because Terraform parses it
  with a regex. Change version, URL and digest together.
- A pin change reaches **new and rebuilt nodes only**. Never lower the K3s pin below what
  running clusters have: a rebuilt Hetzner node re-attaches the existing etcd data.
- `admin-tools/lab/lab-node-bootstrap.sh` and the unused `terraform/modules/worker-node`
  template still fetch `get.k3s.io` unverified; they are outside the install paths.

## Waiting for a Hetzner node (`smallworlds-init.sh`)

**Never treat "port 22 answers" as "the node is ready".** `sshd` binds early in boot, while
cloud-init's `runcmd` still has to `rm -rf /var/lib/rancher/k3s/server` and re-link the
directory onto the data volume — so anything written there beforehand is deleted. That is
what silently emptied the cluster secrets: they were scp'd in, the copy was unchecked, the
local copy was then `rm`'d, and the stage was ticked green. `wait_for_cloud_init()` is the
real gate; the secrets are copied only after it, and read back with `test -s` before the
stage is marked done. The cloud-init template's own `write_files` comment says the same
thing about the same directory.

Every wait goes through `sw_wait_for <seconds> <what> <command...>`: bounded, with a
heartbeat, and `report_node_stuck()` for the guidance. A silent unbounded `until ssh …;
sleep 2; done` is indistinguishable from a hang, which is exactly how it was experienced.
Overrides for the impatient and for tests: `SW_SSH_TIMEOUT`, `SW_CLOUD_INIT_TIMEOUT`,
`SW_K3S_TIMEOUT`, `SW_ARGOCD_TIMEOUT`, `SW_WAIT_REPORT_SECONDS`, `SW_CONVERGE_ATTEMPTS`,
`SW_CONVERGE_INTERVAL`. Argo CD's password wait is deliberately **non-fatal** — the cluster
is up by then, so a slow Argo CD hands over an instruction, not a failure.

Tests: `admin-tools/test-hetzner-placement.sh` (stubbed Hetzner API, ssh/scp and terraform;
covers a fresh project, a pinned Primary IP, no capacity anywhere, the cloud-init race, a
failed secrets copy, and each wait running out).

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

`argocd-apps.md` (sync waves) and `bases.md` (init job bases) are summarized above. `install-contabo-datalix.md` is a start-to-finish operator guide for one fixed combination (Contabo VPS, Datalix S3, Gallery, wizard route) — it deliberately discusses no alternatives, so keep it in step with the wizard's field labels when those change. Its steps 7–14 are also the only place the post-install order is written down (tailnet before closing ports, tailnet before member invites, `admin`/`operator` are not member accounts), and `local-deployment.md` points there — keep them current when `setup-vpn.sh`, `smallworlds-firewall` or `bulk-invite.py` change. `local-deployment.md` covers the LAN/local-server target (requirements, DNS/TLS differences, lifecycle). `pod-archive.md` covers the append-only personal data pods: the `pod-gateway` protocol, the Immich export bridge, and the member home device — read it before touching `tenants/pod-gateway` or `tenants/immich/pod-export`, and note that Immich originals are deliberately *not* covered by `pv-backup` or the offsite replicator any more (`docs/adr/0047`). `storage-and-backup.md` maps where every app's data lives (PVs/PVCs, databases, Garage buckets), the backup chain and its known gaps, and per-layer scale-up procedures — consult it before touching CNPG `backup:` blocks, PV sizes, or anything under `tenants/backup-replicator`/`velero`. `mail.md` covers who sends mail and how, and the design (`docs/adr/0049`, not yet implemented) under which mail is opt-in entirely — onboarding uses out-of-band action-token links and alerting moves to HTTP push, with a fixed `smtp-relay` endpoint only for clusters that do select mail — read it before touching any SMTP setting or `ADMIN_EMAIL`. `hardening.md` is the single reference for why member apps are public while operator interfaces are tailnet-restricted, and for the edge/auth/containment layers that pay for that — read it before changing an ingress annotation, a rate limit, or a NetworkPolicy. Also present: `bases.md`, `plane-architecture.md`, `tenant-dashboard.md`, `tenant-forgejo.md`, `tenant-hermes.md`, `tenant-immich.md`, `tenant-keycloak.md`, `tenant-nextcloud.md`, `tenant-operator-console.md`, `tenant-other.md`, `tenant-stalwart.md` — check the relevant one before making non-trivial changes to that subsystem, as several encode hard-won fixes (version incompatibilities, ordering bugs) that aren't obvious from the manifests alone.

## Project-wide contracts

- **OIDC client secrets**: any tenant using `bases/keycloak-client-job` must read `keycloak-secret` (`clientId`, `client-secret`) — never define a custom secret name for this (`.agents/AGENTS.md`).

## Agent skills

### Issue tracker

Issues and PRDs are tracked as local Markdown under `.scratch/`; external PRs are not a triage surface. See `docs/agents/issue-tracker.md`.

### Triage labels

The tracker uses the five default triage labels. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository using root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.
