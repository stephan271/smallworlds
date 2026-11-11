# Update Strategy — Three Release Rings — Design

**Status:** proposed; nothing below is implemented yet unless marked *exists*
**Date:** 2026-11-09
**Decision record:** `docs/adr/0060-updates-move-through-three-release-rings.md`

## 1. Why

Every application SmallWorlds supports ships security fixes and features on its
own schedule. Today, keeping up with them is entirely manual. The Renovate config
for the base repository exists, but the app is not installed and nothing would test
what it proposes. Releases are cut by hand, and their notes are written by hand
when they are written at all. An Operator who wants to follow the project has
little to go on beyond the tag name.

Two goals pull in opposite directions:

- **The project** wants every supported application kept current, automatically,
  with broken updates caught and reverted without a human in the loop.
- **An Operator** wants updates to be easy and safe. That means deciding as little
  as possible: *which applications run* and *when to follow the project*. Nothing
  should change on their cluster that they did not adopt on purpose.

The two-repo model already separates these concerns: the base repository moves,
and a GitOps Overlay pins an immutable tag. What is missing is the stage in
between, where an update proves itself before anybody's members depend on it,
and the information an Operator needs to decide when to follow.

## 2. The three rings

```
upstream app releases
        │  Renovate (base repo), one PR per application
        ▼
   main branch ──────────────► Ring 0: Canary Cluster
        │                       example data, follows main automatically,
        │                       verifies every change, reverts what breaks
        │  tag only commits that stayed green on Ring 0
        ▼
   release tag vX.Y.Z ───────► Ring 1: smallworlds.network
        │                       real members; adopts every release on day 0,
        │                       by a deliberate merge
        │  Follow Policy delay (default 14 days)
        ▼
                              Ring 2: every other community
                               adopts by a deliberate merge, when its
                               Follow Policy proposes it, or never
```

| Ring | Instance | Data | Follows | Who adopts | Catches |
|---|---|---|---|---|---|
| 0 | Canary Cluster, Contabo Cloud VPS 8 | Example data, kept across upgrades | `main`, automatically, within minutes | Nobody. It is automatic | Broken renders, failed syncs, failed migrations, failed e2e, before any tag |
| 1 | smallworlds.network | Real members | Every release, on the day it is tagged | Stephan, by merging the overlay PR | What example data cannot show: real usage, real phone apps, real load |
| 2 | Other communities | Real members | Releases older than their Follow Policy's delay | Their Operator, by merging the overlay PR | Ideally nothing |

**Rings 1 and 2 never adopt automatically.** Adoption is always a merge in the
GitOps Overlay, which is the audit trail and the rollback point. Ring 0 is the
only place where anything happens without a human.

## 3. Ring 0: the Canary Cluster

### 3.1 Machine and placement

- **Server:** one Contabo **Cloud VPS 8** (8 vCPU, 24 GB RAM), with the NVMe
  storage option. This is the same machine type the Contabo install guide
  (`doc/install-contabo-datalix.md`) uses, and the type Ring 1 is expected to
  move to. Ring 0 then fails the way Ring 1 and a typical Ring 2 server would:
  same kernel line, same firewall, same disk.
- **Install route:** the Setup Wizard, target `local` with `HOST_FIREWALL`
  (`docs/adr/0055`). It is a normal install, so it also re-tests the install
  path. Ring 0 is not a special build.
- **Name:** environment extension `.canary` on `smallworlds.network`: apps at
  `photos.canary.smallworlds.network`, `files.canary.…` and so on. DNS goes
  through the existing Hetzner zone (`LOCAL_DNS=hetzner`). This is the mechanism
  `.dev` and `.lab` already use.
- **Object storage:** the **same external S3 provider as Ring 1**
  (`docs/adr/0053`). The default bucket prefix is derived from domain plus
  extension (`plan.default_bucket_prefix`), so Ring 0 gets
  `sw-smallworlds-network-canary-…`, distinct from Ring 1's
  `sw-smallworlds-network-…`. **The access key must be a separate one, limited
  to the canary prefix** where the provider allows it. A key that can also reach
  Ring 1's buckets turns a canary bug into a production incident. If the
  provider cannot scope keys, use a second account.

  **Accepted for now (2026-11-10):** the provider could not scope the key, and
  Ring 0 runs with an account-wide key. Ring 0 only names its own buckets, so
  the risk is a bug that addresses another prefix, or a compromise of Ring 0.
  Until a second account exists, turn on versioning or object lock for Ring 1's
  buckets where the provider offers it, and never point a Ring 0 restore drill
  (§9) at Ring 1's prefix.
