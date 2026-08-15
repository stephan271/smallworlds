# Hardening

What protects a cluster that is deliberately on the public internet, and what
is deliberately not attempted. Related: `docs/adr/0038` (operator interfaces are
private), `doc/tenant-keycloak.md` §5–§6 (authentication and the admin path).

> **Implementation status.** Everything in §1–§4 is in the manifests. None of it
> has been exercised against a running cluster; the rate limits and the network
> policies in particular are the kind of change that looks right and fails in
> production, so validate on staging (`admin-tools/test-pr-locally.sh`) before
> relying on them. §5 is what is not done.

## 1. The exposure decision

Member applications stay reachable from the public internet. Two independent
reasons make network-level restriction unworkable for them: **share links have
to reach non-members**, who are arbitrary addresses on the internet, and members
open the apps from phones and work laptops. A tailnet would also cost the
property that onboarding requires no installed software (`doc/mail.md` §1).

Operator interfaces are the opposite: nobody outside the operator ever needs
them, so they carry an `ipAllowList` restricting them to the Headscale tailnet,
private LAN ranges and loopback.

| Public | Restricted |
|---|---|
| Nextcloud, Immich, Collabora, Forgejo, Plane, Excalidraw, Bulwark, dashboard, Jitsi, pod-gateway, Headscale, `identity/realms/smallworlds` | Operator Console, ArgoCD, Grafana, `identity/admin`, `identity/realms/master` |

The accepted cost is a larger attack surface, paid for by the layers below and
by keeping versions current (Renovate, Trivy).

## 2. Edge protections (`bases/edge-protection`)

Every public application ingress carries two Traefik middlewares:

- **`edge-ratelimit`** — 100 requests/second per source IP, burst 200. Generous
  because a photo grid or file list issues a burst of asset requests and a whole
  household can share one address. This stops scanners and crude floods; it is
  *not* the control for credential stuffing.
- **`edge-headers`** — `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: strict-origin-when-cross-origin`, and HSTS for 180 days.

Consumed per tenant rather than set as a default on the `websecure` entrypoint.
One entrypoint argument would be far less YAML, but Traefik drops a router whose
middleware is missing, so an unsynced or deleted CR would take down every route
at once instead of one application visibly.

Two omissions are deliberate. **`frameDeny` is absent** because Nextcloud embeds
Collabora in an iframe, which is exactly how a non-member opens a shared
document. **HSTS carries neither `includeSubDomains` nor `preload`**, because
both commit every present and future subdomain of the operator's zone to HTTPS
and preload is effectively irreversible.

**Jitsi and pod-gateway are excluded.** Jitsi's signalling is WebSocket and its
media path is latency-sensitive; pod-gateway serves bulk appends from members'
home devices, where 100 requests a second could throttle a legitimate sync. Both
need measuring against real traffic before they get a limiter.

## 3. Authentication

- The `smallworlds` realm is **passwordless** — passkey or recovery code, no
  password form reachable from the bound flow (`doc/tenant-keycloak.md` §5). A
  stolen password is not a threat that exists here.
- **Brute-force detection is on in both realms.** The `smallworlds` realm sets it
  at import; `master` is created by Keycloak with it off, so `realm-config-job`
  turns it on (`failureFactor: 30`, temporary lockout only — permanent lockout on
  the account every OIDC registration job authenticates with would be a
  self-inflicted outage).
- The master realm's human administrator (`operator`) is separate from the
  automation account (`admin`) and enrols TOTP at first login.

## 4. Containment (`bases/db-network-policy`)

The pod network was flat: any compromised workload could open a socket to every
PostgreSQL instance in every other tenant — Keycloak's user table, Immich's
asset index, Nextcloud's file metadata — regardless of which application had the
vulnerability.

Each database now accepts ingress only from its own namespace, the `cnpg-system`
operator, and `monitoring` (every `cnpg-cluster.yaml` sets
`enablePodMonitor: true`, so a blanket deny would silently take database metrics
with it). Keycloak's database additionally accepts the `stalwart` namespace,
which is the one legitimate cross-namespace database dependency in the cluster —
`mail-provisioner` reads Keycloak's database directly to discover new users.

This restricts **ingress to database pods only**. A namespace-wide default-deny
needs per-application egress knowledge that does not exist in this repo yet, and
a wrong rule there fails in ways that are tedious to diagnose. Here the blast
radius is one database, the symptom is unambiguous, and deleting the policy
restores the previous behaviour.

k3s runs its kube-router policy controller — the install line passes only
`--disable traefik` — so these are enforced, not decorative.

## 5. Node

`unattended-upgrades` is installed and configured for **security origins only**,
with `Automatic-Reboot "false"`. This is a single-node cluster: an unattended
reboot takes every service down without warning.

The consequence is that **kernel and libc updates need a manual reboot**. Nothing
currently alerts on this — check `/var/run/reboot-required` on the node. Wiring
that into Alertmanager is listed below.

The local deployment target does not get this: `bootstrap-local-node.sh` installs
no packages, and imposing an update policy on a machine the operator already owns
and uses for other things is not this project's decision to make.

## 6. Not done

- **A pending-reboot alert.** Security patches land automatically but the reboot
  that activates kernel updates is manual and unmonitored.
- **Rate limits for Jitsi and pod-gateway** (§2).
- **CrowdSec or equivalent** — behavioural IP banning across all ingresses, which
  is the largest remaining lever against scanners and distributed brute force. It
  is a real new component (agent, local API, Traefik bouncer) with its own
  operational weight and false-positive risk.
- **Pod security contexts** — `runAsNonRoot`, dropped capabilities,
  `readOnlyRootFilesystem`. Trivy-operator already reports these; applying them
  is per-chart and some applications break.
- **Namespace-wide default-deny NetworkPolicies** (§4).
- **A TLSOption** pinning minimum version and cipher suites. Traefik's defaults
  are already sane, so this is low value.
- **Application-level hardening** inside Nextcloud and Immich, each of which has
  its own security checklist.
