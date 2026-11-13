# Updates per Application — Ring 0 Steward and Update Desk — Design

**Status:** proposed; nothing below is implemented unless marked *exists*
**Date:** 2026-11-13
**Decision records:** `docs/adr/0063` (Hermes and Remediation are removed),
`docs/adr/0064` (a stuck Ring 0 is handed to an agent that works through pull
requests), `docs/adr/0065` (communities adopt Ring 0's verified state per
application)
**Builds on:** `plans-and-walkthroughs/design-update-strategy.md` and
`docs/adr/0060`. Where the two disagree, this document wins; §10 lists what it
replaces.

## 1. Why

The three Release Rings (`docs/adr/0060`) are mostly built. Renovate proposes
updates, the Canary Follower merges them one at a time, verifies each one on the
Canary Cluster and reverts what breaks. Two things are missing from what the
project wants:

1. **When Ring 0 gets stuck, a person has to notice and act.** The follower
   raises an alert (`CanaryCommitFailed`) and stops its merge queue. Nothing
   tries to unblock it. The goal: Ring 0 keeps itself current with the newest
   stable versions, and when the automatic path stops, a capable AI agent takes
   over before a person is needed.
2. **An Operator can only adopt everything at once.** Ring 1 and Ring 2 move from
   one release tag to the next, all applications together, through a pull request
   in their GitOps Overlay. The goal: an intuitive page that shows what Ring 0
   runs next to what this community runs, and lets the Operator update **one
   application at a time**, whenever they choose. Every future Operator uses this
   page to keep their own community current.

At the same time, the earlier AI agents (Hermes and Remediation) are removed, so
the agent-based update steering starts from a clean slate.

## 2. Words used in this document

These are explained once here and used throughout. Terms in **bold capitals**
are project terms from `CONTEXT.md`.

### Git and GitHub

**Repository (repo).** A folder of files together with its complete history.
SmallWorlds has one public *base repository* (`stephan271/smallworlds`, this
one) and, per community, one private **GitOps Overlay** repository.

**Commit.** One saved change to a repository: which files changed, by whom, why.
Every commit is identified by its **commit hash**, a 40-character code such as
`05bdbcf2a1…`. The hash is computed from the content, so a given hash always
means exactly the same files. Nobody can change what it points to. The short
form (`05bdbcf`) is used in conversation; tools must use all 40 characters.

**Branch.** A movable name that points to the newest commit of a line of work.
`main` is the base repository's main branch. It moves forward every time
something is merged.

**`HEAD`.** "Whatever the branch points to right now". A reference to `HEAD`
changes meaning every time `main` moves, so it is the opposite of a pin.

**Tag.** A fixed name for one commit, such as `v1.3.0`. Unlike a branch it never
moves. A **release** is a tag (since `docs/adr/0057`).

**Ref.** The general word for anything that names a commit: a branch name, a
tag, `HEAD`, or a commit hash. Kustomize URLs write it as `?ref=…`.

**Pull request (PR).** A proposed change on its own branch, waiting to be
merged into `main`. Checks run on it, and people or bots can comment.

**Merge / squash-merge.** Taking a pull request into `main`. A squash-merge
turns all of the PR's commits into one commit on `main`, so one PR is one
commit and can be reverted as one.

**Label.** A tag-like marker on a PR or issue, such as `ring0-queue`. The
automation uses labels as instructions ("this PR may be merged by the queue").

**Commit status.** A small verdict that a service attaches to a commit on
GitHub. Ring 0 posts `ring0/verified` with the state `pending`, `success`,
`failure` or `error`.

**Issue.** A discussion thread in a repository, used here to record that Ring 0
is stuck and to hand the case to the Ring 0 Steward.

**GitHub Actions / workflow.** GitHub's built-in automation. A *workflow* is a
file under `.github/workflows/` that says "when X happens, run these steps" on a
temporary machine GitHub provides, or on a *self-hosted runner*: a small GitHub
program on a machine of your own that waits for such work. The Ring 0 Steward
runs as a workflow on a self-hosted runner on Ring 0's server (§5.3).

