# Operator Console (in-cluster)

The Operator Console is the privileged interface through which an Operator
observes and maintains a running SmallWorlds cluster. Its source lives in
[`operator-console/`](../operator-console/); this note covers the tenant that
runs it inside the cluster.

The same Go binary is both products. On an Operator's computer it is the
Bootstrap Launcher, bound to loopback and entered with a one-time token. In the
cluster it is started with `--serve-console`, authenticates through Keycloak, and
governs every route with a server-side Console Role check. One build, one
embedded client, one test suite — the console cannot drift from the launcher it
grew out of.

## How it is delivered

Unlike Hermes and Remediation, which run their Python straight from a ConfigMap
in a stock image, a Go binary with an embedded Svelte client needs a real build.
The image is built from `operator-console/Dockerfile` and published by
`.github/workflows/publish-operator-console.yml` to
`ghcr.io/stephan271/smallworlds-operator-console:<release tag>`, pinned in
`deployment.yaml`.

Publishing an image changes nothing anywhere on its own: a cluster picks it up
only when its GitOps Overlay bumps the pinned SmallWorlds release, like every
other change in this repository.

## What it reads, and what it may write

The console's ServiceAccount is deliberately narrow (`rbac.yaml`):

- **Read, cluster-wide**: Argo CD Applications; Deployments, StatefulSets,
  DaemonSets, Jobs; PersistentVolumeClaims, Endpoints, Services; Ingresses and
  Traefik Middlewares; cert-manager Certificates; CNPG and Velero backup objects.
- **Write, own namespace only**: its `ChangePlan` and `WorkflowRun` custom
  resources.

Two properties are worth preserving when this is edited.

**No permission to read Secrets, anywhere.** Certificate readiness is observed
from the cert-manager `Certificate`'s Ready condition rather than from the TLS
Secret, precisely so the console never has to be trusted with secret material to
answer "is TLS ready?".

**No permission to change the cluster.** Every mutation goes through the GitOps
Overlay as a proposal (ADR 0008). A console that could patch a workload would
hold a capability the design does not want it to have.

## How an assessment is put together

`internal/observers` reads the cluster; `internal/assessment` decides state. The
split is strict (ADR 0020) and one consequence is load-bearing:

**Runtime evidence is read from Kubernetes directly, not from Argo CD's health
roll-up.** If it came from the same Argo status as the delivery facet, the
engine's central invariant — an Argo-Healthy delivery over a not-yet-ready
workload never reads healthy — would be vacuous. So the console follows the
Application's managed-resource list to the actual Deployments, claims, and
Service endpoints, and asks them.

Other distinctions the observers encode:

| Situation | What the console reports |
| --- | --- |
| A read fails or is forbidden | **Unknown** — never healthy |
| An Argo Application does not exist yet | **Pending** — declared, awaiting delivery |
| A controller status behind the object's generation | **Not ready** — it describes the previous generation |
| A Service with no ready endpoints | **Probes failing** — an unready pod is removed from endpoints |
| An Ingress with no entrypoint annotation | **Publicly reachable** — Traefik serves it on every entrypoint |
| A router whose middleware allows only private ranges | **Not publicly reachable** |
| A middleware that cannot be read | **Publicly reachable** — the conservative answer to "is this exposed?" is yes |

Evidence for one capability is gathered in a single cycle and cached briefly, so
the overview — which assesses every capability on each request — does not turn
one page view into hundreds of API calls. The cache timestamp is also what the
staleness rules judge.

## Access: private without a Private Gateway yet

The console is an operator interface and must not be reachable from the public
internet (ADR 0012, ADR 0038). The Private Gateway that will eventually carry it
is separate work. Until it exists, the same property is enforced by an address
restriction in front of the router: the route sits on the ordinary `websecure`
entrypoint but a Traefik `ipAllowList` middleware accepts only the Headscale
tailnet (`100.64.0.0/10`), private LAN ranges, and loopback.

This is also what the console's own access facet observes — it reads the
middleware, not merely the entrypoint — which is why it can report itself as
properly private rather than as publicly exposed. When the Private Gateway lands,
the middleware is replaced by the gateway entrypoint and the observer already
understands that shape too.