- **GitOps Overlay:** Ring 0 has one, like every install
  (`my-community.canary-config`), with every reference at `?ref=HEAD`; it is
  written once and never bumped. Argo CD's root Application needs it, and it
  carries what the base cannot: the `.canary` hostname patches, the external
  S3 patches, the Gallery and Euro-Office choices, and the Ring 0-only Canary
  Follower.

**Why always on, and not an hourly Hetzner server.** A Hetzner server billed by
the hour, running only while a release is being tested, was considered on
2026-11-10 and rejected:

- **The saving is small.** Every app at once needs 24 GB, so the Hetzner size
  would be a CX53 (32 GB; the 16 GB CX43 is too small): about €29.49/month cap,
  about €0.047/hour after the 2026 price rises. A powered-off server is still
  billed in full. Billing stops only when the server is deleted, and the volumes
  and Primary IP keep costing about €3/month. Break-even with the Contabo server
  (about €14/month) is about 230 hours a month. One test day a week (about 100
  hours) costs about €8/month, a saving of about €70 a year. Prices are from
  third-party trackers; check them before relying on them.
- **The failures that matter most need time.** WAL growth that fills a disk
  (the 2026-08-09 outage), certificate renewal, backup retention, nightly
  CronJobs, memory leaks and log growth show up after days, not in a test
  session. The 72-hour gate in §4.4 would shrink to "one session passed".
- **It would test the wrong install path.** Hetzner means the `hetzner` target
  (Terraform, cloud-init, network volumes). Ring 1 and most Ring 2 servers use
  `local` with `HOST_FIREWALL`.
- **Parking and reviving is not supported today.** etcd lives on the server's
  own disk (`/var/lib/smallworlds-etcd`), so deleting the server deletes the
  cluster datastore. A rebuilt node starts empty, and every generated Secret is
  generated again. Reviving a parked cluster would need:
  - an etcd snapshot (`k3s etcd-snapshot save`, to the volume and to S3)
    before deleting the server;
  - a cloud-init branch that restores it with `k3s server --cluster-reset
    --cluster-reset-restore-path=…`;
  - skipping the template's first step, which deletes
    `/var/lib/rancher/k3s/server` (on the volume, including the token the
    restore needs). That change has to be mirrored in the local bootstrap.

  That is about two to three days of work in the most sensitive part of the
  bootstrap.

Revisit this if the canary turns out to be needed only rarely, or if the
Contabo server proves unreliable.

### 3.2 What it runs

**Every supported application, at the same time**: Nextcloud, Forgejo,
Jitsi, Stalwart, Bulwark, Excalidraw, pod-gateway, plus the always-installed
Platform Services.

Two choices are exclusive by construction, so one side of each runs on Ring 0:

| Choice | Ring 0 runs | Reason | The other side is covered by |
|---|---|---|---|
| Photos | **Gallery** | Ring 1 runs Gallery, and Gallery is `tenants/immich` plus patches, so it exercises the Immich manifests too (`docs/adr/0054`) | A fresh-install run of `test-pr-locally.sh` whenever `tenants/immich` changes |
| Office | **Euro-Office** | Ring 1 runs Euro-Office | A fresh-install run whenever `tenants/collabora` changes |

The release notes say which variant was soaked on Ring 0. "Collabora: fresh
install tested, not soaked" is honest and is enough for an Operator to decide.

**Sizing estimate.** On Ring 1 (OVH, same CPU and RAM class), Nextcloud,
Gallery, Jitsi, Keycloak and monitoring use about 11 GB of 23 GB, measured on
2026-11-09. Forgejo, Stalwart, Bulwark, Excalidraw and pod-gateway add
roughly 2–4 GB (Plane, the largest and least certain part of the first
estimate, was removed on 2026-11-11, `docs/adr/0061`). The steady state should be about 15–17 GB, with Gallery's machine
learning and Nextcloud's background jobs spiking above that. That fits in 24 GB
but should be measured in the first week. If it is tight, the fix is limits on
the spiking workloads, not a bigger machine, because customers run this same
machine size.