**Fine-grained token.** A GitHub password for a program, limited to named
repositories and named permissions (for example: "contents, read and write, on
this one overlay repository only"). It can be revoked at any time.

**Revert.** A new commit that undoes an earlier one. The history keeps both.

### Three different meanings of "pin"

"Pin" always means *fix something to one exact version, so it cannot change by
itself*. Three different things in this project are called pins. Keep them apart:

1. **Bootstrap Pins** (*exists*, `infrastructure/bootstrap-pins.env`,
   `docs/adr/0057`): the exact K3s and Argo CD versions a new node installs,
   each with a download address and a checksum.
2. **Renovate's "Pin dependencies" PRs** (*exists*): Renovate replaces a
   readable image version such as `redis:7.4` with the version *plus its
   digest*, `redis:7.4@sha256:3f9c…`. The digest is the checksum of the exact
   image, so a registry cannot silently swap what `7.4` means. Follow-up
   "digest" PRs move that checksum when the upstream image is rebuilt.
3. **Application Pins** (*new*, this design, §7): the line in a community's
   GitOps Overlay that says "run this application as it was in base commit
   `05bdbcf…`". Changing an Application Pin is how a community updates an
   application.

### Kubernetes and Argo CD

**Manifest.** A YAML file describing something the cluster should run: a
Deployment, a Service, a database cluster.

**Kustomize.** The tool that assembles manifests from several places and applies
patches to them. A **remote reference** such as
`https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/gallery?ref=v1.3.0`
tells Kustomize to fetch those manifests from the base repository at that ref.
This is how an overlay builds on the base.

**Argo CD.** The program inside each cluster that keeps the cluster equal to what
git says (GitOps). It reads the GitOps Overlay, renders it with Kustomize and
applies the result.

**Argo CD Application.** One unit Argo CD manages, defined in
`infrastructure/kubernetes/apps/<name>.yaml`. It says where the manifests come
from: `repoURL` (which repository), `path` (which folder) and `targetRevision`
(which ref).

**`targetRevision`.** The ref an Argo CD Application reads its folder from. On
`main`, 21 Applications say `targetRevision: HEAD`. That is safe only because a
release tag rewrites them to the tag (`admin-tools/bump-version.sh`). §7.3
explains why Application Pins have to do the same.

**Sync.** Argo CD applying what git says to the cluster. **Synced** means the
cluster matches git. **Healthy** means what runs actually works (pods ready,
database up).

**Hook / PreSync hook.** A job Argo CD runs at a fixed point of a sync. A PreSync
hook runs *before* anything changes. SmallWorlds uses one to back up a database
before an update (`bases/pre-update-backup`, *exists*).

**Hard refresh.** Telling Argo CD to forget its cached copy of the manifests and
fetch them again. This is needed when a ref like `HEAD` points somewhere new
without the overlay itself changing. A changed Application Pin changes the
overlay, so no hard refresh is needed.

### Databases and going back

**CNPG.** CloudNativePG, the operator that runs every application's PostgreSQL
database. A **CNPG Backup** is a copy of the database in object storage that a
new database can be built from.

**Database migration.** Many applications rewrite their database structure the
first time a new version starts. The old version often cannot read the result.
An application that does this is called **migrating** here (the follower's
`MIGRATING_TENANTS`: Immich/Gallery, Nextcloud, Forgejo, Stalwart, Keycloak).

**Revert vs restore.** Two ways back from a bad update:
- *Revert:* put the old version back. Works only when the database was not
  migrated.
- *Restore:* build the database again from the backup taken just before the
  update, then run the old version. Everything members did after the backup is
  lost. This is the only way back across a migration
  (`doc/storage-and-backup.md`, "Rolling an update back", *exists*).

### Updates

**Renovate.** A service (here the Renovate GitHub App) that watches every image
and chart version named in the base repository and opens a PR when a newer one
exists.

**Patch / minor / major.** The three parts of a version number `MAJOR.MINOR.PATCH`.
By convention, a *patch* only fixes things, a *minor* adds features without
breaking anything, and a *major* may break things and need manual steps.

**Merge queue.** The part of the Canary Follower that merges one `ring0-queue`
PR per verified commit (`tenants/canary-follower/src/mergequeue.py`, *exists*).

**Verified.** A `main` commit whose `ring0/verified` status is `success`: Ring 0
synced it and passed backup, convergence, smoke, e2e and data checks, and the
quiet period.

**Soak.** How long a verified state has run on Ring 0 (or Ring 1) without
problems. "Green for 31 hours" is a soak time.

**Platform Services and Community Applications** (`CONTEXT.md`). Platform
Services are what every cluster runs and members do not use directly:
cert-manager, CNPG, Traefik, Keycloak, `join`, monitoring, backups, Headscale,
the Member Dashboard, the Update Desk. Community Applications are what members
use and what an Operator chooses: Gallery/Immich, Nextcloud, Forgejo, Jitsi,
Excalidraw, Stalwart, Bulwark, Collabora, Euro-Office, the pod archive.

**Update Unit** (*new*, §7.2). What moves as one when an Operator updates: the
Platform, or one Community Application together with the applications it
cannot work without (for example Nextcloud with its office suite).

**Verified Pair** (*new*, §7.4). An Update Unit's content together with the
Platform's content that Ring 0 has actually run at the same time.

**Upgrade Stop** (*new*, §7.5). A version an application must not skip when
updating, for example each Nextcloud major.

### People and places

**Operator.** The person who runs a community's cluster (`CONTEXT.md`).

**Private Network / tailnet.** The Headscale VPN that Operator devices join.
Operator interfaces answer only from it (`doc/hardening.md`).

**ntfy.** The push-notification service that carries alerts to the Operator's
phone (`doc/alerting.md`, *exists*).

## 3. Overview

```
upstream releases
   │ Renovate: one PR per application, label ring0-queue (minor/patch/digest)
   ▼
main ──► Ring 0, Canary Cluster (exists)
   │      follower: merge one PR → back up → sync → verify → status
   │      ├─ green ─► publish Ring State (§6) ─► next PR
   │      └─ stuck (§5.1)
   │            ├─ restore by itself if the app migrates (§5.2)
   │            └─ issue "ring0-stuck" + evidence ──► Ring 0 Steward (§5.3)
   │                                                   GitHub Actions, Claude
   │                                                   → fix PR, ring0-queue
   │                                                   → or "needs a person" → ntfy
   ▼
ring-state branch: ring0.json (what Ring 0 verified, per Update Unit)
                   ring1.json (what smallworlds.network runs, per Update Unit)
   │ read hourly by every community's Update Desk
   ▼
Update Desk (Ring 1, Ring 2): "yours / Ring 1 / Ring 0" per application
   │ Operator presses Update on one application
   ▼
commit to the community's GitOps Overlay (Application Pins, §7)
   │ Argo CD sync → pre-update backup (exists) → new version
   ▼
Desk follows sync and health; offers Undo (revert) or the restore steps
```

Nothing AI-driven runs inside a community's cluster. The only agent works on
Ring 0's behalf, in GitHub, through pull requests.

## 4. Removing Hermes and Remediation

Both agents run on **every** community's cluster today (`apps/hermes.yaml`,
`apps/remediation.yaml` in the root kustomization), and both do nothing:

- **Hermes** (Tier 2) has been in `DRY_RUN` since 2026-07. It can only diagnose
  and email, and email depends on Stalwart, which is optional since
  `docs/adr/0049`. Ring 1 does not run it. It needs an Anthropic key on every
  cluster that arms it.
- **Remediation** (Tier 1) is in `DRY_RUN`, has one handler (`oomkill`),
  escalates to Hermes, and opens PRs against `stephan271/my-community-config`,
  an overlay that no longer exists.
- **`dashboard/status-data.yaml`** claims Hermes patches it. Hermes's RBAC
  never allowed that, and nothing reads it.

Remove in one change (`docs/adr/0063`):

