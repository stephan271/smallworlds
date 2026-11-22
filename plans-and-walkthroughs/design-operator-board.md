# Operator Board (`ops.<domain>`) — Design

**Status:** proposed; nothing below is implemented unless marked *exists*
**Date:** 2026-11-22
**Decision record:** `docs/adr/0068` (one read-only Operator Board leads to
every operator tool)
**Builds on:** `docs/adr/0051` and `docs/adr/0062` (the console's observation
half, left open), `docs/adr/0066` (router pairs), `doc/alerting.md`,
`plans-and-walkthroughs/design-per-app-updates.md` §6 (Ring State)

## 1. Why

An Operator today keeps watch through eleven separate places: Argo CD, Grafana,
the Keycloak admin console and the Invitation Desk on the Private Network; the
Member Dashboard on the public address; ntfy and healthchecks.io outside the
cluster; the Ring 0 Board, Renovate's PRs and the Steward's incidents on GitHub;
and the Update Desk on their own machine. Each answers its own question well.
None answers the first one: **is my cluster well, and if not, where do I go?**

The Operator Console tried to answer it and was deleted for its size
(`docs/adr/0062`). Its observation half (node readiness, every Application's
sync and health, the reason the cluster gives for what has not settled) was
left without a replacement. This design is that replacement, plus the links to
every tool, kept small enough to stay maintained.

## 2. What this is

One page, read-only, on the Private Network, on every cluster.

```
┌──────────────────────────────────────────────────────────────────────────┐
│ smallworlds.network · Operator Board                     as of 14:02:31  │
│                                                                          │
│ ● 2 things need attention                                                │
│   CNPGBackupStale (gallery-db, critical): no backup for 31 h   → Grafana │
│   Application nextcloud: Progressing for 12 min               → Argo CD │
├────────────┬────────────┬────────────┬────────────┬────────────┬────────┤
│ Alerts     │ Apps       │ Node       │ Backups    │ Certs      │ Updates│
│ ● 1 crit.  │ ◐ 37/38    │ ● Ready    │ ● 1 stale  │ ● 14 ok    │ ◐ 3    │
│   0 warn.  │   Healthy  │   disk 61% │   gallery  │   next exp.│ behind │
│            │            │   K3s 1.34 │            │   in 61 d  │ Ring 0 │
├────────────┴────────────┴────────────┴────────────┴────────────┴────────┤
│ Alert delivery: push sent 3 min ago · heartbeat sent 2 min ago           │
├──────────────────────────────────────────────────────────────────────────┤
│ Operate       Argo CD · Grafana · Logs (Grafana Explore) · Keycloak admin │
│ Members       Invitation Desk                                            │
│ Alerts        healthchecks.io · ntfy app                                 │
│ Updates       Ring 0 Board · Renovate PRs · ./smallworlds-update …       │
│ Reference     Post-install steps · Backup & restore · Alerting           │
├──────────────────────────────────────────────────────────────────────────┤
│ Applications  (cards, as on the Ring 0 Board, problems first)            │
└──────────────────────────────────────────────────────────────────────────┘
```

### It is not

- **A console.** It has no buttons that change anything (ADR 0068). Links open
  the tool that does.
- **A status page for members.** Members see the Member Dashboard. A
  member-facing status page is a separate question
  (`plans-and-walkthroughs/implementation_plan-statuspage.md`, not built).
- **A second alerting system.** It shows Alertmanager's alerts. It does not
  invent its own thresholds (§4.1).
- **A view across clusters.** One Board per cluster (§10).

## 3. Where it runs

| | |
|---|---|
| Tenant | `infrastructure/kubernetes/tenants/operator-board/`, namespace `operator-board` |
| Application | `apps/operator-board.yaml`, sync wave **0** (needs no Keycloak) |
| Code | Python 3.14 stdlib, `python -u /app/main.py` in `python:3.14-slim`, source from a `configMapGenerator`, `PYTHONPATH=/app` (CLAUDE.md, "Stdlib services") |
| State | none: no PVC. Everything is read again every 30 s and kept in memory |
| Host | `ops.<domain>`, a new core subdomain (§8) |
| Exposure | router pair: `operator-board-private` on `operators`; `operator-board` on `websecure` with the LAN-only `ipAllowList` (same ranges as `join-desk-private`) |
| Sign-in | none of its own (ADR 0068). `gethomepage.dev/enabled: "false"` on both Ingresses |
| Update Unit | Platform (it is not in `update-units.yaml`) |

