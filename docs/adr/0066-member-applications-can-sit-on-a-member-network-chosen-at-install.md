---
status: proposed
---

# Member applications can sit on a Member Network of their own, chosen at install

The Operator decides at install whether member applications stay on the public
internet, as today, or move onto a **Member Network**: a second tailnet, with
its own Headscale and separate from the Operator's Private Network, that
members' devices join. With the Member Network on, only what guests need stays
public: share-link paths, Jitsi and the sign-in and join pages. Members reach the
full applications through the tailnet. **Off is the default**, and the answer can
be changed later by re-running the installer.

`doc/hardening.md` §1 and ADR 0038 kept member applications public for two
reasons: share links have to reach people who are not members, and onboarding
should not require installed software. Recordings of signed-out guests on Ring 0
(2026-11-14) showed that the first reason does not rule out a tailnet. Guests use
a small, known set of paths per application, and those can stay public while the
rest goes private. The second reason is a real cost, and only the Operator can
weigh it for their community: a household of non-technical members and a
group of technically confident friends weigh it differently. So it is a choice
and not a new default. With the Member Network off, ADR 0038 and hardening §1
hold unchanged. With it on, this ADR replaces them for member applications.

## The routing change, in both modes

The current separation cannot carry a second tailnet. Operator interfaces carry
an `ipAllowList` that admits the tailnet, private ranges and cluster addresses,
and tailnet traffic reaches Traefik through the node's tailnet address, so it
arrives *with a cluster source*. It is admitted because of that source, not
because it came from the tailnet (hardening §1). Members' devices would arrive the
same way, and the allow-list would admit them to Argo CD, Grafana and the
Keycloak admin console.

So separation moves from source addresses to **Traefik entrypoints**:

| Entrypoint | Reached from | Serves |
|---|---|---|
| `websecure` (public, `:443`) | the internet | Member mode off: everything public today. Member mode on: Guest Paths, Jitsi, the `smallworlds` realm, `join`, both Headscales' control endpoints |
| `members` | the Member Gateway only | the full member applications (Member Network on only) |
| `operators` | the Private Gateway only | Argo CD, Grafana, `identity/admin`, `identity/realms/master`, the Invitation Desk |

A **gateway** is a small tailscale pod joined to its tailnet. It has a tailnet
address of its own and forwards `:443` to its entrypoint, which the
LoadBalancer does not expose. Every router names its entrypoints, so an operator
router does not exist on `members` or `websecure` at all. The allow-lists stay as
a second layer but no longer carry the separation.

The `operators` entrypoint and the Private Gateway are introduced **in both
modes**. Doing it only when the Member Network is on would leave two models of
operator routing to maintain, and it also closes the cluster-source gap that
exists today. This part can land and be proven first, on its own. The node's own
tailscale client stays for SSH and `kubectl`.

### Inside the cluster

In-cluster callers resolve the application hostnames through the
`coredns-custom` override, which both bootstrap paths write (the cloud-init
template and `bootstrap-local-node.sh`). It answers with the node address, which
lands on `websecure`. With the Member Network on, that entrypoint serves only
Guest Paths, so everything inside the cluster that calls a member application by
its hostname would get the guest's view:

- **Applications calling each other.** Euro-Office fetches and saves documents
  through Nextcloud's own endpoints, which are not Guest Paths. Editing a
  document would fail for members, not only on Ring 0.
- **Ring 0's checks Job.** It runs the full-sign-in e2e suite and
  `seed.py check` from inside the cluster, so it would reject every commit.

So with the Member Network on, the override answers the member application
hostnames with a cluster-only Service in front of the `members` entrypoint.
In-cluster callers then see the full applications, the way members do. Identity,
`join` and the Headscales still resolve to `websecure`. The override is generated
at boot, so a change to it needs mirroring in both bootstrap paths. An install
that switches the Member Network on later needs the ConfigMap rewritten on the
node, because cloud-init does not run again.

## The Member Network

- **A second Headscale instance, not one Headscale with ACLs.** In Headscale, a
  missing or broken policy means everyone can reach everything, so one mistake on a
  shared tailnet puts members' unmanaged phones beside the Operator's laptops.
  The trust levels also differ: many self-enrolled devices on one side, one
  person's devices on the other. Separate instances cannot make that mistake.
- **Different address ranges.** The Private Network moves to `100.64.0.0/12` and
  the Member Network takes `100.96.0.0/12`, so logs and policies can tell the two
  apart.
- **Members enroll by signing in.** The member Headscale uses OIDC against the
  `smallworlds` realm, so adding a device means signing in with the passkey the
  member already has. Onboarding becomes: Member Invitation → join page → passkey →
  install the Tailscale app → sign in. The join page and the realm therefore stay
  public: a new member is not on the tailnet yet.
- **Leaving removes the devices.** Today's Headscale never expires a node
  (`node.expiry: 0`), which is acceptable for the Operator's own devices and not
  for members. Removing a member in Keycloak must also remove their devices.
  Whether that is a node expiry, a reconciling job or a step in the Invitation
  Desk is left to the implementation, but the member Headscale must not ship
  without one.
- **Same hostnames, two answers.** Public DNS keeps sending `nextcloud.<domain>`
  to the public address, where only the Guest Paths answer. The member
  Headscale's DNS sends the same name to the Member Gateway, where the full
  application answers. The same Let's Encrypt certificate covers both, and share
  links keep the URLs the applications already generate. This works with managed
  and with manual DNS (ADR 0056) alike, because the private answer comes from
  Headscale and not from the operator's DNS provider.