| What | Where |
|---|---|
| Tenants | `infrastructure/kubernetes/tenants/hermes/`, `tenants/remediation/` |
| Applications | `apps/hermes.yaml`, `apps/remediation.yaml`, their lines in `infrastructure/kubernetes/kustomization.yaml` |
| Namespaces | `hermes`, `remediation` in `namespaces.yaml` |
| Dead status page | `tenants/dashboard/status-data.yaml` and its kustomization line |
| Renovate | the `hermes` and `remediation` groups in `renovate.json` (`test-renovate-groups.py` must still pass) |
| Docs | `doc/tenant-hermes.md`; the Hermes/Remediation paragraphs in `CLAUDE.md`, `doc/alerting.md`, `doc/mail.md`, `doc/tenant-dashboard.md`, `doc/argocd-apps.md`, `doc/storage-and-backup.md`, `smallworlds_architecture.md`, `Roadmap.md` steps 4–5 |
| Plans | `implementation_plan_hermes.md`, `walkthrough-auto-operate.md`: a header line "replaced by design-per-app-updates.md", content unchanged |
| Comments | `tenants/canary-follower` and `tenants/join` mention "the Hermes pattern"; say "the stdlib/ConfigMap pattern" |

A cluster loses both when it next adopts a Platform state without them: the root
app prunes their Applications. No overlay needs editing. The tag
`hermes-final` marks the last commit that has them, as `operator-console-final`
does for the console (`docs/adr/0062`).

## 5. Ring 0: from stuck to unstuck

### 5.1 What "stuck" means

The follower decides this itself, deterministically, from what it already
tracks. Ring 0 is **stuck** when one of these holds:

| # | Condition | Example |
|---|---|---|
| S1 | A commit got `failure` | e2e fails after a Nextcloud minor |
| S2 | An `error` used up its three retries | a chart repository that stays down |
| S3 | Queued PRs exist, but nothing was merged for 24 hours | the head never turns green |
| S4 | A queued PR has been unmergeable for 12 hours | a conflict, a failing PR check |
| S5 | A restore on Ring 0 (§5.2) did not end healthy | |

Being stuck stops the queue, as today. New: the follower opens (or updates) one
issue per incident, labelled `ring0-stuck`, and fills it with an **evidence
bundle**: the condition, the commit and PR, the diff, Argo CD's status and
events for the Applications that are not healthy, the last 200 log lines of
each failing pod, and the checks Job's summary and log tail.

