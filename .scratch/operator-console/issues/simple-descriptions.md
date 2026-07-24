==The following descriptions where created using Sol 5.6 High model from OpenAI, unless noted otherwise==

These issues describe planned capabilities (“ready for agent”), not proof that they already work. In plain terms, issues 01–26 would let an operator  create, protect, maintain, recover, upgrade, and eventually retire a SmallWorlds installation safely.

Some actions require a higher role:

- **Operator:** routine administration.
- **Owner:** sensitive access/security actions.
- **Lifecycle Authority:** whoever holds the cluster’s recovery authority and can recover or dismantle it.
- 
Issues 01–10 are marked complete, although issues 09 and 10 still note outstanding real-machine/browser qualification tests. Issue 11 is still in progress, so only part of that capability exists today.

### 01 — Prove the launcher workflow works

This is a safe rehearsal using a fake change rather than altering a real cluster.

The operator can:

- Start the launcher and connect through a secure local browser session.
- Create and reopen multiple cluster profiles.
- Review and approve a harmless example change.
- Follow its progress and cancel it safely.
- Close the browser and later reconnect without stopping the work.
- See evidence that the operation completed.

This does not provide a useful cluster change itself. It establishes the reliable foundation used by later real operations.

### 02 — Store passwords and tokens safely

The operator gets a secure vault for credentials used during setup and administration.

They can:

- Unlock the vault using the computer’s credential store or a passphrase.
- Save API tokens, passwords, and similar credentials.
- Restart the launcher without losing them.
- See whether a credential exists, where it came from, when it expires, and whether it needs rotation.
- Deliberately replace or remove it.

The actual secret is never displayed again and must not appear in profiles, plans, logs, diagnostics, or browser responses.

### 03 — Transfer recovery authority to another computer

The Lifecycle Authority can move control of a cluster from one launcher computer to another using an encrypted Recovery Bundle.

They can:

- Export the cluster profile, recovery material, infrastructure state, credentials, and resumable workflow history.
- Protect the bundle with a passphrase or advanced encryption recipient.
- Preview and verify a bundle before importing it.
- Restore authority on another supported operating system.
- Resume unfinished work after the transfer.

Corrupt bundles, incorrect passwords, mismatched clusters, and unsafe duplicate authority are rejected. This is effectively the cluster’s encrypted emergency handover package.

### 04 — Choose what the cluster should provide

The operator can select which platform services and community applications should be installed.

They can:

- Choose Minimal, Collaboration, Full, or Custom setups.
- See which components are mandatory.
- Understand dependencies between applications.
- Review estimated CPU, memory, storage, public exposure, and backup needs.
- See the exact files that would describe the selected cluster.
- Pin the configuration to a specific SmallWorlds release.

This creates a preview and change plan. It does not yet install anything or place secrets in Git.

### 05 — Create and use a private GitHub configuration repository

The operator can establish a private GitHub repository containing the cluster’s desired configuration without installing Git or GitHub CLI.

They can:

- Receive guidance for creating a suitably restricted GitHub token.
- Validate the token’s permissions, repository access, and expiry.
- Create and initialize a private repository.
- Store the selected SmallWorlds configuration there.
- Replace the powerful setup token with a narrower long-term token.
- Propose later changes through branches and pull requests.

The launcher never merges pull requests automatically, force-pushes, or hides live cluster changes outside Git. Tokens remain in the secure vault.

### 06 — Use a non-GitHub Git repository

The operator can use an existing empty repository from another provider, provided it supports Git over HTTPS.

They can:

- Enter the repository address and credentials securely.
- Validate access.
- Initialize the repository with the SmallWorlds configuration.
- Propose later changes on clearly named branches.
- Receive instructions for merging those branches manually.
- Resume safely after uncertain network failures.

It does not support SSH repository addresses. It also cannot promise to create a provider-specific pull request when the provider has no supported API.

### 07 — Download trusted installation software

The launcher can obtain the exact installation package belonging to the selected SmallWorlds release.

The operator can:

- See where the package is coming from.
- Download it from the official SmallWorlds GitHub Release.
- Resume an interrupted download.
- See download and cache status.
- Receive proof that the package matches its expected checksum and signature.
- Avoid manually locating or installing Kubernetes and GitOps tools.

The launcher rejects substituted, incompatible, downgraded, damaged, or incorrectly signed packages. Offline installation is explicitly future work.

### 08 — Check whether a local Linux machine is suitable

Before changing anything, the operator can inspect the Linux machine intended to run the cluster.

The target can be:

- A remote Linux machine reached over SSH.
- The same computer as the launcher, when running on supported Linux.
- Not the same computer when the launcher runs on macOS or Windows.