It runs on Ring 0 as well. That is how its code gets tested against a real
cluster on every commit (§9).

## 4. What it shows

### 4.1 The verdict and the colours

The page reads every source on one schedule (default 30 s, `REFRESH_SECONDS`),
builds one state document, and serves it at `/api/state`. The page polls it.

| Colour | Means | Comes from |
|---|---|---|
| **red** | an alert of severity `critical` or `warning` fires | Alertmanager only |
| **amber** | something has not settled, or is close to a limit, and no alert fires for it yet | the Board's own reads |
| **grey** | this source did not answer; its last good answer is shown with its age | any source |
| **green** | the source answered and nothing above holds | — |

The top line says the worst of these with a count. Below it are the reasons, one
line each: alert name, the object it is about, its `summary`, and a link to it.
Red comes first, then amber, then grey. An alert that is `silenced` or
`inhibited` is shown greyed beneath the rest, never counted.

Each alert is put on one tile by a fixed table, by alert name first and the
`namespace` label second:

| Tile | Alerts |
|---|---|
| Backups | `CNPG*`, `Velero*`, `PodArchive*`, `Backup*` |
| Node | `Node*`, `KubeNode*`, `PersistentVolume*`, `KubeletDown`, `TargetDown` for node-exporter |
| Applications | `Application*`, `KubePod*`, `KubeDeployment*`, `KubeStatefulSet*`, `KubeJob*` |
| Certificates | `Certificate*` (none exist yet, §7) |
| Updates | `Canary*` (Ring 0 only) |
| Alerts | everything else |

A test checks that every rule in `apps/*-alerts.yaml` and
`tenants/canary-follower/alerting.yaml` lands on the tile its name suggests, so a
new rule has to be placed on purpose.

*Settled while building slice 02:*

- An alert belongs to an Application when it names it (`ApplicationNotHealthy`
  carries `name`) or fires in the namespace the Application deploys to. That
  card turns red with the alert's name on it. A red card is one problem and is
  named once, by its alert. The card still shows Argo CD's reason.
- Severities `none` and `info` never count. The push route drops them too.
- `Watchdog` missing is amber, not red: no alert can say that the alert
  pipeline is broken, so the Board says it.
- When Alertmanager stops answering, the alerts it last reported stay red,
  with the Alerts tile grey. Hiding a known alert because Alertmanager
  blinked would be worse than a stale red.

### 4.2 Applications

From `applications.argoproj.io` in `argocd`: sync status, health, the running
operation, and the reason Argo CD gives (`status.conditions`,
`status.operationState.message`, health messages of resources that are not
Healthy), cut to 300 characters as in `ring0.json`.

The rules that turn Argo CD's fields into words (sync failure, "Degraded while
rolling", upstream unreachable) are the ones the Canary Follower already uses
(`tenants/canary-follower/src/argocd.py`, *exists*). Kustomize cannot load a file
from another tenant's folder, so the Board carries a copy of those functions in
`src/argo.py`, and `tests/test_argo_parity.py` runs both on the same fixtures.
A difference fails the test.

The cards: one per Application, problems first, grouped as Platform and
Community Applications by the same list `ringstate.py` uses. Each links to
`https://deploy.<domain>/applications/argocd/<name>`.

Amber: OutOfSync, Progressing, Missing, Unknown, or an operation that has been
running for over 10 minutes. Red only when `ApplicationNotHealthy` or
`ApplicationNotSynced` fires (`apps/update-alerts.yaml`, *exists*).

### 4.3 Node

From `nodes`: Ready, K3s version (`status.nodeInfo.kubeletVersion`), kernel,
the age of the last reboot, and `DiskPressure`/`MemoryPressure`. From
Prometheus: free space on `/` and on `/mnt/smallworlds-data` and their
`predict_linear` days left, the same expressions as `apps/node-disk-alerts.yaml`
so the numbers match the alerts.

### 4.4 Backups