**Mail:** Stalwart needs inbound and outbound port 25 and a PTR record. Check
when ordering that Contabo leaves port 25 open for the server, or what it takes
to have it opened.

### 3.3 Example data

Ring 0 is only worth its cost if it **upgrades populated applications**. A fresh
install, which `test-pr-locally.sh` already covers, cannot show a migration that
breaks existing data.

- A seeder, `admin-tools/canary/seed.py` (new), creates a fixed cast of three
  example members and gives each application that keeps data some content
  (decided 2026-11-11):
  - **anna** owns most of it: photos, albums, the repository, files she shares.
  - **ben** is on the receiving end: shares, a shared album, issue comments,
    mail from anna. Links *between* members are what migrations break.
  - **zoë** ("Zoë Müller-Lüthi") has non-ASCII names, also in file names, and
    signs in to only some applications, so some accounts appear late.

  They sign in like members, with passkeys only: the seeder and the data checks
  drive Playwright with virtual authenticators whose private keys are kept, so
  every check also exercises Keycloak's login theme and token handler.

  | Application | Seeded content |
  | --- | --- |
  | Gallery/Immich | photos with faces, a named face, an album shared with ben |
  | Nextcloud | files (with umlauts), a share, a calendar and a contact |
  | Keycloak | the three members themselves, passkeys, recovery codes, groups |
  | Forgejo | a repository with commits, issues, labels and a pull request |
  | Stalwart | mailboxes, mail between the members, a folder |
  | pod-gateway | one export from Immich |

  Jitsi, Excalidraw, Collabora, Euro-Office, Bulwark and the dashboard keep no
  member data (Excalidraw has neither a volume nor a database), so a smoke test
  covers them. It builds on `admin-tools/lab/seed-immich-photos.py` and
  `admin-tools/restore-drill/app-data.py` (*exist*).

  **Status 2026-11-11:** members and Gallery are seeded on Ring 0 and pass
  `seed.py check`. The photos are 19 public-domain NASA pictures pinned by
  SHA-256 in `admin-tools/canary/photos.json` (three astronauts, so face
  recognition has people to find; the largest is 52 MB, so it goes up to S3 in
  parts). Nextcloud, Forgejo, Stalwart and pod-gateway are still to come. Two
  traps the browser half already handles: the passkey button's handler arrives
  with a module script after the page, so the first click can do nothing; and
  Keycloak refuses a passkey whose signature counter did not rise, so the
  counter starts from the clock rather than from the stored value.
- **The data is never re-seeded.** It ages with the cluster and passes through
  every migration, as a real community's data does. The seeder runs once, and
  again only to add content for an application or a feature that is new.
- Each application gets a **data check** that asserts the seeded content is
  still there and readable after an update: photo count, album membership, a
  face's name, a file's checksum, an issue's title. This is what catches
  "the app starts, but members' data is gone or unreadable".
- Example data only. **Never restore Ring 1's member data into Ring 0**: it
  would put real people's photos and mail on a test machine.

### 3.4 Following main

The Canary overlay references the base at `?ref=HEAD`. On `main`, the base's
Applications say `targetRevision: HEAD`, so every tenant comes straight from
`main`. This is the behaviour that made HEAD dangerous for real communities, and
it is exactly what Ring 0 wants.

Argo CD does not notice a new commit in a remote Kustomize base until it
hard-refreshes (*known*: a soft refresh is served from cache). Hooks also run
only on a full sync. So Ring 0 needs an active follower.

**Canary Follower** (new; stdlib Python in a ConfigMap, the Hermes/Remediation
pattern, deployed on Ring 0 only):

1. Every 5 minutes, read `main`'s head commit.
2. When it changes, hard-refresh every Application and trigger a full sync.
3. Wait for every Application to be `Synced` and `Healthy`, with a bound.
4. Run the verification (§3.5).
5. Report the outcome as a GitHub **commit status** `ring0/verified` on that
   commit: pending, then success or failure, with a summary.
6. On failure, revert (§3.6).

### 3.5 Verification

Run in order. Each step's result goes into the commit status summary.