The operator can see:

- Machine identity and SSH fingerprint.
- Operating system and processor architecture.
- CPU, memory, and disk capacity.
- Required ports, directories, networking features, and privileges.
- Whether the chosen applications will fit.
- Existing Kubernetes installations or conflicting data.
- Whether an interrupted installation belongs to this cluster and can be resumed.

Inspection is read-only. Unknown installations, identity changes, occupied resources, and data belonging to another cluster block setup instead of being overwritten.

### 09 — Install SmallWorlds on the local Linux node

The operator can turn an approved and inspected Linux machine into a working SmallWorlds cluster.

They can:

- Review privileged changes, storage locations, exposure, downtime, and recovery behavior.
- Approve a plan tied to that exact machine and configuration.
- Install k3s, the lightweight Kubernetes system.
- Install Argo CD, which keeps the cluster aligned with its Git configuration.
- Supply required secrets without putting them in Git.
- Resume safely after the launcher or network is interrupted.
- Cancel at defined safe points.
- Verify actual Kubernetes readiness rather than trusting successful command output.

The core implementation is marked complete, but the issue still records an outstanding clean-node browser test against a final release package. In practical terms, the capability exists, but its final real-environment qualification is not completely closed.

### 10 — Finish a private LAN-only installation

The Lifecycle Authority can turn the newly installed local cluster into a privately administered system and hand normal control to the first Console Owner.

They can:

- Create the cluster’s private certificate authority.
- Install the required trust on the operator’s device.
- Establish stable private names for the console, Grafana, and Argo CD.
- Connect the launcher through the private Tailscale-based network.
- Verify private DNS, HTTPS, reachability, and gateway identity.
- Remove temporary SSH or Kubernetes access only after private access works.
- Create a short-lived first-Owner invitation.
- Register the first Owner using a passkey.
- Receive the final private Console address.

The administration interfaces are not exposed directly to the LAN or internet, and forged addresses are rejected. The launcher does not open router ports and does not promise remote access outside the LAN-only arrangement.

Like issue 09, the implementation is marked complete but still calls out an outstanding full browser test on a dedicated Linux node.

### 11 — See the health of everything in the cluster

This is intended to provide the first genuinely useful day-to-day Operator Console.

Once complete, authenticated users will be able to see each installed capability assessed across five separate questions:

1. Is it configured correctly?
2. Has Argo CD delivered the configuration?
3. Is the application actually running?
4. Is it reachable only where it is supposed to be?
5. Is its persistent data adequately protected?

The operator will be able to:

- See whether each capability is planned, blocked, installing, healthy, degraded, failed, or disabled.
- Understand why it received that status.
- See when the evidence was collected and whether it is outdated.
- Follow a relevant route to fix or investigate the problem.
- Open Grafana or Argo CD privately for deeper, read-only investigation.
- Distinguish “Argo applied it” from “the application really works.”
- See a stateful application marked degraded when its backups are stale, even if the application is still serving users.

Access will depend on role:

- **Observer:** view status but make no changes.
- **Operator:** perform permitted routine actions and proposals.
- **Owner:** access sensitive administration.

This issue is still in progress. The underlying assessment logic exists, but authentication, role enforcement, evidence collection, the complete console experience, and supporting integrations are not all finished yet.

#### Description from Claude AI:

SmallWorlds has two admin surfaces. The Launcher (issues 01–10) is the app on your laptop that builds a cluster. Issue 11 is the Operator Console — the admin dashboard that lives inside the running cluster and lets you watch and manage it afterward. Think of it as the cockpit you use day-to-day once the plane is built.

What I just committed is the console's brain: the logic that looks at a pile of facts about an app (Nextcloud, the database, etc.) and decides "this is healthy" or "this is degraded, and here's the one button to fix it." But right now the brain has no eyes, no login screen, and no visible pages. Those are the remaining steps.

Here's each one, why it exists, and whether you actually need it.

1. Evidence observers — the console's eyes

What: Code that actually queries the live cluster (Argo CD, Kubernetes, TLS certs, backups) and turns what it sees into the facts the brain judges.
Layman's version: The brain I built can score a health report, but nobody's filling in the report yet. Observers are the sensors that read the real cluster.
Necessary? Yes, non-negotiable. Without observers the console shows nothing real — the brain judges empty input. This is the core of "observe cluster capabilities."

2. Keycloak login + Console Roles — the door and the keys

What: Sign-in via your existing Keycloak identity, plus three permission levels — Observer (look only), Operator (routine changes), Owner (sensitive admin). Enforced on the server, not just hidden in the UI.
Layman's version: This is a control panel for your whole cloud. It needs a lock, and different keys for "can look" vs "can change things."
Necessary? Yes for any real deployment. It's acceptance criteria 1 and 2, and skipping it means anyone who reaches the console controls the cluster. You could defer it for a throwaway local demo, but not for something you'd actually run.