- **CNPG:** per `clusters.postgresql.cnpg.io`. Amber past 26 h (daily schedule
  plus slack), or when `LastBackupSucceeded` or `ContinuousArchiving` is
  `False`. *Learned in slice 03:* with the barman-cloud plugin the Cluster's
  `status.lastSuccessfulBackup` stays empty. The time is in the plugin's
  `ObjectStore` (`barmancloud.cnpg.io`), `status.serverRecoveryWindow[<server>]
  .lastSuccessfulBackupTime`, the same source as the metric `CNPGBackupStale`
  reads. The server is the Cluster's plugin parameter `serverName`, else the
  Cluster's name. An ObjectStore keeps servers no Cluster writes to any more
  (Ring 0's `immich-database` from before PostgreSQL 18), so the Board follows
  each Cluster to its own.
- **Velero:** the newest `backups.velero.io` per schedule, its phase and
  completion time.
- **File copies and off-site:** every CronJob whose name says `backup` or
  `replicator`: amber when its last scheduled run has not succeeded within two
  hours, or a daily one's last success is older than 26 h. A suspended one is
  shown as a fact, not a failure: suspending is a choice made in git, and
  Ring 0 suspends its replicator on purpose (its storage checks run it).
- **Pod archive:** only its alerts. Device state lives in `pod-gateway`, which
  the Board does not read.

The tile links to `doc/storage-and-backup.md` on the base repository at the
commit this cluster runs.

### 4.5 Certificates

From `certificates.cert-manager.io`: Ready, `status.notAfter`, and the
`Issuing` condition's message. Amber when not Ready or fewer than 14 days are
left. cert-manager renews 30 days ahead, so 14 days means renewal has been
failing for two weeks. This reads the Certificate resources, not metrics, so it
needs nothing that is not collected today.

No alert covers certificates, so this tile is never red. An alert would need
cert-manager's metrics, which Prometheus does not collect today. That is
deferred (§10): until then, a certificate that cannot renew shows amber here
and reaches no phone.

### 4.6 Updates

Where this cluster stands against Ring 0:

1. **What runs.** The base commit is `status.sync.revision` of the Applications
   whose source is the base repository. After the Update Desk's base-pin block
   they all name the same commit. If they do not, the tile says "mixed" and
   lists them.
2. **What Ring 0 verified.** `ring0.json` from the `ring-state` branch (*exists*):
   `head`, `now.state` (`up-to-date`/`updating`/`stuck`) and `commits`.
3. **How far behind.** If the running commit is among `commits`, it is counted
   from there. Otherwise the Board asks GitHub's public compare API
   (`/repos/<repo>/compare/<running>...<verified>`, no token, at most once an
   hour, cached), which gives `ahead_by`.
4. **What to do.** The tile shows the command that starts the local Update Desk,
   `./smallworlds-update --overlay <checkout> --kubeconfig <file>`, and links to
   the Ring 0 Board and the base repository's open Renovate PRs.

`ring0.json` is at most five minutes stale (design-per-app-updates.md §6). The
tile shows its `updated` time, and greys out once that is older than an hour.
`RING_STATE_URL=` (empty) turns the internet reads off, and the tile says
"unknown". On Ring 0 the tile says "this is Ring 0" and shows `now` only.

Amber when one or more verified commits are newer than what runs. Red never:
being behind is not an outage. Ring 0 being `stuck` is shown as a fact about
Ring 0.

*Settled while building slice 04:*

- "Behind" counts commits on `main` with a `success` verdict, newer than the
  one this cluster runs. Branch commits carry no verdict and a rejected one is
  not a place to go, so neither counts. The tile says "verified commits", not
  "steps": the Update Desk groups commits into steps of its own.
- The Board knows it runs on Ring 0 when an Application `canary-follower`
  exists. There the tile shows Ring 0's own state from `now` (up to date,
  checking what, stuck), and amber only when Ring 0 is stuck.
- A cluster running a commit Ring 0 has not verified yet, or one it rejected,
  is told so rather than counted as behind.
