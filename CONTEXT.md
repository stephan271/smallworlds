# SmallWorlds Administration

The language used to describe installation and operation of a SmallWorlds cluster.

## Language

**Operator**:
The person responsible for configuring, observing, and maintaining a SmallWorlds cluster.
_Avoid_: Admin user, administrator

**Console Role**:
An Operator's in-cluster authority level: Observer for read-only access, Operator for routine actions and proposals, or Owner for access management and sensitive in-cluster actions.
_Avoid_: Keycloak group, admin level

**Operator Console**:
The privileged interface through which an Operator sets up, observes, and eventually operates a SmallWorlds cluster.
_Avoid_: Admin dashboard, Homepage

**Bootstrap Launcher**:
The locally executed product that makes the Operator Console available before the SmallWorlds cluster exists.
_Avoid_: Desktop app, installer script

**Deployment Mode**:
One of the supported ways to establish a cluster: Hetzner-hosted, Local LAN-only, or Local internet-exposed. All three are first-class modes of the Operator Console.
_Avoid_: Environment, target

**Launcher Host**:
The Operator's computer on which the Bootstrap Launcher runs. It need not become part of the cluster.
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

**Setup Journey**:
The resumable, dependency-driven progression from an empty Cluster Profile to an operational and protected cluster.
_Avoid_: Wizard, installation steps

**Journey Task**:
A revalidatable unit of progress in the Setup Journey with explicit prerequisites and completion evidence.
_Avoid_: Wizard page, script step

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

**GitOps Overlay**:
The community-owned private repository that declares cluster-specific configuration while referencing a pinned SmallWorlds base release.
_Avoid_: Community repo, private overlay

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