3. Serving mode + the actual web pages — the console's face

What: Making the same binary run in "inside-the-cluster" mode and drawing the real screens: an overview, a per-app detail page with the five facets and the fix-it buttons.
Layman's version: This is the dashboard you actually open in a browser. Brain + eyes + login are useless if there's no screen to look at.
Necessary? Yes. This is the product. It's the whole point of the issue.

4. Durable plans/runs (backed by Kubernetes + Loki logs) — a notebook that survives a reboot

What: When the console starts doing actions (not just watching), it records each proposed change and its progress so a pod restart doesn't lose track of in-flight work; detailed logs live in Loki, referenced not duplicated.
Layman's version: If the console is halfway through a task and the server restarts, it shouldn't forget what it was doing.
Necessary? Partly deferrable. For a first, observe-only console you can live without this — watching doesn't need durable memory. It becomes necessary the moment you add "propose a change" or "run a bounded action." It's acceptance criterion 9, so it's in scope for closing the issue, but it can safely be the last tracer.

5. Grafana & Argo CD read-only sign-in — hooking up the deep-dive tools

What: Wire Grafana and Argo CD to the same Keycloak login as read-only, so "investigate further" links open the right dashboard in a new tab.
Layman's version: When an app looks unhealthy, the console hands you off to the detailed graphs/sync view — using the same login, look-but-don't-touch.
Necessary? Yes for completeness (criterion 7), but low-risk and independent. It's mostly configuration (Keycloak clients via GitOps) rather than console code, and nothing else depends on it, so it can slot in whenever.

### 12 — Check whether data is really protected

The operator gets a clear overview of every important dataset and its backups.

They can see:

- Whether a local and offsite backup actually exists.
- How old each backup is.
- Whether the configured retention appears to be working.
- When restoration was last tested.
- Which applications are at risk because protection is missing or stale.

Crucially, “the backup job ran successfully” is not treated as proof that usable backup data exists. This issue only provides inspection; restoring or deleting backups is future work.

### 13 — Set up offsite backups

The operator can configure an external S3-compatible backup destination and verify that it works.

They can:

- Enter storage credentials securely.
- Check access to the bucket and, where possible, confirm versioning.
- Review exactly what will change.
- Run a limited test backup or replication.
- Confirm that an offsite recovery copy was actually created.
- Diagnose bad credentials, unsupported versioning, failed transfers, or outdated status information.

The credentials are kept out of Git, logs, and ordinary configuration screens.

### 14 — Add a community application

An Operator or Owner can enable an optional application after the cluster has been installed.

They can:

- Choose from supported applications that are not already enabled.
- See dependencies and estimated CPU, memory, storage, exposure, and backup implications.
- Review the exact configuration change.
- open a Git pull request for the change.
- Watch the application progress from approval through deployment and readiness.

The console does not merge the proposal automatically and does not directly alter the running cluster. Removing applications is not included in the first release.

### 15 — Add and remove operator devices

A Console Owner can control which laptops or phones may access the private administration interfaces.

They can:

- Create a short-lived, one-use invitation for a new device.
- Guide the device through private-network enrollment.
- Confirm that it can reach private operator services.
- Revoke a lost or untrusted device.
- See a warning if revocation might lock out the last Owner.

Revocation affects only the selected device and leaves an audit record.

### 16 — Recover lost Owner access

The Lifecycle Authority can regain Owner access if normal login or private-network access stops working.

They can:

- Prove that they hold the correct cluster recovery authority.
- Inspect existing Owner access where possible.
- Approve a clearly marked, sensitive recovery operation.
- Create one temporary, single-use Owner claim.
- Use it to establish a replacement identity and passkey.

Existing Owners are not silently removed or reset. This is recovery of routine access, not an automatic takeover of every identity.

### 17 — Create safe diagnostics for support

An operator can assemble a support bundle without automatically sending private operational data anywhere.

They can:

- Preview exactly what the bundle will contain.
- Review what was removed or hidden.
- Exclude optional categories.
- Export the archive explicitly.
- Perform the workflow from a phone if necessary.

Passwords, secret values, private keys, Kubernetes credentials, and similar sensitive material must be excluded. Nothing is automatically uploaded, and there is no default analytics or crash reporting.

### 18 — Inspect and plan a Hetzner installation

Before creating anything, the operator can inspect a Hetzner project and prepare a reliable costed plan.

They can:

- Securely supply and validate a Hetzner API token.
- Discover existing servers, volumes, IPs, firewalls, SSH keys, and DNS resources.
- Identify ownership conflicts rather than accidentally reusing similarly named resources.
- Check whether the public domain is correctly delegated.
- Choose a small, recommended, or high-capacity setup.
- See availability, estimated recurring cost, and resources that may remain billable.
- Review the complete infrastructure plan before approving it.

This issue performs planning only; it must not create or change Hetzner resources.

### 19 — Build the Hetzner cluster

The operator can take an approved Hetzner plan and turn it into a working SmallWorlds cluster.

They can:

- Provision the approved cloud resources.
- Install Kubernetes and the SmallWorlds configuration.
- Resume safely after network or launcher interruptions.
- Establish private administration access.
- Enroll the first operator device and create the first Owner.
- Verify DNS, certificates, login, and connectivity.
- Remove temporary public administration access after private access is proven.

The Operator Console, Grafana, and Argo CD remain private even if some member-facing applications are public.

### 20 — Build an internet-facing local cluster

The operator can install SmallWorlds on a local machine while exposing selected member services to the internet.

They can:

- Configure the public domain and DNS provider.
- Receive exact router port-forwarding instructions.
- Complete and acknowledge the router changes manually.
- Obtain public certificates.
- Make the required member services and device coordination reachable.
- Keep administration interfaces accessible only through the private gateway.

The launcher deliberately does not change the router automatically. Some mail and video-conferencing limitations may still apply and must be explained.

### 21 — Propose a SmallWorlds update

An Operator or Owner can review and propose an explicit release upgrade.

They can:

- See an authenticated, compatible release.
- Read its release notes.
- Review the exact configuration changes.
- Understand possible downtime, data, exposure, and recovery risks.
- Open a pull request for the update.
- Watch the cluster adopt the update after someone merges it.

Nothing is silently installed. The console does not merge the update or directly mutate the cluster.

### 22 — Shut down the cluster but preserve its data

The Lifecycle Authority can dismantle the running system while retaining the data needed for possible recovery.

They can:

- See exactly what will be stopped, deleted, or retained.
- Preserve persistent data, shared DNS zones, and the Git configuration.
- See continuing cloud costs for retained resources.
- Safely resume an interrupted shutdown.
- Remove the local cluster software while preserving its data directory.

Anything with uncertain ownership is retained rather than deleted. Merely forgetting the cluster profile on the launcher is a separate action and does not alter the real cluster.

### 23 — Permanently decommission the cluster

The Lifecycle Authority can completely remove cluster-owned infrastructure, including persistent data, with stronger safeguards.

They can:

- Review current backups and recovery material first.
- See every proposed deletion and its irreversible consequences.
- Use a typed confirmation tied to that exact cluster and plan.
- Explicitly proceed even when backups are insufficient, if stopping paid infrastructure is necessary.
- Export a final, redacted activity record.

Only resources proven to belong to the cluster are deleted. Shared Git configuration and DNS zones remain, and uncertain resources default to being retained.

### 24 — Run the launcher on ordinary computers

The launcher becomes a native application for:

- Linux on Intel/AMD and ARM.
- macOS on Intel and Apple Silicon.
- Windows on Intel/AMD.

The operator can download and run it without separately installing Git, Kubernetes tools, OpenTofu/Terraform, GitHub CLI, Node.js, or similar developer tooling.

It can continue long-running work after the browser closes, reopen the existing secure session, and manage a remote Linux cluster from any supported desktop platform. Installing a cluster on the same machine remains Linux-only.

### 25 — Make the console accessible, bilingual, and mobile-friendly

All primary operator workflows are checked and improved for practical use by more people.

The operator gets:

- Fully authored English and German interfaces.
- Correct local dates, sizes, durations, and currencies.
- Keyboard and screen-reader-friendly workflows.
- Text and icons instead of color-only status indicators.
- Light, dark, high-contrast, and reduced-motion support.
- Phone-compatible status, diagnostics, setup, planning, and emergency recovery.
- Accessible alternatives to charts and timelines.

This is mainly a quality and usability milestone rather than a new administrative power.

### 26 — Prove the first stable release is genuinely ready

This is the final release gate. It verifies the whole product in all three supported deployment modes:

1. Hetzner-hosted.
2. Local, private LAN-only.
3. Local with selected internet-facing services.

For the operator, it means the documented journeys—from installation through backups, device access, updates, recovery, diagnostics, and decommissioning—have been tested together under realistic conditions.

The release cannot be called stable until security scans, accessibility, German localization, mobile use, recovery, interruption handling, packaging, documentation, and all deployment-mode tests pass. Offline installation and importing an existing cluster remain explicitly outside the first release.