The issue lives in the **private** Ring 0 overlay repository
(`my-canary-installation`), not in the public base repository: logs can contain
Ring 0's own credentials and addresses. The follower's token needs *Issues: read
and write* on that repository in addition to what it has (README, "Deploying
it").

### 5.2 Restoring on Ring 0 by itself

The most common dead end today is a failed update of a migrating application:
the follower says "restore, do not revert" and waits for a person. Ring 0 holds
no member data, so the follower may restore by itself. It follows the drilled
runbook (`doc/storage-and-backup.md`, "Rolling an update back"):

1. Commit the recovery patch to the Ring 0 overlay (Cluster
   `bootstrap.recovery` from `ring0-before-<sha12>`, no WAL archiver).
2. Open and merge the revert PR on the base repository. This order matters: the
   runbook does both in one commit, which two repositories cannot do.
3. Sync, wait for `recovery` in the Cluster's spec, delete the Cluster, wait for
   it to be healthy, restart the application's pods.
4. Remove the recovery patch, take a Backup, wait for `completed`.
5. Mark the update `ring0-rejected-<fingerprint>` (*exists*) and let the queue
   continue.

**Precondition:** the Gallery restore across an Immich base change
(design-update-strategy §8, step 4, "still open") is drilled by hand first. The
runbook has only been proven with Forgejo. Until then this step is off
(`AUTO_RESTORE: "false"`), and S1 for a migrating application goes straight to
the Steward with "restore needed" in the evidence.

### 5.3 The Ring 0 Steward

An AI agent that takes over a stuck Ring 0, run as a **GitHub Actions workflow
with Claude Code** (`anthropics/claude-code-action`), signed in with Stephan's
**Claude Pro subscription** (below). It may act on Ring 0 itself, and it tries at
least three times before it hands the case to Stephan. It is a new start: it
shares no code with Hermes.

**Why GitHub Actions and not a pod on Ring 0.** Almost every reason Ring 0
gets stuck is a change to the repository: a chart renamed a setting, an upstream
release is broken, a test assumed something the new version changed. The fix is
a commit. Claude Code already reads code, renders manifests, runs tests and
writes PRs, so there is no tool loop to build and maintain (Hermes's hand-written
loop only had read-only tools and email). The evidence it needs comes from the
follower, which is already inside the cluster. The credential lives in GitHub's
secret store only: no community cluster ever needs an AI provider or key.

**Where the workflow runs: a self-hosted runner on Ring 0's server.** The
Steward needs Ring 0's Kubernetes API. A runner GitHub provides would have to
reach it over the internet (port 6443 is still open on Ring 0 today, and should
not stay open) or join Ring 0's tailnet, whose Headscale runs *inside* Ring 0
and may be what broke. Instead, a GitHub Actions runner runs on the Contabo
server as a systemd service under its own unprivileged user `steward`, outside
k3s. It reaches the API on the server itself, needs no open port, and works
while Argo CD or Headscale are down. It is registered to the private Ring 0
overlay repository only, so no pull request from a stranger can ever run on it
(a self-hosted runner on a public repository could be given work by anyone).
The `steward` user cannot read `/etc/rancher/k3s/k3s.yaml`; its only cluster
credential is the kubeconfig of the `ring0-steward` ServiceAccount (§5.4). The
runner's own updates are part of keeping that server patched.

**Trigger.** In the private Ring 0 overlay repository,
`.github/workflows/steward.yml`: on an issue labelled `ring0-stuck`, and on a new
evidence comment from the follower. A `concurrency` group ensures one run at a
time.

**What it checks out.** The base repository at `main` (read and write through
PRs) and the Ring 0 overlay (read). It follows the base's `CLAUDE.md` and a
prompt file `steward/PROMPT.md` in the overlay repository.

**What it may do:**
- **Act on Ring 0** through the `ring0-steward` ServiceAccount (permissions in
  §5.4): read everything but Secrets (Applications, pods, logs, events, CNPG
  status, Jobs), and take the actions that leave nothing behind that git does
  not say: delete a pod so its controller recreates it, delete a failed Job,
  start a Job from a CronJob (an init job, the checks), ask Argo CD to refresh
  or sync an Application, take a CNPG Backup. A restore it does not run
  itself: it asks the follower for one (§5.2) by labelling the issue
  `ring0-restore`, so the drilled runbook stays the only restore path.
- **Fix forward:** a PR on the base repository, labelled `steward`, that changes
  manifests, configuration or (with review, §5.4) tests. Body: diagnosis,
  evidence quoted, what Ring 0 should show if the fix is right.
- **Hold a version back:** close the Renovate PR. Renovate then ignores that
  version and proposes the next upstream release. It writes a note on the PR
  and the issue with the upstream bug, if one exists.
- **Diagnose only:** a comment on the issue saying what it found and why it
  needs a person, for example "S2: Codeberg has been down for 6 hours, nothing
  to fix".

**What it never does:** merge anything, push to `main`, edit
`.github/workflows/`, act on any cluster but Ring 0, read a Secret or `exec`
into a pod, delete data (a PVC, a namespace, a CNPG Cluster or Backup, an S3
object), or act on the Ring 1 overlay or any community's overlay.

**Token.** A fine-grained token: base repository with *Contents*, *Pull
requests* and *Issues* (read and write), no *Workflows*; Ring 0 overlay with
*Contents* (read) and *Issues* (read and write). Without *Workflows* permission,
GitHub refuses any push that touches a workflow file.

**Model credential: Stephan's Claude Pro subscription.** The action accepts an
Anthropic API key (billed per token) or a subscription token. Stephan chose the
subscription: `claude setup-token` on his own machine prints a long-lived token,
stored as the secret `CLAUDE_CODE_OAUTH_TOKEN` in the Ring 0 overlay
repository's Actions secrets and nowhere else, and passed to the action as
`claude_code_oauth_token`. What follows from it:
- Steward runs share the plan's usage limits with Stephan's own Claude use. A run
  that stops because a limit was reached is not an attempt: the follower starts
  it again after the limit resets.
- The model is the strongest one the plan offers in Claude Code (Opus 5.5 if
  Pro includes it, otherwise Sonnet 5.5), named in the workflow so it is a
  deliberate choice.
- The token expires. The workflow's first step checks it and, when it is
  refused, the follower notifies Stephan at once instead of counting a failure.
- If the Steward ever has to run without Stephan, or the limits get in the way,
  the same workflow takes an API key with a monthly spend limit instead.

**Attempts.** One **attempt** is one Steward run that ends with something to
test, a cluster action, a PR, or both, followed by a full Ring 0 verification
of the result. While a run is active (issue labelled `steward-working`), the
follower starts no verification; when it ends, the follower verifies the head of
`main` from the start, so the verdict never mixes the Steward's actions with a
check that was already under way. A failed verification adds its evidence to
the issue as a new comment, which starts the next attempt with everything the
earlier ones tried.

**How it ends.** The follower closes the issue when the head is green and the
queue moves again. After **three** failed attempts on the same incident, the
follower labels the issue `ring0-needs-person` and sends one ntfy notice
("Ring 0 needs you: <issue link>"). It notifies earlier only when the next step
is one the Steward is not allowed to take: a restore while `AUTO_RESTORE` is
off, a change to a protected path that needs Stephan's review (§5.4), a Secret
that has to change, or a credential that was refused. Waiting three attempts
for those would only delay him. Budget: at most 6 runs per day and a turn limit
per run, both in the workflow.

### 5.4 Guardrails: Ring 0 stays the judge

The agent must not be able to make Ring 0 green by weakening what judges it.

- A `steward` PR goes through the same PR checks and the same Ring 0
  verification as any update. The Steward never merges.
- **Protected paths:** `e2e-tests/`, `admin-tools/canary/`,
  `infrastructure/kubernetes/tenants/canary-follower/`, `renovate.json`,
  `.github/`. The merge queue merges a `steward` PR that touches any of them
  only after an approving review from the repository owner. The rule lives in
  `mergequeue.py`, not in branch protection, so Renovate's own PRs to the same
  paths keep flowing.
- **Two arming stages, for PRs only.** First *propose only*: Steward PRs carry
  `steward` but not `ring0-queue`, and Stephan adds the label after reading
  them; until he does, the attempt is paused, not failed. Cluster actions are
  armed from the start, since they leave nothing behind (above). After about four
  weeks with no wrong proposal, the Steward may add `ring0-queue` itself (except
  for protected paths), and its three attempts then run without Stephan.
- Whatever it reads (logs, upstream release notes, PR comments) is data, not
  instructions. The prompt says so, and the token's and the ServiceAccount's
  limits make that hold even if the agent is misled.
- **Ring 0 is judged on what git says, not on what the Steward did.** Its
  cluster actions are limited to ones that leave no lasting difference from
  git: a deleted pod or Job is recreated from the manifests, and Argo CD's self
  heal reverts any change to an Application's spec. So a green verdict after a
  Steward action still means "this commit works as written". When an action
  alone made Ring 0 green (a restart, a re-run Job), the Steward says in the
  issue whether communities would hit the same problem, and if so it opens a
  PR that makes the fix part of the manifests (a hook, an init job, a probe).
  Ring 1 and Ring 2 get no Steward.
- **The `ring0-steward` ServiceAccount** has one ClusterRole: `get`, `list` and
  `watch` on every resource except `secrets`; `get` on `pods/log`; `delete` on
  `pods` and `jobs`; `create` on `jobs` and on CNPG `backups`; `patch` on Argo
  CD `applications` (refresh and sync). No `pods/exec`, no `secrets`, no
  `delete` on anything that holds data. RBAC enforces this, whatever the
  prompt says.
- **Why no Secret and no `exec`, even on a cluster without member data.**
  Ring 0's S3 key is account-wide at the shared provider, so it reaches Ring 1's
  backups too. A Secret read or an `exec` into a pod that carries the key would
  give a misled agent a way to delete them. This stays so until Ring 0 has a key
  scoped to its own buckets, and even then the Steward has no need to read one.

### 5.5 Major updates

"Newest stable" includes majors, which never enter the queue today. With the
Steward:

1. Renovate opens the major PR without `ring0-queue` (*exists*).
2. The follower labels it `steward-prepare` when the queue is idle. The Steward
   reads the upstream upgrade notes, adapts the manifests on the PR's branch,
   writes the Upgrade Stop (§7.5) if the upstream requires one, and summarises
   the manual steps in the PR body.
3. Stephan reads it and adds `ring0-queue`. Ring 0 verifies it as any update.

### 5.6 Throughput

One update per verification cycle (sync, e2e with full OIDC, 30-minute quiet
period) allows roughly 10–15 merges a day. Renovate's digest and pin PRs (§2)
use up most of that. Change `renovate.json` so digest and pin updates are
grouped into **one weekly PR** across the repository, while real version updates
stay one PR per application. A digest change is a rebuild of the same version
and rarely breaks anything alone. If one does, the whole weekly PR is rejected
and the Steward splits it.

## 6. Ring State: what the rings publish

Every community's Update Desk needs to know what Ring 0 has verified, without
access to Ring 0. The follower publishes it.

**Where.** Branch `ring-state` of the base repository, file `ring0.json`, read
through `https://raw.githubusercontent.com/stephan271/smallworlds/ring-state/ring0.json`.
It is public: no token needed. Every update is a commit, so the branch's history
is the record. Renovate and the PR checks ignore the branch.

**When.** When a verification starts, when it moves to its next step, after
every final verdict (`success` or `failure`) on `main`, and when the queue of
waiting updates changes. That is a handful of commits per update, so roughly 50
to 80 a day on `ring-state` while the queue is busy: a side branch nobody
merges, so the number costs nothing. Each write replaces the file through
GitHub's contents API, as one commit.

**What.** Built from git, not from the cluster, except the `now` section (below):

```json
{
  "schema": 1,
  "updated": "2026-11-13T10:42:00Z",
  "head": {"commit": "05bdbcf…(40)", "state": "success", "green_since": "2026-11-13T09:10:00Z"},
  "now": {
    "state": "updating",
    "commit": "7e21c40…(40)",
    "pull": {"number": 58, "title": "Update gallery", "units": ["photos"]},
    "step": "checks",
    "step_since": "2026-11-13T10:31:00Z",
    "started": "2026-11-13T10:12:00Z",
    "queued": [
      {"number": 57, "title": "Update nextcloud", "units": ["nextcloud"], "since": "2026-11-12T22:04:00Z"},
      {"number": 54, "title": "Update Docker image ghcr.io/bulwarkmail/webmail to v1.21.2",
       "units": ["mail"], "since": "2026-11-12T19:40:00Z"}
    ],
    "stuck": null
  },
  "verified": [
    {"commit": "05bdbcf…", "verified_at": "2026-11-13T09:10:00Z",
     "units": {"platform": "a1…", "photos": "9c…", "nextcloud": "47…", "…": "…"}}
  ],
  "contents": {
    "photos": {
      "9c…": {"first_verified": "2026-11-11T14:00:00Z",
              "versions": {"gallery-server": "v5.10.0", "gallery-ml": "v5.10.0", "postgres": "16.6"},
              "database": "migrates", "undo": "restore",
              "change": "https://github.com/stephan271/smallworlds/pull/44"}
    }
  },
  "upgrade_stops": {"nextcloud": ["32", "33"]}
}
```

- `units` holds one **content fingerprint** per Update Unit (§7.2): a hash of
  the git tree hashes of the unit's paths. Git gives every folder a tree hash
  that changes exactly when a file below it changes, so two commits with the
  same fingerprint run the same manifests for that unit.
- `verified` keeps the last 180 days. That is the Desk's whole knowledge of
  which combinations Ring 0 has run (§7.4).
- `versions` are read from the rendered manifests' images and chart versions,
  the way `admin-tools/release-notes.py` already does (*exists*).
- `undo` is `restore` when the unit is migrating and an application or
  PostgreSQL image changed, otherwise `revert`.

**`now`: what Ring 0 is doing at this moment.** The rest of the file says what
Ring 0 *has* verified. `now` says whether it is busy, so that an Operator can
tell "Ring 0 has nothing newer" apart from "Ring 0 is checking something newer
right now", and does not adopt a state that Ring 0 is about to replace.

| `state` | When | What the Desk shows |
|---|---|---|
| `up-to-date` | The head of `main` is `success` and no update is queued | "Ring 0 is up to date (verified 10:42)" |
| `updating` | A verification is running, or the head is green and a queued update is about to be merged | "Ring 0 is checking *Update gallery* (#58): checks, 11 min. 2 more waiting." |
| `stuck` | One of S1–S5 (§5.1) holds | "Ring 0 is stuck since 09:30 (rejected update, being looked at). Nothing new arrives until it is resolved." |

- `step` is the follower's current step: `backup`, `sync`, `converge`, `smoke`,
  `checks`, `quiet`, or `revert` / `restore` after a rejection. `verify()`
  already runs them in this order; it reports each one as it starts.
- `pull` is the queued PR whose squash commit is being verified, or `null` when
  the commit came from a person's push.
- `units` of a PR are the Update Units its changed files belong to (§7.2). The
  Desk uses them to mark a row "newer version being checked on Ring 0 now" and
  "newer version waiting on Ring 0".
- `queued` lists the open PRs labelled `ring0-queue`, oldest first. Majors and
  other PRs that are not queued are left out: they are not on their way.
- `stuck` is `{"condition": "S1", "since": "…", "summary": "…"}`. It names no
  issue link: the evidence issue lives in the private Ring 0 overlay repository
  (§5.1). Until stuck detection is built (phase S1 in §11), the follower sets it
  for a `failure` on the head and for an `error` that used up its retries.
- **Freshness.** `raw.githubusercontent.com` may serve a file up to five minutes
  old. The Desk shows `updated` next to the banner ("as of 10:42") and treats a
  `now` older than two follower intervals plus the longest step as "Ring 0 has
  not reported; its state is unknown", never as up to date.

**Back-fill.** The first time the follower writes `ring0.json`, it fills
`verified` from the `ring0/verified` statuses already on `main`'s history (180
days), computing each commit's fingerprints from git. Without it the Desk would
start with no Verified Pairs and offer nothing. The back-fill also has to reach
the `main` commit that Ring 1's current tag `v1.3.0` was cut from, or Ring 1's
own state is not a Verified Pair (§7.4).