1. **Converged.** All Applications `Synced`/`Healthy` within the bound.
2. **Smoke.** Every Ingress host answers `https://<host>/` below 500 with a
   valid certificate, asked from inside the cluster. The Follower does this
   itself (`tenants/canary-follower/src/checks.py`): the staging smoke-test Job
   probes one Service per tenant and would need a patch per app.
3. **E2E, full OIDC.** The Playwright suite with `FULL_OIDC=1`. Ring 0 has real
   Let's Encrypt certificates, so full login round-trips work, which ephemeral
   staging cannot do. It runs **in the cluster as a Job** (Playwright image), not
   from GitHub: Keycloak administration answers only on the tailnet, and the e2e
   setup needs it.
4. **Data checks** from §3.3.
5. **Quiet period.** No `critical` alert for 30 minutes after the sync.

A commit is **green on Ring 0** when all five pass. After that it stays green
unless a later failure on the same commit is recorded.

### 3.6 Automatic revert on Ring 0

When verification fails on commit `X`:

- If `X` came from Renovate, the Canary Follower opens a revert PR for `X` and
  merges it. `main` returns to the last green state, and Ring 0 follows.
- If `X` came from a person, it opens the revert PR, does **not** merge it, and
  alerts. A human change may be a deliberate fix-forward in progress.
- In both cases it sends an ntfy alert to the Operator channel.

**What a revert cannot undo.** If `X` migrated a database, the old image may not
read it, and Ring 0 stays red after the revert. Recovery then means restoring
that application's database on Ring 0 from the backup taken before `X`
(§5.2) — Ring 0 takes the same pre-update backups as everyone else. In the
first version this is a manual runbook. Automating it is a later step, once the
runbook has been run a few times.

**Confirmed on 2026-11-11 (§8):** reverting the Gallery v5.10.0 bump would have
left Ring 0's Gallery crash-looping, because v5.10.0's newer Immich base had run an
Immich migration that nothing undoes. So the revert, when it is built, **skips
every commit that touches a tenant whose database migrates**; for those the
Follower only alerts. That part exists already: a rejected commit raises
`CanaryCommitFailed` (warning, through ntfy), and both the alert and the GitHub
status say "restore, do not revert" when the commit touches such a tenant
(`tenants/canary-follower/README.md`).

The token for commit statuses and revert PRs is a fine-grained GitHub token
limited to the base repository (statuses, pull requests, contents). It lives
**only on Ring 0**, which holds no member data.

## 4. From main to a release

### 4.1 Renovate in the base repository

The base `renovate.json` (*exists*) groups every minor and patch update into one
weekly PR. Change it to:

- **One PR per application** (`groupName` per tenant). An application's images
  and chart move together; Gallery's server and ML images are one PR. A
  failure then reverts one application, not the whole week.
- **`minimumReleaseAge: "3 days"`** for upstream releases, so a release the
  upstream project withdraws within days never reaches `main`.
- Keep **no automerge into `main` for majors**. For minors and patches,
  automerge is allowed **once Ring 0 and the PR checks exist**: Ring 0 is then
  the reviewer, and §3.6 reverts what it rejects.
- **Before installing the app:** close the stale PRs #34 and #36 left by the old
  in-cluster Renovate. Expect a digest-pinning PR on the first run; it comes from
  `config:best-practices`.

### 4.2 PR checks (new GitHub workflow)

On every PR, cheap and fast, no cluster:

- `kubectl kustomize --enable-helm` renders every tenant, including `gallery`.
- Scripts pass `bash -n`; `renovate.json` parses.
- The existing script tests under `admin-tools/test-*.sh` that need no cloud.

A PR that fails here never reaches `main` or Ring 0.

### 4.3 Release kinds and version numbers

Today `bump-version.sh` (*exists*) only ever raises the patch number, so the
number carries no information. From now on the number does:

| Release | When | Example |
|---|---|---|
| **patch** `v1.Y.Z+1` | Only fixes and security fixes, no application feature changes, no migrations that change data | An image patch release, a manifest fix |
| **minor** `v1.Y+1.0` | Any application feature update, a database migration, a newly supported application | Gallery v5.8 → v5.10 |
| **major** `v2.0.0` | Something every Operator must act on: a manual step, a breaking rename, a node rebuild | A new K3s line that requires a rebuild |

Upstream projects bundle security fixes with features: Gallery v5.9.0 is both.
Such a release is a **minor** with a **Security** flag in its notes. The Follow
Policy delays the *proposal*, and an Operator who reads the flag can still adopt
early by hand.

