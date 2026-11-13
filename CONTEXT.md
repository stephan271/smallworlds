# SmallWorlds Administration

The language used to describe installation and operation of a SmallWorlds cluster.

> **Much of this vocabulary describes the Operator Console, which is deleted**
> (`docs/adr/0062-the-operator-console-is-deleted.md`; deactivated before that by
> `docs/adr/0051`). The terms stay because ADRs 0001–0046 use them and those records
> keep their words. Terms that describe the console rather than the cluster are marked
> *(console; deleted)*.
> Installation is now described by **Setup Wizard**, and "Wizard" is no longer a word to
> avoid.

## Language

**Operator**:
The person responsible for configuring, observing, and maintaining a SmallWorlds cluster.
_Avoid_: Admin user, administrator

**Console Role**:
An Operator's in-cluster authority level: Observer for read-only access, Operator for routine actions and proposals, or Owner for access management and sensitive in-cluster actions.
_Avoid_: Keycloak group, admin level

**Operator Console** *(console; deleted)*:
The privileged interface through which an Operator sets up, observes, and eventually operates a SmallWorlds cluster.
_Avoid_: Admin dashboard, Homepage

**Setup Wizard**:
The single-use local web wizard that collects a cluster's settings and runs the setup scripts, then stops. It replaces the Bootstrap Launcher's role and has none after the cluster exists.
_Avoid_: Console, installer UI, launcher

**Bootstrap Pins**:
The K3s version, K3s install script and Argo CD manifest a new Cluster Node installs before Argo CD takes over, each named with an immutable URL and a SHA-256 in `infrastructure/bootstrap-pins.env`. Every bootstrap path reads them and verifies the downloads on the node.
_Avoid_: Bootstrap assets, bootstrap archive, input lock

**Bootstrap Launcher** *(console; deleted)*:
The locally executed product that made the Operator Console available before the SmallWorlds cluster existed. Superseded by the Setup Wizard.
_Avoid_: Desktop app, installer script

**Deployment Mode**:
One of the supported ways to establish a cluster: Hetzner-hosted, Local LAN-only, or Local internet-exposed. All three are first-class, and the Setup Wizard offers all three.
_Avoid_: Environment, target

**Launcher Host**:
The Operator's computer on which the Setup Wizard — formerly the Bootstrap Launcher — runs. It need not become part of the cluster.
_Avoid_: Local server, admin machine

**Cluster Node**:
The Linux server on which the SmallWorlds k3s cluster runs. In a Local Deployment Mode it is normally reached from the Launcher Host over SSH, but a Linux Launcher Host may also be its own Cluster Node.
_Avoid_: Local machine, target machine

**Cluster Profile**:
The Bootstrap Launcher's durable record of one cluster's desired setup, workflow history, infrastructure state, and references to credentials. A Cluster Profile can be reopened to resume or maintain that cluster.
_Avoid_: Environment, project

**Cluster Capability**:
A named piece of functionality managed and visualized by the Operator Console, classified as either a Platform Service or a Community Application.
_Avoid_: Component, app

**Platform Service**:
A Cluster Capability that supports infrastructure, identity, delivery, observability, networking, storage, or protection of the cluster.
_Avoid_: Infrastructure component, system app

**Community Application**:
A Cluster Capability used directly by community members.
_Avoid_: Tenant, optional app

**Capability State**:
The lifecycle condition of a Cluster Capability: planned, blocked, installing, healthy, degraded, failed, or disabled.
_Avoid_: App status, component status

**Capability Assessment**:
The explainable evaluation that derives a Capability State from configuration, delivery, runtime, access, and protection evidence.
_Avoid_: Health check, Argo status

**Protection Status**:
The evidence-backed assessment of whether a cluster dataset has sufficiently recent local and offsite Recovery Points under its declared retention policy.
_Avoid_: Backup status, job success

**Recovery Point**:
A verified point in time to which a dataset is expected to be recoverable.
_Avoid_: Backup file, snapshot

**Restore Drill**:
A controlled exercise that proves a Recovery Point can be restored into a non-production target and records the result.
_Avoid_: Test restore, backup check

**Lifecycle Authority**:
The single Launcher Host currently entrusted with infrastructure lifecycle operations for a Cluster Profile.
_Avoid_: Primary admin, controller

**Recovery Bundle**:
An encrypted, portable export that can transfer a Cluster Profile and its lifecycle authority to another Launcher Host or recover it after loss.
_Avoid_: Backup file, profile archive

**Launcher Vault**:
The encrypted store on the Lifecycle Authority that holds credentials and secret material referenced by Cluster Profiles.
_Avoid_: Keychain, secrets file

**Cluster Secret**:
Secret material required by a running cluster and intentionally excluded from its Desired Configuration history.
_Avoid_: Configuration value, GitOps secret

**Cluster CA**:
The private trust authority used to issue certificates for a Local LAN-only cluster.
_Avoid_: Self-signed certificate, local cert

**Setup Journey** *(console; deleted)*:
The resumable, dependency-driven progression from an empty Cluster Profile to an operational and protected cluster. The Setup Wizard has panels rather than a Journey: it holds no durable state, so there is nothing to resume beyond what the setup scripts already cache.

**Journey Task** *(console; deleted)*:
A revalidatable unit of progress in the Setup Journey with explicit prerequisites and completion evidence.

**Change Plan**:
An immutable, reviewable proposal for one mutation, including expected effects, risks, costs, downtime, and rollback information.
_Avoid_: Preview, confirmation screen

**Workflow Run**:
The durable execution record of an approved Change Plan, including redacted events, checkpoints, and verification evidence.
_Avoid_: Command, job