**`ring1.json`.** The same shape, written by a workflow in the Ring 1 overlay
repository on every push to its `main`. It reads the overlay's Application Pins
and records what smallworlds.network runs and since when. The token lives in
that repository's Actions secrets and can push to `ring-state` only (a GitHub
ruleset keeps `main` merge-only). No credential for the base repository ever
sits in Ring 1's cluster. This gives every other Operator the strongest evidence
there is: "the project's own community has run this for 9 days".

## 7. Application Pins in the GitOps Overlay

### 7.1 Today (*exists*)

Ring 1's overlay (`my-ovh-gallery-nextcloud`) names `v1.3.0` once per remote
reference: in the root `kustomization.yaml` (the whole Platform), in each
Application manifest URL (`…/v1.3.0/infrastructure/kubernetes/apps/immich.yaml`)
and in each application folder (`immich/kustomization.yaml`:
`…tenants/gallery?ref=v1.3.0`). `bump-overlay-release.sh` rewrites all of them
together. The folder structure already allows one ref per application. Only the
tooling assumes a single one.

### 7.2 Update Units and the pin file

An **Update Unit** is what moves as one. The units are defined once, in the base
repository, in `infrastructure/kubernetes/update-units.yaml`:

| Unit | Contains | Why together |
|---|---|---|
| `platform` | everything not claimed below: root kustomization, `bases/`, every Platform Service | cluster-wide; Ring 0 cannot test half a platform |
| `photos` | `apps/immich.yaml`, `tenants/immich`, `tenants/gallery` | Gallery is Immich plus patches (`docs/adr/0054`) |
| `nextcloud` | Nextcloud, Collabora, Euro-Office | the office suites are Nextcloud's editors |
| `mail` | Stalwart, Bulwark | Bulwark is Stalwart's webmail |
| `forgejo`, `jitsi`, `excalidraw`, `pod-archive` | one each | independent |