### 4.4 The release gate

`bump-version.sh` gains a kind argument (`patch|minor|major`) and refuses to tag
a commit that is not green on Ring 0. It reads the commit status.

**Cadence:** weekly, or immediately for a security fix. A release candidate is the
newest green commit that **has stayed on Ring 0 for 72 hours** (one full backup
cycle, and every nightly job run at least twice), except for a security release,
where the 72 hours shrink to one green verification.

### 4.5 Release notes

Generated by `admin-tools/release-notes.py` (new), then edited by a human, and
published as the GitHub release. They are what the Operator sees: Renovate
copies a tag's release notes into the PR it opens in the GitOps Overlay. Every
release says:

1. **Kind and flags:** patch, minor or major; Security; Manual steps required.
2. **Per application,** each in its own section so an Operator can skip the apps
   they do not run:
   - old → new version, with a link to the upstream notes;
   - two or three lines of what changes for members;
   - **Database migration:** none / forward-only;
   - **Rollback:** revert the overlay commit / restore the pre-update backup;
   - opt-in features and how to enable them;
   - **Resources:** expected change in memory, especially machine learning.
3. **Platform:** changes to always-installed services, bootstrap pins (these
   reach new and rebuilt nodes only), and anything in the GitOps Overlay that
   `bump-overlay-release.sh` migrates.
4. **Evidence:**
   - date on Ring 0, hours green, e2e and data-check results;
   - which variants were soaked and which were only fresh-install tested;
   - after the Ring 1 adoption, appended later: "running on smallworlds.network
     since <date>, no incidents" or the incident.
5. **Version path:** releases that must not be skipped, if any, and the expected
   downtime.