- One compare call per pair of commits per hour, kept only for the current
  pair; a failing compare (GitHub's 60 calls an hour) is noted and the tile
  says "more than 25 commits behind", which is still true.

### 4.7 Alert delivery

Whether alerts can reach the Operator, read from Alertmanager's own metrics in
Prometheus:

- `alertmanager_notifications_total` and `…_failed_total` by `integration`
  over the last hour. The heartbeat is a webhook every 5 minutes, so
  "webhook sent within 15 min, none failed" means the heartbeat is out.
- `alertmanager_config_last_reload_successful`.
- Whether the two `AlertmanagerConfig`s `smallworlds-alerts` and
  `smallworlds-heartbeat` exist (read, not their Secret).

Amber when nothing has been sent within 15 minutes or anything failed. The
healthchecks.io side, whether the pings arrive, is not visible without that
service's API key. The tile links to healthchecks.io instead. The ntfy topic is
a credential (`doc/alerting.md`), so the Board never shows or links it. It names
the ntfy app.

*Settled in slice 05 (checked on Ring 1, 2026-11-22):*

- `alertmanager_notifications_total` and `…_failed_total` carry only
  `integration` (and `reason`), no receiver. The heartbeat and the ntfy push
  are both `webhook`. The heartbeat is the only steady sender, so "no webhook
  in 15 minutes" means the heartbeat is not going out; a failure in the last
  hour means a wrong or expired URL.
- prometheus-operator writes no `status` on an `AlertmanagerConfig`, so a
  rejected one is invisible there. Alertmanager's `/api/v2/receivers` lists
  the receivers it loaded, by name and labels only (no URL), so a missing
  `monitoring/smallworlds-alerts/push` or `monitoring/smallworlds-heartbeat/heartbeat`
  shows the rejection without the Board ever seeing a credential.
- The stock `Alertmanager…` rules (failed reload, failed to send, …) are the
  tile's red. It is a tile, like the others, not a strip.

### 4.8 Links

**In-cluster tools** are found, not configured. Each operator interface's
`-private` Ingress carries:

```yaml
smallworlds.network/board-name: "Argo CD"
smallworlds.network/board-group: "Operate"        # Operate | Members | Logs …
smallworlds.network/board-path: "/applications"   # optional
smallworlds.network/board-order: "10"             # optional
```

The Board lists Ingresses cluster-wide (as Homepage does) and builds
`https://<host><path>` from the Ingress's own host, so domain patches carry over
and a tool that is not installed does not appear. First set: Argo CD, Grafana
(plus "Logs" → `/explore`), Keycloak admin (`/admin/master/console/`), the
Invitation Desk (`/desk`), the Board itself (no link).

**Fixed links**, in `src/links.py`: healthchecks.io, the Ring 0 Board, the base
repository's PRs labelled `ring0-queue`, and the docs: `doc/alerting.md`,
`doc/storage-and-backup.md`, the post-install steps of
`doc/install-contabo-datalix.md`, `doc/hardening.md`. Docs link to the base
repository at the commit this cluster runs, so they match what is deployed.

**The GitOps Overlay** link comes from the root Application's `repoURL`. It is
the Operator's own private repository, and the Steward's incidents live there
on Ring 0.

## 5. Architecture

```
            Private Gateway ─► traefik-internal:9443 (operators) ─┐
            node's LAN ─────► websecure + LAN allow-list ─────────┤
                                                                  ▼
                                    ┌──────────────────────────────────────┐
                                    │ operator-board pod (stdlib, :8080)   │
                                    │  collector thread, every 30 s:       │
   kube API (SA, get/list only) ◄───┤   kube.py      Applications, nodes,  │
                                    │                CNPG, Velero, certs,  │
                                    │                CronJobs, Ingresses   │
   Alertmanager :9093  ◄────────────┤   alerts.py    /api/v2/alerts        │
   Prometheus   :9090  ◄────────────┤   prom.py      disk, delivery        │
   raw.githubusercontent.com,  ◄────┤   rings.py     ring0.json, compare   │
   api.github.com (optional)        │  state.py  → one document, colours   │
                                    │  web.py    → /, /api/state, /healthz │
                                    └──────────────────────────────────────┘
```

Files (all in `configMapGenerator`, checked by `tests/test_configmap.py`):

| File | Does |
|---|---|
| `src/main.py` | starts the collector thread and the HTTP server |
| `src/config.py` | env: `DOMAIN`, `REFRESH_SECONDS`, `ALERTMANAGER_URL`, `PROMETHEUS_URL`, `RING_STATE_URL`, `BASE_REPO`, `RING` |
| `src/kube.py` | in-cluster API client with the ServiceAccount token (as the follower's `kube.py`) |
| `src/argo.py` | Argo CD wording, a copy of the follower's (§4.2) |
| `src/alerts.py`, `src/prom.py`, `src/rings.py` | one source each. Each returns `(data, error, fetched_at)` and never raises |
| `src/state.py` | combines the sources, maps alerts to tiles (§4.1), sets colours. Pure, so it is where the tests go |
| `src/links.py` | annotations → links, plus the fixed list |
| `src/web.py` | `/` (static page), `/api/state`, `/healthz`. GET only. Any other method gets `405` |
| `static/board.html`, `board.css`, `board.js` | plain HTML/CSS/JS, no framework. Card styles copied from `ring0-board/` |

Every source has its own timeout (5 s) and is read in turn, so one slow source
delays the refresh and does not stop it. The document carries each source's
`fetched_at` and `error`. The page draws grey from those, so the page itself
never has to guess.

**Security headers** come from `bases/edge-protection` like every tenant. The
page sets `Content-Security-Policy: default-src 'self'` and loads nothing from
outside.

## 6. Permissions

A `ClusterRole` `operator-board-reader` (and a `Role` in `argocd` for the
Applications), `get`/`list`/`watch` only. Each slice adds the rows it reads.
Slice 01 has Applications and Ingresses:

| API group | Resources |
|---|---|
| `""` | `nodes`, `namespaces` |
| `argoproj.io` | `applications` |
| `postgresql.cnpg.io` | `clusters`, `backups`, `scheduledbackups` |
| `velero.io` | `backups`, `schedules` |
| `cert-manager.io` | `certificates` |
| `batch` | `cronjobs` |
| `networking.k8s.io` | `ingresses` |
| `monitoring.coreos.com` | `alertmanagerconfigs` |

No Secrets, no `pods/log`, no `pods/exec`, no ConfigMaps, no events, no verb
that writes. `tests/test_rbac.py` fails on any of those, so a later "just add
Secrets" has to change the test on purpose.

**NetworkPolicy** `operator-board`: ingress on 8080 from Traefik's pods only.
Egress is not restricted in slice 01 (*learned while building it*): its only
destination is the Kubernetes API, whose address on k3s is each node's own, so
a policy would need the node's address per install. Egress to DNS, Alertmanager
`9093` and Prometheus `9090` in `monitoring`, and `443` outside the cluster
ranges come with issues 02–04, and the API server then needs an answer of its
own (an `ipBlock` filled in by the installer, or none).

**What never appears on the page:** Secret contents, log lines, member names or
e-mail addresses, the ntfy topic, the healthchecks.io ping URL. Alert
`annotations.description` is shown because the rules are ours. A test checks
every rule in the repository for `{{ $labels.* }}` keys that could carry
member data (today: none).

## 7. Alerts it relies on, and the ones it adds

*Exist:* `apps/backup-alerts.yaml`, `node-disk-alerts.yaml`,
`pod-archive-alerts.yaml`, `update-alerts.yaml`, kube-prometheus-stack's
defaults, the follower's `alerting.yaml` on Ring 0.

*Added by this work:*

- `OperatorBoardDown`: the Board's own pod not Ready for 15 min, from
  kube-state-metrics (already collected). Without it, a broken Board would only
  be noticed when someone opens it.

*Deferred:* `CertificateNotReady` and `CertificateExpiresSoon`. They need
cert-manager's metrics, which are not collected today (§10).

## 8. DNS and hosts (`ops.<domain>`)

`ops` is a core subdomain (CLAUDE.md, "Node placement and DNS"), so in the same
PR:

- `infrastructure/terraform/main.tf` `local.core_subdomains`
- `smallworlds-init.sh` `dns_subdomains()` and the "DNS records:" line
- `admin-tools/generate_domain_patches.py`: a host patch for **both** Ingresses
  (patches target by name; a missed one keeps `smallworlds.network`)
- `bootstrap-local-node.sh`'s default `DNS_SUBDOMAINS` and the installer's LAN
  hosts snippet
- `admin-tools/setup-vpn.sh` `OPERATOR_HOSTS`, so the printed hosts line names it
- a manual-DNS install covers it by its wildcard. Nothing to print.
- `admin-tools/prepare-community-repo.sh`: `operator-board` among the
  always-installed applications, so the overlay routes it to a directory of its
  own that carries the domain patches.
- `infrastructure/kubernetes/operator-steps.yaml`: one `after` step for every
  existing community (create the `ops` record, re-run
  `prepare-community-repo.sh` on a domain other than `smallworlds.network`, add
  `ops.<domain>` to the hosts line). It names the merge commit, which exists
  only after the merge, so it lands in a follow-up commit straight after.

*Not in the list, on purpose (learned while building it):* the wizard's
`_hostnames` lists the names members use, and Argo CD and Grafana are not in it
either. The CoreDNS host lists are for names pods resolve to Traefik, and no pod
needs the Board: Ring 0's specs reach it through `traefik-internal` directly.

## 9. Testing

| Where | What |
|---|---|
| `tenants/operator-board/tests/` (unittest, stdlib) | `state.py` colours and verdict from recorded fixtures (healthy, alert firing, a source down, Ring 0 stuck, mixed revisions); alert → tile table against every rule file; `argo.py` parity with the follower; RBAC has no forbidden verb or resource; ConfigMap lists every file; `web.py` refuses non-GET |
| `admin-tools/test-operator-board.sh` | runs the above and renders the tenant with `kubectl kustomize` |
| `admin-tools/test-generate-domain-patches.py` | both Ingresses get the domain |
| Ring 0, `e2e-tests/tests/13-operator-board.spec.ts` | through the Private Gateway: page loads, verdict line present, every tile has a colour, no tile is green whose source shows an error; the links Argo CD/Grafana/Keycloak/Desk are present and answer |
| Ring 0, `11-operator-entrypoint.spec.ts` | `ops.<domain>` answers on `operators`, `404` on `members`, `403` from outside |
| Server dry-run | the new manifests applied with `--dry-run=server` against Ring 0 before the PR |

## 10. Not decided here

- **Several clusters on one page.** An Operator of Ring 0 and Ring 1 opens two
  Boards. Combining them would need each Board to publish its state or one
  Board to reach the other cluster's API. Neither is cheap. It can be revisited
  once both run.
- **Member Invitations on the Board** (open invitations, codes about to expire).
  That would mean reading `join`'s store, so it waits until it is asked for.
- **healthchecks.io's own view** of the heartbeat. It needs an API key in the
  cluster.
- **Certificate alerts.** Collecting cert-manager's metrics (its
  `ServiceMonitor`) and adding `CertificateNotReady`/`CertificateExpiresSoon`
  would let the Certificates tile turn red and reach the phone. Skipped for now
  (2026-11-22). The tile stays amber at most.
- **Steward incidents and Renovate PR counts** for a private overlay. They need a
  GitHub token in the cluster, which ADR 0067 avoided on purpose. They stay
  links.

## 11. Implementation order

Issues in `.scratch/operator-board/issues/`. Each ends on something a person can
see on Ring 0.

| # | Slice | Done when |
|---|---|---|
| 01 | **Tracer:** tenant, Application (wave 0), router pair, `ops` subdomain in every map (§8), the operator step, link annotations on the four operator Ingresses, a page that shows the links and the Applications tile only. Server dry-run on Ring 0 | `ops.canary…` shows Argo CD's Applications and four working links from a tailnet device, and answers `403` from outside |
| 02 | **Verdict and Alerts:** Alertmanager source, alert → tile table and its test, the verdict line, grey for a failed source | a firing test alert on Ring 0 turns the page red with a link to it |
| 03 | **Node and Backups:** nodes, Prometheus disk, CNPG, Velero, replicator CronJobs | Ring 0's backup times shown, matching `kubectl` |
| 04 | **Updates:** `ring0.json`, running revision, compare, the Update Desk command | Ring 1 shows how many steps it is behind, matching the Update Desk |
| 05 | **Alert delivery:** Alertmanager metrics and configs (§4.7) | Ring 0 shows "heartbeat sent N min ago" |
| 06 | **Certificates** tile (from the resources, no metrics) and `OperatorBoardDown` | a test certificate that cannot issue turns the tile amber on Ring 0; scaling the Board to 0 fires the alert |
| 07 | **Docs:** `doc/tenant-operator-board.md`; `doc/hardening.md` restricted row; CLAUDE.md (stdlib services, doc map); README's operator tools; `doc/install-contabo-datalix.md` post-install steps (open the Board first); `doc/auto-updates.md` where the Updates tile appears | docs say where to start |

Ring 1 gets it through the Update Desk like any other step, after issue 01 or
after all seven. Issue 01's operator step tells the Operator about the hosts
line.