## Console Roles

`console-roles-job.yaml` creates the three roles (`observer`, `operator`,
`owner`) on the console's Keycloak client. It creates them and nothing else: who
holds one is not decided by a Job. The first Owner is claimed once through the
Bootstrap Launcher (ADR 0043); further grants are ordinary Keycloak role
assignments.

A user Keycloak authenticates but who holds no Console Role is **denied by
default** and receives no session. That is correct, and it is also the reason a
cluster whose roles were never assigned has a console nobody can enter — check
role assignment first when a login redirects back with `auth_error=no_console_role`.

## Configuration

Read once, at startup; a missing required value fails the process rather than
producing a console that silently cannot complete a login.

| Variable | Meaning |
| --- | --- |
| `SMALLWORLDS_OIDC_ISSUER` | The Keycloak realm URL. Authorize, token, and JWKS endpoints are discovered from it, so a realm move cannot leave a stale endpoint behind. **Required.** |
| `SMALLWORLDS_OIDC_CLIENT_ID` | From `keycloak-secret`'s `clientId` key (project-wide contract). **Required.** |
| `SMALLWORLDS_OIDC_CLIENT_SECRET` | From `keycloak-secret`'s `client-secret` key. |
| `SMALLWORLDS_CONSOLE_URL` | The console's own address as a browser sees it. Must match the redirect URI registered on the Keycloak client exactly. **Required.** |
| `SMALLWORLDS_BASE_DOMAIN` | Base domain for the Grafana and Argo CD deep links. |
| `SMALLWORLDS_SESSION_KEY` | Signs session cookies. Optional; without it a restart signs every Operator out. |
| `SMALLWORLDS_CONSOLE_NAMESPACE` | Where the Activity Record is written. Default `operator-console`. |
| `SMALLWORLDS_ARGOCD_NAMESPACE` | Default `argocd`. |
| `SMALLWORLDS_ROOT_APPLICATION` | The app-of-apps whose managed resources are the authority on what the overlay declares. Default `smallworlds-root`. |
| `SMALLWORLDS_GATEWAY_ENTRYPOINT` | Default `private-gateway`. |
| `SMALLWORLDS_PUBLIC_ENTRYPOINTS` | Comma-separated. Default `web,websecure`. |

The three domain-bearing values are rewritten per community by the overlay's
domain patches (`admin-tools/generate_domain_patches.py` and its Go counterpart
`internal/capability/domain.go`, kept in step by a parity test). The OIDC
redirect URI the console sends, the redirect URI Keycloak accepts, and the
Ingress hostname are three separate places that must agree; a login fails
outright if any one of them still names another domain.

## The Activity Record

Change Plans and Workflow Runs are compact custom resources in
`admin.smallworlds.network/v1alpha1` (ADR 0025), so they survive a restart
without a second application database and inherit Velero protection. Detailed
events are never stored inline — a run carries a Loki query
(`{app="operator-console",run="<id>"}`) instead, which is what keeps these
objects small enough to belong in etcd. The CRD schemas mirror the size budgets
the console enforces before writing, so an oversized record is refused by the API
server too.

```bash
kubectl get changeplans,workflowruns -n operator-console
```

## The console is itself a Cluster Capability

`operator-console` is an ordinary entry in the capability catalog. That is
deliberate: it lets the console assess and report itself degraded, and it is why
the console must never be the only path by which a failure can be noticed — the
Alertmanager → Remediation → Hermes chain is independent of it.

## Known outstanding work

- The **Private Gateway** itself (a Headscale-joined proxy and a dedicated
  Traefik entrypoint), and moving Grafana and Argo CD behind it with their
  read-only Keycloak OIDC clients. Until then those two remain on their current
  ingress and the console's deep links point at them as they are.
- **Live protection evidence.** The protection inventory declares every dataset
  and the assessment folds it in, but no live reader is wired yet, so the
  protection facet reads unknown rather than reporting a stale Recovery Point.
- **NetworkPolicies** for the console pod, which belong with the gateway work
  where the traffic paths are actually pinned down.
