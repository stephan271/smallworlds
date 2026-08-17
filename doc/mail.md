# Mail

How the cluster sends and receives mail, how to run it without the self-hosted
mail server, and which addresses have to exist where. Related: `docs/adr/0049`
(the decision this document implements), `doc/tenant-stalwart.md` (the mail
server itself), `doc/tenant-hermes.md` (one of the senders).

> **Implementation status.** Stalwart is **no longer installed by default**: it
> left the master kustomization and became an entry in `OPTIONAL_APPS`, so a
> community that does not want a mail server does not get one. That is the first
> piece of `docs/adr/0049` to reach the manifests. Section 2's other half still
> holds — the senders that exist are hardwired to Stalwart, so a cluster that
> deselects it has no outbound path at all rather than a fixed `smtp-relay`
> endpoint. Sections 3 onwards remain the target design and are **not yet in the
> manifests**; Section 9 is the remaining work.
>
> Upgrade note: an existing overlay that relied on the base for Stalwart loses it
> on the next release bump. Add `apps/stalwart.yaml` to the overlay's root
> kustomization to keep it.

## 1. Three capabilities, one of them optional

"Mail" is three separable things, and only one of them is genuinely optional:

| Capability | Depends on it | Optional? |
|---|---|---|
| **Outbound transactional** — alerts, optional app notifications | Alertmanager, Hermes | Only if alerts go by mail rather than webhook |
| **Member mailboxes** — `user@domain`, IMAP/JMAP, webmail | Bulwark, `mail-provisioner` | Yes — a Community Application |
| **Inbound role addresses** — `postmaster@`, `abuse@`, DMARC reports | The domain itself | Effectively no, but needs almost nothing |

A cluster with no mail server is normal.

**Onboarding does not require mail.** `admin-tools/bulk-invite.py` defaults to
minting each member's action token through the `action-token-link` SPI and
writing the URLs to a file for the Operator to distribute out of band; `--email`
selects the old `execute-actions-email` path for clusters that do have a relay.
Nor is there a password-reset dependency: `bulk-invite.py` sets no password
credential, and the realm has `resetPasswordAllowed: false`, so no "Forgot
password?" link exists and Keycloak never sends a reset mail. (The bound browser
flow does still offer a password form as an alternative to the passkey — it
simply has nothing to match against. See `doc/tenant-keycloak.md` §5.)
Invitation mode also sets `emailVerified` directly rather than mailing a
verification.

What still needs an egress channel is **alerting** — Alertmanager and Hermes —
and the `self-registration` onboarding mode, which sets `verifyEmail=true`
(`tenants/keycloak/realm-config-job.yaml`) and is meaningless without a way to
verify. Alerting need not be SMTP: Alertmanager's `webhookConfigs` already carry
the Tier 1 remediation route, and pointing alerts at ntfy, Gotify or Matrix
removes the mail dependency entirely.

A fully mail-free cluster is therefore coherent, and is the expected shape of a
LAN deployment: invitation onboarding, passkey login, push alerting, no
mailboxes. Sending is needed only for `self-registration`, member-facing
application notifications, or alerts routed by mail. See `docs/adr/0049`.

## 2. Who sends mail today

All three senders talk to Stalwart's in-cluster service on plain port 25 with
no authentication and no TLS, which Stalwart accepts from the cluster subnets
`10.42.0.0/16` and `10.43.0.0/16` (`doc/tenant-stalwart.md`).

| Sender | Where | From address |
|---|---|---|
| Keycloak | `tenants/keycloak/values.yaml` (`KC_SMTP_*`) | `auth@<domain>` |
| Alertmanager | `apps/alertmanager-config.yaml` (`smarthost:`) | `alertmanager@<domain>` |
| Hermes | `tenants/hermes/src/config.py`, `mailer.py` | `hermes@<domain>` |

Hermes uses `smtplib.SMTP` directly with **neither `starttls()` nor `login()`**.
This is the single most important constraint on the design: any scheme that
requires each sender to authenticate to an external provider forces a rewrite of
the component least suited to it.

**Nextcloud, Immich, Forgejo and Plane have no SMTP configuration at all** and
therefore silently send nothing. Wiring them to the endpoint in section 3 is
the natural moment to fix that.