## Guest Paths per application

From the 2026-11-14 recordings on Ring 0 (Gallery 5.10, Nextcloud 35.0.2,
Euro-Office 9.3.4):

- **Nextcloud.** Public: `/s/<token>`, `/public.php/dav/files/<token>/…`,
  `/apps/files_sharing/publicpreview/<token>`, `/apps/text/public/session/…`,
  `/apps/eurooffice/s/<token>`,
  `/ocs/v2.php/apps/eurooffice/api/v1/config/<id>?shareToken=`, `/avatar/guest/…`,
  and the static assets under `/core`, `/dist`, `/apps`, `/custom_apps`.
  `office.<domain>` stays wholly public, because the guest's browser loads the
  editor from it.
- **Gallery.** Share-link paths **and** a `?key=` parameter, never the key alone.
  A share key overrides member credentials, but public endpoints such as
  `/api/auth/login` ignore it, so matching on the key alone would expose sign-in.
- **Jitsi stays wholly public.** Guests need the web app, the XMPP websocket and
  UDP 10000. This is acceptable because rooms already require a member to open
  them (`AUTH_TYPE: jwt`, `muc_wait_for_host`), so the public surface is joining
  as a guest.
- **Mail.** Only port 25 stays public. Submission, IMAP and the webmail move to
  the Member Network.
- **Excalidraw, Forgejo, pod-gateway.** These are members-only unless decided
  otherwise. Excalidraw's collaboration links would need the room websocket
  public. Members' home devices for pod-gateway (ADR 0047) join the Member
  Network like any other device.

Guest Paths are versioned knowledge about each application. An update can add
a path, and the new path then fails only for guests. Each list therefore needs a
signed-out end-to-end test that Ring 0 runs on every commit, so the update queue
stops on a broken share link the same way it stops on a broken login.

## Prerequisites

- **Nextcloud's background jobs must run from a cron job.** They run in AJAX mode
  today (no cron in the values, `backgroundjobs_mode` unset), so they run only
  while someone has a page open. A Nextcloud reachable only by members would run
  them even less, and nothing would warn about it. This change belongs on
  `main` regardless of the Member Network.

## How the choice is made

- **One answer, `MEMBER_NETWORK` (`off`/`on`).** It is asked by
  `prepare-community-repo.sh`, cached like `OBJECT_STORAGE`, and offered by the
  Setup Wizard in the same panel as the application selection. Following ADR
  0051, the wizard only passes the answer through.
- **The answer becomes overlay patches.** These are the routers' entrypoints, the
  member Headscale, the Member Gateway and the Headscale DNS records, generated
  in the way `generate_domain_patches.py` and `generate_storage_patches.py` are.
  A tenant does not get a second copy of its manifests.
- **It is offered only for an install reachable from the internet** (`hetzner`,
  or `local` with internet exposure, ADR 0055). On a LAN-only cluster the member
  applications are not public in the first place.

## Changing the answer later

Unlike the object-storage choice, this one is reversible without moving data,
but it is not instant for members:

- **Off → on.** Members have to enroll a device before their applications stop
  answering in public. The installer warns about this, and the Operator should
  give members time to enroll before applying it. Share links keep working
  throughout, because their URLs do not change.
- **On → off.** The member applications become public again at once. The member
  Headscale and its devices are removed, and members can uninstall the app.

## Considered and rejected

- **The Member Network always on.** It would make installing software a condition
  of membership in every community, including the ones for which that is the
  wrong trade, and it breaks for members whose work laptops allow no second VPN.
- **One Headscale with ACLs.** This fails open, as described above. It would give
  the Operator access to both networks from one client without switching, which is
  the only thing it does better.
- **Separation by source address**, made reliable by having Traefik see the real
  source (host networking, PROXY protocol). The separation would still rest on an
  allow-list that already failed once without anyone noticing (2026-10-11).
  Entrypoints fail closed: a router that is not attached to an entrypoint cannot
  be reached through it.

## Costs

- **Members install software.** That means a Tailscale app with a custom login
  server, which is available on Android, iOS, macOS, Windows and Linux. Gallery's
  background photo upload on phones runs only while the VPN is connected, so it
  depends on the platform's on-demand rules.
- **The Operator switches tailnets.** A Tailscale client has one active account,
  so when the Operator is also a member, their devices switch between the
  Private Network and the Member Network.
- **Two exposure modes to verify.** Ring 0 runs one configuration at a time. It
  runs with the Member Network on, the mode with more moving parts and the only
  one with Guest Paths to test. The off mode is then covered by the routing being
  the same apart from the `members` entrypoint.
- **Ring 0 needs a second check run, from outside.** Because of the DNS
  override, the in-cluster checks Job sees the full applications. It proves that
  they work, not what a guest can reach. The signed-out guest suite therefore has
  to run from outside the cluster and the tailnets, through the public address.
  It has to cover both directions: every Guest Path answers, and a sign-in page
  or member API behind it does not. Running it from inside the cluster would test
  the members' view again. Its result becomes part of `ring0/verified` like the
  in-cluster checks, so a share link broken by an update stops the queue.

## Verification

None of this is built. The Guest Path lists come from signed-out browser
recordings on Ring 0 and are the only part observed. Not yet proven:

- Headscale's `extra_records` answering for the community's own domain while
  `override_local_dns` is `false` on the clients. If that fails, the Member Network
  needs split DNS for `<domain>` or `override_local_dns: true`.
- Upload speed when a member's device cannot connect to the node directly and
  falls back to a DERP relay.