A test (like `admin-tools/test-renovate-groups.py`) checks that every tenant and
every Application belongs to exactly one unit, and that Renovate's groups do not
cross unit boundaries.

The overlay gets one file the Desk and the scripts both read and write,
`smallworlds-pins.yaml`:

```yaml
# Which commit of the SmallWorlds base repository each part of this cluster
# runs. Written by the Update Desk. Every commit here has been verified on
# Ring 0. Do not edit the kustomization files' refs by hand: they are
# generated from this file.
platform: 05bdbcf2a1c4e…   # 40 characters
units:
  photos: 05bdbcf2a1c4e…
  nextcloud: a54cb3f90e1d7…
```

`pins.py apply` rewrites every ref in the overlay's kustomization files from
it. It is one stdlib module, shipped in the Desk's source and used by
`admin-tools/` too, so there is one code path. A test checks the two copies are
identical. `bump-overlay-release.sh` becomes "set every pin to the commit of
this tag" and keeps its migrations.

### 7.3 Never `HEAD`

On `main`, 21 Applications say `targetRevision: HEAD`. A release tag fixes that
inside the tagged commit; a commit of `main` does not. An overlay pinned to a
`main` commit would therefore still take those 21 tenants from whatever `main`
is now: the problem `v1.2.44` fixed. So `pins.py apply` also writes a
`targetRevision` patch for **every** Application whose `repoURL` is the base
repository: the Platform pin for Platform Services, the unit's pin for a
Community Application. Applications routed to the overlay keep the overlay's
`HEAD` (`route_revision.py`, *exists*). Their folder's remote reference carries
the pin.

A render test fails if any Application in a rendered overlay names the base
repository with a `targetRevision` that is not a 40-character commit hash.

Kustomize fetches a remote reference by commit hash with `git fetch`, and
GitHub serves any commit reachable from a branch. This has to be confirmed with
Argo CD's repo server on Ring 0 before anything depends on it (§11, phase D3).

### 7.4 Verified Pairs: which combinations are allowed

Ring 0 always runs one commit: all units at the same state. A community that
updates units at different times runs combinations Ring 0 may never have seen.
The rule that keeps this safe:

> Every Community Application's content must have run on Ring 0 together with
> the Platform content this community runs. (A **Verified Pair**: some verified
> commit in `ring0.json` has both fingerprints.)

Community Applications do not depend on each other (that is what the units in
§7.2 ensure), so pairs with the Platform are the only combinations that matter.

What the Operator sees: pressing *Update* on Photos may also show "this also
updates the Platform (cert-manager 1.19.1 → 1.19.2)". That happens when the new
Photos content was only ever verified with a newer Platform. Updating the
Platform may also show "this also updates Jitsi", when Jitsi's current content
was already replaced on Ring 0 before that Platform arrived. The Desk computes
the smallest set of moves that keeps every pair verified, shows it, and commits
it as one change. A lone, simple update is the normal case. The extra moves
appear only when they are needed.

### 7.5 Upgrade Stops

Some applications refuse to skip versions: Nextcloud cannot jump a major, and
Gallery cannot skip an Immich base change. Ring 0 only ever tests one step at a
time. A community that waited six months must therefore be led through the
steps in between.

`infrastructure/kubernetes/upgrade-stops.yaml` (base repository) lists them per
unit, by version. It is written by the PR that introduces such a version (for
majors, the Steward drafts it, §5.5). `ring0.json` carries the list. The Desk
then moves to the newest verified content *before* the stop, waits until it is
Healthy and its pre-update backup completed, and offers the next step. Each step
is one Desk action and one overlay commit.