Receiving is used only by humans: Bulwark reaches Stalwart over JMAP
(`tenants/bulwark/bulwark-deployment.yaml`) and `mail-provisioner` creates
mailboxes from the Keycloak database. **Nothing in the cluster consumes inbound
mail programmatically**, which is why dropping the mail server costs no
functionality beyond mailboxes themselves.

## 3. The relay seam

Every sender targets one stable endpoint. What sits behind it varies:

```
smtp-relay.mail.svc.cluster.local:25     ← all senders; plaintext, no auth
        │
        ├── mail server installed  →  ExternalName Service
        │                             → stalwart-mail.stalwart.svc.cluster.local
        │
        └── mail server absent     →  null-client MTA (Postfix, relayhost-only)
                                      → provider:587, STARTTLS + AUTH
```

The consumer contract is identical in both modes: **host
`smtp-relay.mail.svc.cluster.local`, port 25, no authentication, no TLS.** No
tenant manifest branches on whether the mail server is installed, and
`admin-tools/test-pr-locally.sh` keeps a single delivery path to exercise.

Why a relay pod rather than provider credentials templated into each consumer:

- **Hermes needs no code change.** See section 2.
- **One credential in one place** instead of the provider password copied into
  a Helm values file, an `AlertmanagerConfig` CR and a Python ConfigMap.
- **A queue.** Providers throttle and have outages; Postfix retries. Keycloak
  and Alertmanager do not — today a refused message is simply lost.
- **Central envelope rewriting**, which is what makes providers that restrict
  sender identity workable at all (section 6).

The cost is one small pod (no database, no PVC) and, in mail-server mode, a hop
that would be pure overhead — which is why that mode uses an `ExternalName`
Service and does not run the null client at all.

## 4. Mode A — self-hosted mail server

Unchanged from `doc/tenant-stalwart.md`. Stalwart is authoritative for the mail
domain, holds member mailboxes, signs outbound mail with its own DKIM key, and
pushes its own MX/SPF/DKIM/DMARC records via `push_dns.py`. The only difference
is that the `smtp-relay` `ExternalName` Service is deployed alongside it so
senders address the seam rather than Stalwart directly.

Terraform provides the supporting infrastructure: A records for `mail` and
`webmail`, the PTR for `mail<env_ext>.<domain>` that must match Stalwart's EHLO
identity, and inbound firewall rules for 25, 587 and 993
(`infrastructure/terraform/main.tf`). All of it is conditional on this mode.

## 5. Mode B — external provider

The null client authenticates to a provider on 587 and relays everything. The
Operator supplies, during the Setup Journey, a host, port, username, password
and sending domain; these land in a Cluster Secret (`smtp-relay-credentials`)
applied the same way as the Keycloak and Garage credentials in
`smallworlds-init.sh`.

Not installed in this mode: Stalwart, `mail-provisioner`, Bulwark, the
`mail`/`webmail` DNS records, the PTR, and the SMTP/IMAP firewall rules.