**Activity Record**:
The redacted, attributable history of plans, approvals, Workflow Runs, and their verification outcomes.
_Avoid_: Audit log, console log

**Member Dashboard**:
The existing low-privilege Homepage interface that lets community members discover and open applications.
_Avoid_: Operator Console, admin dashboard

**Member Invitation**:
A short, single-use code the Operator issues so that one person can become a community member, or so that an existing member can register a new passkey after losing theirs. It is redeemed on the join page and is used up when a passkey exists, not when it is opened. It is not the Keycloak action token, which is minted only at that moment (`docs/adr/0058-members-are-invited-through-a-join-page.md`).
_Avoid_: Invite link, onboarding link, Enrollment Invitation (that one admits an Operator Device to the Private Network)

**Invitation Desk**:
The Operator-only half of `join`, reachable from the Private Network, where Member Invitations are created, handed out, followed and revoked.
_Avoid_: Invite admin, admin page, console

**GitOps Overlay**:
The community-owned private repository that declares cluster-specific configuration while referencing a pinned SmallWorlds base release.
_Avoid_: Community repo, private overlay

**Release Ring**:
One of three stages an update passes through: Ring 0 is the Canary Cluster, Ring 1 is smallworlds.network, Ring 2 is every other community. Rings 1 and 2 adopt Ring 0's verified state per Update Unit through the Update Desk (`docs/adr/0060`, `docs/adr/0065`).
_Avoid_: Stage, channel, environment

**Canary Cluster**:
The Ring 0 cluster: every supported application at once, example data that is never re-seeded, following `main` automatically and reverting what fails its verification. Nothing is tagged until it has stayed green there.
_Avoid_: Staging (that is the ephemeral cluster of `test-pr-locally.sh`), reference instance, test cluster

**Follow Policy**:
The Operator's choice of when the Update Desk tells them about an update: immediately, after two weeks on Ring 1, security fixes now and features later, or only when asked. It also sets the minimum soak before the Desk offers a state. It only notifies; adoption is always the Operator's action on the Desk (`docs/adr/0065`).
_Avoid_: Update channel, auto-update

**Update Desk**:
The Operator-only page on the Private Network (`updates.<domain>`) that shows, per Update Unit, what this community runs, what Ring 1 runs and what Ring 0 has verified, and that updates or undoes one unit by committing Application Pins to the GitOps Overlay (`docs/adr/0065`).
_Avoid_: Update dashboard, admin console, Member Dashboard (that one is Homepage, for members)

**Update Unit**:
What moves as one when a community updates: the Platform (every Platform Service), or one Community Application together with the applications it cannot work without, such as Nextcloud with its office suite. Defined in `infrastructure/kubernetes/update-units.yaml`.
_Avoid_: App, tenant, component

**Application Pin**:
The line in a GitOps Overlay's `smallworlds-pins.yaml` that fixes one Update Unit to one commit of the base repository that Ring 0 has verified. Not the same as Bootstrap Pins (node install versions) or Renovate's digest pins (image checksums).
_Avoid_: Version, ref, release

**Verified Pair**:
An Update Unit's content together with the Platform content that Ring 0 has run at the same time, in some verified commit. The Update Desk only offers states that keep every unit in a Verified Pair.
_Avoid_: Compatible version, supported combination

**Upgrade Stop**:
A version of an application that an update must not skip, such as each Nextcloud major; the Update Desk moves a lagging community through it one verified step at a time.
_Avoid_: Required version, migration step

**Ring State**:
The public record of what Ring 0 has verified (`ring0.json`, written by the Canary Follower) and what Ring 1 runs (`ring1.json`), on the base repository's `ring-state` branch.
_Avoid_: Release manifest, status file

**Ring 0 Steward**:
The AI agent (Claude Code in a GitHub Actions workflow) that takes over when the Canary Follower reports Ring 0 stuck. It opens pull requests and comments, and never merges or touches a cluster (`docs/adr/0064`).
_Avoid_: Hermes (removed, `docs/adr/0063`), SRE agent, bot

**Git Provider**:
The hosting system through which the Operator Console creates or updates a GitOps Overlay and, when supported, opens configuration proposals.
_Avoid_: Git server, repository backend

**Desired Configuration**:
The durable, non-secret declaration of how a cluster should be configured, owned by its GitOps Overlay and reconciled by Argo CD.
_Avoid_: Settings, launcher state

**Runtime Action**:
A bounded operation against the current cluster that does not change Desired Configuration, such as starting an already-declared backup Job.
_Avoid_: Configuration change, direct fix

**Private Network**:
The network through which enrolled Operator Devices reach privileged cluster interfaces.
_Avoid_: Overlay, VPN

**Private Gateway**:
The cluster's sole web entrypoint for the Operator Console and linked operator interfaces on the Private Network.
_Avoid_: Private ingress, VPN proxy

**Operator Device**:
A computer enrolled in the Private Network for access to operator interfaces.
_Avoid_: Tailscale node, admin machine

**Enrollment Invitation**:
A short-lived, single-use grant that allows an additional Operator Device to join the Private Network.
_Avoid_: Pre-auth key, join token

**Offline Bundle**:
A future, versioned distribution containing the SmallWorlds release, managed tools, providers, packages, and container images needed to bootstrap without internet access.
_Avoid_: Installer archive, air-gap mode

**Existing Cluster Import**:
A future Setup Journey that creates a trustworthy Cluster Profile for a cluster established by the legacy scripts without rebuilding that cluster.
_Avoid_: Migration, adoption