## 8. The Update Desk

### 8.1 What it is and where it runs

A Platform Service in every cluster (new tenant `update-desk`, wave 1 because it
needs Keycloak). It has the same shape as the Invitation Desk
(`docs/adr/0058`):
- Python stdlib, source from a ConfigMap, no image build.
- It answers only on the Private Network, at `updates.<domain>`.
- Sign-in through Keycloak's master realm with the `admin` role.

`updates` is a new core subdomain, so it goes into the four subdomain maps, both
CoreDNS host lists and the hosts snippet (`CLAUDE.md`, "Node placement and DNS"),
and a manual-DNS install prints its record.

**Why not inside the Member Dashboard.** The Member Dashboard is Homepage, a
third-party start page for members on a public address. It cannot hold buttons
that change the cluster, and Operator interfaces stay off the public internet
(`doc/hardening.md`). A link to the Desk does not belong on it either: members
do not need to know it exists.

Ring 0 runs the Desk too, without a git token, so it is read-only there. Ring 0
thereby verifies every change to the Desk itself, and the e2e suite gets a spec
for its read-only page.

### 8.2 What it shows

**At the top, Ring 0's banner**, from `ring0.json`'s `now` (§6): up to date,
updating (which update, which step, for how long, how many wait behind it), or
stuck, always with "as of" and the time.

**Then one row per Update Unit** this community runs, plus the Platform, with a
badge that says where the row stands:

| Badge | Meaning | Offers |
|---|---|---|
| **Current** | Yours is Ring 0's newest verified content | nothing |
| **Update ready** | Ring 0 verified newer content, it is a Verified Pair and has passed the minimum soak | *Update* |
| **Soaking** | Ring 0 verified newer content, but less than the minimum soak ago | the time left until it is offered |
| **Being checked** | Ring 0 is verifying newer content for this unit right now, or it is queued there | nothing yet; the row says it may change soon |
| **Drift** | not Synced, not Healthy, or running something other than its pin | a link to the Application and what differs |

A row can carry two badges (*Update ready* and *Being checked*: something is
verified, and something newer is on its way). Rows that are not *Current* sort
first.

While the Desk is read-only and the community has no pin file yet (phase D2 in
§11), "Yours" comes from what the cluster runs: each Application's images and
chart versions, compared with `contents.versions` in `ring0.json`. That finds
every version difference, but not a change to manifests alone (a setting, a
resource limit). Once the pins exist, the fingerprints replace the comparison.

The columns:

| | Yours | Ring 1 (real members) | Ring 0 (verified) | Database | Undo |
|---|---|---|---|---|---|
| Platform | 2026-11-10 | 2026-11-12, 1 day | 2026-11-13, 3 h | — | revert |
| Photos (Gallery) | 5.8.0 | 5.10.0, 2 days | 5.10.0, 2 days green | migrates | restore only |
| Nextcloud | 32.0.1 | 32.0.1 | 32.0.2, 6 h green | migrates | revert |
| Jitsi | stable-10314 | same | same | — | — |

Per row: the versions from `contents`, the soak times, the linked change (PR
and upstream release notes), and the Upgrade Stops in the way. A state is
offered only once it is a Verified Pair (§7.4) and has passed the community's
minimum soak (§8.5).

The Desk reads its own state from the overlay (the pin file, through the git
token) and from the cluster (each Application's sync status, health and running
images), so it also shows **drift**: an Application that is not Synced or not
Healthy, or that runs something other than what the pins say.

### 8.3 Update

1. The Operator presses *Update* on one row and chooses the target: Ring 0's
   newest or Ring 1's current state.
2. The Desk shows the plan before anything happens:
   - the version changes;
   - the other units that move with it (§7.4);
   - the Upgrade Stop that splits it into steps (§7.5);
   - "the database is backed up first", and the undo for this update
     (revert or restore only).
3. On confirmation, the Desk **commits directly** to the overlay's `main`: the
   pin file and the regenerated kustomization files, in one commit. It uses the
   GitHub contents API with the current commit as a precondition, so a push made
   in between is noticed, re-read and shown again rather than overwritten. The
   commit message says what moved (`Update Photos to 05bdbcf: Gallery 5.8.0 →
   5.10.0`). An `Updated-by:` trailer names the Keycloak user. The overlay's
   history is the audit trail and the rollback point.
4. It asks Argo CD to refresh the root Application (annotation
   `argocd.argoproj.io/refresh=normal`; the Desk's RBAC allows `patch` on
   Applications in `argocd` and nothing else there).
5. It follows the update on the page: pre-update Backup completed (*exists*,
   `bases/pre-update-backup`), Synced, Healthy, then 10 minutes of quiet. If it
   is not Healthy after 30 minutes, the existing `ApplicationNotHealthy` alert
   fires (*exists*, `apps/update-alerts.yaml`) and the page shows the undo.

**Token.** A fine-grained token on this community's overlay repository only,
*Contents: read and write*, in the Secret `update-desk/update-desk-git`. The
Setup Wizard and `prepare-community-repo.sh` ask for it once. Without it the Desk
is read-only and says how to add it. Overlays on another Git Provider (Forgejo)
come later; the first version supports GitHub only.

### 8.4 Undo

- **Undo = revert:** an *Undo* button commits the previous pins back.
- **Undo = restore only:** no button. The page shows the runbook
  (`doc/storage-and-backup.md`, "Rolling an update back") with the backup name,
  namespace and Cluster filled in. On a real community, going back across a
  migration loses members' data, so a person decides and runs it. This is
  `docs/adr/0060`'s judgement, unchanged.

### 8.5 Notices replace Renovate in the overlay

The **Follow Policy** keeps its meaning (*when is the Operator told about an
update*) but changes its mechanism. It no longer writes a Renovate rule into the
overlay that opens PRs. The Desk checks `ring0.json` and `ring1.json` hourly and
sends one ntfy notice per newly offered state:

| Follow Policy | Notice when |
|---|---|
| Immediately | Ring 0 has verified it (Ring 1 uses this) |
| After two weeks (default) | Ring 1 has run it for 14 days |
| Security now, features later | security-flagged: when Ring 0 verified it; others: as the default |
| Only when I ask | never; the Desk still shows it |