Bulwark is the one of those that need not be given up. It is a JMAP client, not
a server, and it already reaches Stalwart at its public hostname rather than a
cluster-internal address — so it can be pointed at a mail server elsewhere,
typically another Stalwart on a host with a real IP and PTR, federated to this
Keycloak. The requirements are narrow (JMAP rather than IMAP, and an auth scheme
shared with the cluster's identity), and the three values to patch are in
`doc/tenant-other.md`, "Bulwark against a mail server outside the cluster". Member identities in Keycloak become the members' own external
addresses, which is a simplification rather than a limitation — Stalwart already
authenticates on `preferred_username` rather than e-mail precisely to avoid
coupling identity to the mail domain.

There is one genuine loss: **no bounce visibility.** Mailbox-hosting providers
expose no bounce webhook, suppression list or delivery dashboard, so a rejected
password reset is invisible to the cluster. Accepted in `docs/adr/0049`; the
mitigation, if a community outgrows it, is a transactional ESP behind the same
seam.

## 6. Choosing a provider

For system and admin mail only — a handful of messages a day on average, but
**bursty**: a bulk-invite run sends one message per member within a minute or
two, and an incident night multiplies Alertmanager's `groupInterval: 5m` and
`sendResolved: true` across alertnames.

Two Swiss options, compared on the axes that actually differ. Figures verified
2026-08-15; re-check before relying on them.

| | Migadu | Infomaniak |
|---|---|---|
| Billing unit | Traffic — addresses and domains unlimited | Per mailbox |
| Outgoing / 24 h | 20 (Micro, $19/yr), 100 (Mini, $90/yr), 500 (Standard, $290/yr) | 200 (Mail Starter, 1 address), 500 (Mail Service, 5+) |
| Window | Daily | Sliding, computed in real time |
| Sender identity | *Wildcard Sender*: one credential sends as any address on the domain | Must match the authenticated mailbox or a pre-registered alias, else `Sender Mismatch` |
| Multiple domains | Included | Another line item each |

**Migadu Micro is not viable** at 20 messages/day — one bulk-invite run exceeds
it. The realistic Migadu tier here is Mini.

**Default recommendation: Infomaniak Mail Service Starter**, one `system@`
mailbox plus free aliases. 200 messages per rolling 24 h absorbs invite bursts
and alert storms, it is the cheapest single line item of anything considered,
the Operator gets a real mailbox that *receives* `postmaster@` and DMARC
reports rather than a forward-only arrangement, and the owned-datacentre and
24/7-support story matches what this project asks other people to trust.

**Prefer Migadu Mini when one account must cover several domains** — a
production cluster plus a `.dev` cluster plus other communities — or when
service sender addresses are expected to multiply. Its Wildcard Sender is the
better fit for GitOps: under Infomaniak, every new `From` address added to a
manifest needs an alias created in the provider's dashboard first, an
out-of-band step that will eventually be forgotten when an overlay bump
introduces a tenant that sends mail. Either register the aliases up front (the
Setup Journey should print the list) or have the null client rewrite all
envelope senders to the authenticated address, accepting the loss of per-service
`From` addresses.

Avoid Gmail and Microsoft consumer relays entirely: they rewrite `From` to the
authenticated account, which destroys the per-service sender addresses that
Keycloak and Alertmanager rely on.

One Deployment Mode note: **Hetzner Cloud blocks outbound port 25 by default**
on new accounts until unblocking is requested. Relaying to a provider on 587
sidesteps this, which makes Mode B the smoother default for a new Operator.

## 7. Domain and DNS

**Send from a subdomain, never the apex.** `notify.<domain>` (or
`notify.dev.<domain>`, extending the `ENV_EXT` scoping in
`doc/tenant-stalwart.md`). Three reasons:

1. Apex reputation stays with member mail; a provider incident or a spam
   complaint against transactional mail does not taint it.
2. **The two modes coexist.** External sending authenticates for
   `notify.<domain>` while a self-hosted mail server owns the apex MX and DKIM,
   so switching between them is not a cutover with a propagation hole in the
   middle.
3. Relaxed DMARC alignment — the default — accepts a `notify.<domain>` DKIM
   signature under an apex `<domain>` policy, so one apex DMARC record still
   covers everything.

**SPF, DKIM and DMARC are required in Mode B and nobody in the cluster writes
them.** In Mode A `push_dns.py` publishes them; with no mail server that code
path does not exist, so the records become one-time Operator setup from values
the provider generates. Skipping them does not fail loudly — it makes Keycloak's
password resets land in spam, which reaches the Operator as "SSO is broken".
The Setup Journey must print the exact records rather than assume them.

**Keep one receivable address even with no mailboxes.** Without any MX for the
domain: replies to `noreply@` vanish, **DMARC `rua=` reports have nowhere to
go** so deliverability is unobservable, and providers that require sender-address
verification cannot complete it. An alias-only plan forwarding `postmaster@`,
`abuse@`, `dmarc@` and `admin@` to the Operator's existing mailbox costs a few
euros a year and removes a whole class of silent failure. Set `Reply-To:` to
that forwarding alias on outbound mail so a member replying to an invitation
reaches a human.

## 8. The admin address

`ADMIN_EMAIL` is collected once by `smallworlds-init.sh` and published in the
`smallworlds-global-config` ConfigMap, where it currently does four incompatible
jobs:

| Use | Where | It is really a… |
|---|---|---|
| ACME registration and expiry notices | `terraform/main.tf` (`acme_email`), `smallworlds-init.sh` | destination |
| Alertmanager recipient | `apps/alertmanager-config.yaml` | destination |
| Hermes incident reports | `tenants/hermes/src/config.py` | destination |
| Nextcloud admin **username** | `tenants/nextcloud/nextcloud-secret-init-job.yaml` | identity |
| Immich admin account | `tenants/immich/admin-init-job.yaml` | identity |

**The destination uses must never resolve to a mailbox the cluster hosts.** An
Operator who sets `ADMIN_EMAIL=admin@<domain>` served by their own Stalwart has
built a loop with a fatal failure mode: the node dies, Alertmanager fires, and
the alert is delivered to a mailbox on the node that just died. At bootstrap it
is worse — ACME registration and the first alerts happen before the mail server
exists at all. Removing the mail server makes this *more* likely to be gotten
wrong, because `admin@<their-domain>` is the obvious thing to type.

Therefore:

1. **`ADMIN_EMAIL` is an off-cluster destination** — the Operator's existing
   personal or work mailbox, hosted by someone the cluster does not run. This
   holds in both modes. `smallworlds-init.sh` rejects (or loudly warns about) a
   value under the cluster's own mail domain, in the same spirit as the
   `push_dns.py` scope guards.
2. **`ADMIN_LOGIN` is separate** (default `admin@<domain>`) and supplies the
   Nextcloud username and the Immich admin account. Conflating the two is why
   `nextcloud-username` is currently somebody's personal address. Both
   applications send password resets there, so it must be an alias that
   forwards — not a black hole.
3. **The pretty address is an alias, not a mailbox.** `admin@<domain>` forwards
   to `ADMIN_EMAIL`. Branded role addresses on the community domain, with a
   delivery endpoint that survives the cluster being on fire.

## 9. Remaining work

- The notifier tenant: a ~40-line stdlib translator from Alertmanager's webhook
  envelope to ntfy, in the pattern of `remediation`/`hermes`, plus replacing
  Hermes' `mailer.py` with the equivalent `urllib` POST. Alertmanager has native
  receivers for Telegram, Pushover and Discord if an adapter is not wanted;
  ntfy, Gotify and Matrix all need one. **The notification endpoint must run off
  the cluster it watches.**
- Route `Watchdog` to an external heartbeat that alerts on silence. It is
  currently sent to `blackhole` in `apps/alertmanager-config.yaml`, which means
  **nothing detects total cluster loss today** — Alertmanager cannot report its
  own death. This is independent of the mail decision.
- No test covers the onboarding link path. `bulk-invite.py`'s SPI call is
  exercised only against a stub; a live cluster has never run it, and neither
  path has an `e2e-tests/` assertion.
- `tenants/mail-relay/` — the null-client MTA and the `smtp-relay` Service,
  plus the `ExternalName` variant shipped from the Stalwart tenant. Sync wave
  `-5` or `0`; nothing needs it at sync time, only at runtime.
- Repoint the three senders in section 2 at the seam; add SMTP configuration to
  Nextcloud, Immich, Forgejo and Plane while doing so.
- `smallworlds-init.sh` — prompt for the mail mode and, in Mode B, the provider
  host/port/username/password and sending domain; emit
  `smtp-relay-credentials`; add the `ADMIN_EMAIL` guard and the `ADMIN_LOGIN`
  split; print the required DNS records and provider aliases.
- ~~`admin-tools/prepare-community-repo.sh` — move `stalwart` out of the
  always-installed `APPS` list into `OPTIONAL_APPS`~~ **done**; it also left the
  base's master kustomization, and the Operator Console's catalog reclassified it
  from a platform service to a community application. Bulwark is *warned* about
  rather than gated: it can run against an external JMAP server, so refusing the
  selection would forbid a legitimate deployment (`doc/tenant-other.md`).
- `infrastructure/terraform/main.tf` — make the `mail`/`webmail` A records, the
  PTR and the 25/587/993 firewall rules conditional on Mode A.
- `e2e-tests/tests/03-bulwark.spec.ts` must skip cleanly in Mode B. Add a relay
  smoke test (a Keycloak password-reset request accepted by the relay), because
  mail otherwise becomes an untested silent-failure path.