The version table (item 2's old → new) is generated by diffing the rendered
images and chart versions of the two tags. The rest is a template that the
generator pre-fills from the upstream release notes and a human finishes.

## 5. Rings 1 and 2: adopting a release

### 5.1 Follow Policy

The Operator's second decision, beside which applications run. It is one question
in the Setup Wizard and in `prepare-community-repo.sh`, written into the GitOps
Overlay's `renovate.json`:

| Follow Policy | Renovate setting on the smallworlds dependency | Who it suits |
|---|---|---|
| **Immediately** | `minimumReleaseAge` none | Ring 1 |
| **After two weeks** (default) | `minimumReleaseAge: "14 days"` | Most communities |
| **Security fixes now, features after two weeks** | patch: none; minor: `"14 days"` (`matchUpdateTypes`) | Communities that want both |
| **Only when I ask** | `enabled: false`; the Operator runs `bump-overlay-release.sh` | Communities that change nothing without a reason |

Majors are never grouped with the others and always come as their own PR, so
they are never merged by accident with a routine update.

Every policy only **proposes**: Renovate opens a PR, and the Operator merges it.
The merge is what Argo CD deploys.

### 5.2 Before the update: a backup of every database that will migrate

New. Each tenant with a CNPG cluster gets an Argo CD `PreSync` hook that creates
an on-demand CNPG `Backup` and waits for it to complete. The update then always
has a fresh restore point, and "rollback = restore" loses only minutes, not up
to a day.

Two constraints:
- Argo CD runs hooks only on a **full** sync. The overlay bump has to trigger one
  (the Renovate PR's description says so, and `bump-overlay-release.sh` prints
  the command).
- The hook must not block an update when the backup target is down for an
  unrelated reason forever. It fails after a bound, and the sync stops with a
  clear message rather than updating without a backup.

### 5.3 After the update: detection and the rollback command

Rings 1 and 2 are **not** reverted automatically. A database that has migrated
forward cannot safely run the old image, and a wrong automatic rollback on a real
community loses members' data. Instead:

- An alert, through the existing ntfy and healthchecks.io path
  (`doc/alerting.md`), when an Application is not `Healthy` within 30 minutes
  after an overlay bump.
- The alert links the release's **rollback section**: for each application,
  either "revert the overlay commit", or "restore the pre-update backup with
  these commands". That is a per-tenant runbook in `doc/tenant-*.md`.

### 5.4 Overlay tooling

- `bump-overlay-release.sh` (*exists*) stays the one way to move an overlay by
  hand.
- `update-community-version.sh` (*exists*) duplicates it, skips its migrations
  and `overlay-config.yaml`, and pushes without review. **Retire it.**

### 5.5 Precondition: no live overlay below v1.2.44

Before Ring 0 makes `main` move faster, **every live GitOps Overlay must be on
v1.2.44 or later**. An overlay on an older tag (or on `?ref=HEAD`) takes its
tenants straight from `main`, so it would become a Ring 0 instance with real
members. Today `my-community-config` (v1.2.39), `my-community-dev-config`
(v1.2.20), `my-community-jitsi-only-config` (v1.2.43) and
`my-community-lab-config` (v1.2.42) are below. For each one: move it to a
current tag or confirm that its cluster no longer exists.

**Done (2026-11-10):** none of the four clusters exists any more, and the
overlays are being deleted. The live overlays are Ring 1's
`my-ovh-gallery-nextcloud` (v1.2.45) and Ring 0's `my-canary-installation`
(`?ref=HEAD`, on purpose).

## 6. What exists and what is new

| Piece | State |
|---|---|
| Two-repo model, immutable tags, `targetRevision` pinned at release | exists (`bump-version.sh`, `route_revision.py`) |
| Base `renovate.json` | exists; change to one PR per app, `minimumReleaseAge`, automerge behind Ring 0 |
| Overlay `renovate.json` | exists; add the Follow Policy |
| `bump-overlay-release.sh` | exists |
| `test-pr-locally.sh`, Playwright e2e, smoke-test job | exist; e2e gains a data-check layer and an in-cluster runner |
| Seeders | partly (`seed-immich-photos.py`, `app-data.py`); new `admin-tools/canary/seed.py` covers every app |
| Alerting (ntfy, healthchecks.io) | exists |
| Remediation / Hermes | exist, `DRY_RUN`; not needed for this design |
| Ring 0 server, overlay, install | new |
| Canary Follower (refresh, verify, commit status, revert) | built 2026-11-10 (`tenants/canary-follower`): follow, converge, smoke, quiet period, status. Not yet: e2e Job, data checks, revert |
| PR-check workflow | new |
| Release kinds in `bump-version.sh`, the gate | built 2026-11-10 (`admin-tools/lib/ring0_gate.py`; override with a recorded reason) |
| `release-notes.py` | built 2026-11-10 (draft with `TODO:` lines; tests in `admin-tools/test-release-notes.py`) |
| Follow Policy question (wizard, `prepare-community-repo.sh`) | new |
| `PreSync` database backup per CNPG tenant | new |
| Post-update alert and per-tenant rollback runbooks | new |

## 7. Implementation order

Each phase is useful on its own.

1. **Ring 0 stands.** Order the VPS, create the scoped S3 key, install with
   the wizard (`.canary`, every app), overlay on `?ref=HEAD`. Seed the example
   data. Measure memory for a week. Satisfy §5.5. *Following is manual at this
   point: hard-refresh by hand.*
2. **Renovate on.** Change the base `renovate.json` (§4.1), add the PR checks
   (§4.2), close the stale PRs, install the Renovate GitHub App on the base
   repository.
3. **Ring 0 follows and verifies.** The Canary Follower with steps 1–5 of §3.4,
   commit status only, no revert yet. Then add the data checks.
4. **Releases carry information.** Release kinds and the gate in
   `bump-version.sh`; `release-notes.py`.
5. **Rings 1 and 2 adopt safely.** Follow Policy; `PreSync` backups;
   post-update alert; rollback runbooks per tenant; retire
   `update-community-version.sh`.
6. **Ring 0 reverts.** §3.6, after phase 3 has run long enough for its false
   alarms to be fixed. Then allow Renovate automerge for minor and patch updates.

## 8. Walkthrough: Gallery v5.8.0 → v5.10.0

The first update to go through the rings, run by hand as each phase lands, to
find what the design missed. Gallery has two pending releases:

- **v5.9.0** (2026-10-22): security patch (built on Immich v3.3.5 after a
  certificate-authority compromise, plus Node.js patches). It also **changes
  how Gallery records its database migrations** ("Back up your database
  first"), and it fixes reading multipart uploads back from Garage-like S3
  stores.
- **v5.10.0** (2026-11-04): features, all opt-in or UI-only — Spatial Scene (off
  by default; machine learning), Library Cleanup, redesigned app search,
  Space editors managing members, database backups to S3 (Gallery's own;
  CNPG already covers this here).

Steps, and the phase each one exercises:

| # | Step | Phase | What to learn |
|---|---|---|---|
| 1 | Renovate's PR (or a hand-made one in the same shape): both images `v5.8.0 → v5.10.0` in `tenants/gallery/kustomization.yaml` | 2 | Does Renovate group the two images; do the PR checks render `gallery` |
| 2 | Before merging: Ring 0 runs Gallery v5.8.0 with seeded photos, faces and albums | 1 | Does the seed cover what v5.9's migration change touches |
| 3 | Merge; Ring 0 follows; verification plus data checks | 3 | Does the migration-ledger change pass with existing data; memory with v5.10 |
| 4 | **Deliberately try the rollback on Ring 0:** revert to v5.8.0 and see whether it starts on the migrated database. Restore the pre-update backup if not | 6 | Write the Gallery row of the rollback section from fact, not from guessing |
| 5 | 72 hours green → `bump-version.sh minor` → v1.3.0; notes from `release-notes.py`, flagged **Security** | 4 | Is the release-notes template enough for an Operator to decide |
| 6 | Ring 1: the Renovate PR appears in `my-ovh-gallery-nextcloud` on day 0; on-demand backup of the `immich` database; merge; full sync | 5 | Does the `PreSync` backup run; does the post-update alert stay quiet |
| 7 | Append the Ring 1 evidence to the release notes | 4 | — |
| 8 | Ring 2 Operators on "after two weeks" get the PR on day 14 | 5 | — |

Ring 1 is a production cluster with real members: step 6's commands are run by
Stephan.

**Results so far (2026-11-11, steps 1–4).** Renovate is not installed yet, so
step 1 was a hand-made PR (#44); its checks rendered `gallery` and passed.

- **Step 2 (seed):** members and Gallery were seeded on v5.8.0 (§3.3), and an
  on-demand backup was taken (`before-gallery-5-10`).
- **Step 3 (update):** Ring 0 followed the merge without help. Under v5.10.0,
  `seed.py check` passed with a fresh passkey sign-in. The migration-ledger
  change ran on existing data (84 records in `gallery_migrations`), and the
  52 MB original read back from S3 intact.
- **Step 4 (rollback):** reverting worked for every Gallery migration. v5.8.0
  then still refused to start, because v5.10.0's newer Immich base had run an
  Immich migration that nothing undoes. Going forward again was clean, and
  `check` passed. The full Gallery rollback row is in `doc/tenant-immich.md`.

**What this changes in the design:**
- **§3.6:** the automatic revert cannot undo an update that moves Gallery to a
  new Immich base. Reverting the commit leaves Ring 0 crash-looping, which is
  worse than staying on the new release. The Follower had no revert yet. It
  now raises an alert for every rejected commit, and that alert says
  "restore, not revert" for tenants whose database migrates.
- **§4.5:** the release notes' rollback row has to say per release whether it
  is "revert" or "restore only". For Gallery, the Immich base named in its
  notes decides.
- **Still open:** step 4's last part, restoring `before-gallery-5-10` into a
  CNPG cluster that Argo CD manages on Ring 0, was not run. It is the real way
  back for a release that crosses a base, and it needs its own runbook before
  Ring 1 relies on it.

## 9. Moving Ring 1 to Contabo later

Not part of this design, but it is affected by it. Ring 1 keeps its S3 provider,
so its photos (Gallery stores them in S3), files and every backup stay where they
are. Moving means installing a Contabo node for `smallworlds.network` with the
same bucket prefix and restoring the databases from their barman backups in that
same storage. That is a real restore drill, and should be rehearsed on Ring 0
first: install a second Ring 0 node, restore Ring 0's databases into it, and run
the data checks.

## 10. Open questions

- Contabo and port 25 / PTR for Stalwart (§3.2).
- ~~Whether the S3 provider can scope an access key to a bucket prefix (§3.1).~~
  It cannot (2026-11-10); accepted for now, a second account later.
- The quiet-period and soak lengths (30 minutes, 72 hours) are first guesses.
  Revisit after the first month.
- ~~Plane's real memory footprint (§3.2).~~ Plane was removed (`docs/adr/0061`).