The policy also sets the **minimum soak** the Desk requires before offering a
state at all (default: 24 hours green on Ring 0). The overlay's `renovate.json`
rule for the base (`smallworlds:follow-policy`, *exists*) is removed by
`pins.py migrate`. `admin-tools/lib/follow_policy.py` writes the Desk's
setting instead.

## 9. Tags and releases from now on

Tags stay, with a smaller job:

- **Installs** start from a tag. The Setup Wizard writes a pin file with every
  unit at that tag's commit on `main` (the commit the release commit was cut
  from). From then on, the Desk moves the community.
- **Bootstrap Pins** change only with a tag, since they reach new and rebuilt
  nodes only (`docs/adr/0057`).
- `bump-version.sh`'s Ring 0 gate (*exists*) stays. The weekly cadence does not:
  a tag is cut when an install baseline is worth refreshing or Bootstrap Pins
  change.
- `release-notes.py` (*exists*) keeps writing tag notes. Per-application notes
  move to the Desk, which reads them from `ring0.json` (`change` links).

## 10. What this replaces

| Earlier decision | Now |
|---|---|
| ADR 0060: Ring 1 adopts every release on the day it is tagged, by a merge | Ring 1 adopts per Update Unit through its Desk, on Follow Policy *immediately* |
| ADR 0060: Ring 2 adopts a release by merging a Renovate PR | Ring 2 adopts per Update Unit through its Desk |
| design-update-strategy §5.1: Follow Policy as an overlay Renovate rule | Follow Policy as Desk notices and minimum soak (§8.5) |
| design-update-strategy §4.3: weekly releases with meaningful numbers | tags as install baselines (§9) |
| Roadmap steps 4–5, implementation_plan_hermes.md: an in-cluster agent manages updates | a Ring 0 Steward in GitHub Actions: Ring 0 actions and PRs, never merges (§5.3) |

Unchanged: Ring 0 and everything the follower does today, pre-update backups,
post-update alerts, "only Ring 0 rolls back automatically", and the overlay as
the one source of truth for what a community runs.

## 11. Implementation order

Two tracks, independent of each other. The **Desk track** gives every Operator
the page to see and adopt Ring 0's state. The **Steward track** keeps Ring 0
moving when it gets stuck. The Desk does not need the Steward: without it, a
stuck Ring 0 shows as *stuck* on the Desk's banner and waits for Stephan, as it
does today. Each phase is useful on its own.

| # | Phase | Done when |
|---|---|---|
| 0 | **Observe.** Two weeks of the queue as it is; record every stuck case and what unblocked it. Drill the Gallery restore by hand (§5.2 precondition). | A list of real stuck cases, which becomes the Steward's test set |
| 1 | **Remove Hermes and Remediation** (§4, ADR 0063). *Done 2026-11-13, tag `hermes-final`.* Group digest/pin updates weekly (§5.6): still open. | Ring 0 green without them |

**Desk track**

| # | Phase | Done when |
|---|---|---|
| D1 | **Units and Ring State** (§6, §7.2): `update-units.yaml` and its test; the follower writes `ring0.json` with `now`, `verified`, `contents` and the back-fill. | `ring0.json` shows Ring 0 checking a queued update while it happens, and its history reaches `v1.3.0`'s commit |
| D2 | **Update Desk, read-only** (§8.1–8.2): the tenant, `updates.<domain>`, the banner and the badges. "Yours" from the running images, no pin file yet. No overlay changes. | Stephan sees from the Desk on Ring 1 which applications are behind and whether Ring 0 is busy |
| D3 | **Application Pins** (§7.1–7.3): confirm commit-hash refs through Argo CD on Ring 0; `pins.py`; the never-`HEAD` render test. Move Ring 1 from `v1.3.0` to pins at the tag's `main` commit (Stephan runs it). The Desk switches "Yours" to the pins. | Ring 1's rendered manifests differ from `v1.3.0` only in `targetRevision` values; nothing restarts |
| D4 | **Desk actions** (§8.3–8.4), Verified Pairs, Upgrade Stops, the git token. First real update: one Community Application on Ring 1. | One unit updated, undone, updated again through the Desk |
| D5 | **Notices and `ring1.json`** (§8.5, §6); remove the Follow Policy's Renovate rule; Wizard and `prepare-community-repo.sh` ask for the Desk token. Docs: `doc/tenant-update-desk.md`, `CLAUDE.md`, `README.md`. | A Ring 2 install from the wizard gets its first notice |

**Steward track**

| # | Phase | Done when |
|---|---|---|
| S1 | **Follower: stuck detection and evidence** (§5.1); `now.stuck` from S1–S5 instead of the stand-in. Then automatic restore on Ring 0 (§5.2) behind `AUTO_RESTORE`, after the drill in phase 0. | A provoked failure opens a `ring0-stuck` issue with a usable bundle |
| S2 | **Ring 0 Steward**, propose-only for PRs (§5.3–5.4): the self-hosted runner on Ring 0's server, the `ring0-steward` ServiceAccount, the attempt counter and `steward-working` pause in the follower, the protected-path rule in `mergequeue.py`, the subscription token. | It fixes a provoked failure within three attempts; after ~4 weeks, arm its PRs |

## 12. Open questions

- Commit-hash refs in Kustomize remote references through Argo CD's repo server
  (§7.3): expected to work, unconfirmed.
- Is `pod-archive` really independent of `photos`? The Immich export bridge
  lives in `tenants/immich/pod-export` and talks to `pod-gateway`
  (`doc/pod-archive.md`). If the protocol changes on both sides at once, they
  must be one unit.
- Does a security flag come from Renovate (`vulnerabilityAlerts`) or from a
  person? The "security now" Follow Policy needs one source.
- Retention of `ring0-before-*` backups once the follower restores by itself:
  three per namespace (*exists*) may be too few when several updates fail in a
  row.
- The Steward's monthly cost, measured in phase S2, decides the daily run limit.
