# Getting Started with SmallWorlds

A plain-English description of everything a new operator does, in order, from
"I have nothing" to "my community has a running, private, backed-up cloud."

This document deliberately describes **what happens and why**, not which button
is where. It is written so it can be used as the specification for the Operator
Console's user experience: every numbered step below should correspond to
something the operator recognises as one decision or one piece of progress.

---

## 1. The two computers

Almost every misunderstanding about SmallWorlds comes from mixing up two
different machines. They have different jobs, they are usually not the same
computer, and only one of them ends up running anything permanently.

### The Launcher Host — your own laptop or PC

This is the computer you sit at. It is where you download and run the
SmallWorlds administration program. It does the thinking, the planning and the
talking-to-other-systems: it contacts your cloud provider, your Git host, and
the server that will run the cluster.

Important properties:

- **It is temporary.** Once installation is finished, you can close the program.
  The community's cloud does not depend on your laptop being switched on.
- **It is trusted.** It holds your API tokens, your recovery keys, and the
  authority to create or destroy the installation. This is why it stores things
  encrypted and why it never sends secrets to the browser.
- **It can be any of: Linux, macOS or Windows.** All three can install and
  administer a cluster on a remote Linux server.
- **It stays important after installation, but only for emergencies:** moving
  authority to a new laptop, recovering lost access, or shutting the
  installation down.

### The Cluster Node — the machine that runs the community's cloud

This is the Linux server that will actually host everything: files, photos,
chat, mail, the databases, the backups. It runs continuously.

It can be:

- **A rented cloud server** (Hetzner Cloud). You do not own or touch it
  physically; the launcher creates it for you and it costs money every month.
- **A machine on your own network** — an old desktop, a home server, a NUC, a
  rack machine in an office. You already own it; the launcher installs onto it
  over SSH.
- **The very same Linux laptop you are sitting at**, if you are on Linux and
  genuinely want that. This is possible but unusual, and it is offered only by
  the Linux version of the launcher.

The Cluster Node needs to be a supported Linux system with systemd, enough CPU,
memory and disk for the applications you choose, and a user you can log into
with either root or sudo rights.

### The third participant — GitHub

There is a third machine involved that you do not own and never administer:
GitHub. It plays two quite different roles, and keeping them apart avoids a lot
of confusion.

**Role one — where the SmallWorlds software comes from.** The public SmallWorlds
project on GitHub publishes the launcher downloads for each operating system,
the signed installation packages for each release, and the shared configuration
that every SmallWorlds installation is built from. This is a read-only
relationship: you take from it, you never write to it.

**Role two — where *your* configuration lives.** Your installation gets its own
**private** repository, holding the settings for this one community: which
applications you chose, your domain, your naming, and which SmallWorlds release
you pinned to. Today this is also hosted on GitHub — or on another provider that
speaks Git over HTTPS. You own it, and it contains no secrets.

The reason it exists is that SmallWorlds does **not** configure the server by
remote-controlling it. It writes the desired configuration into your repository,
and the server continuously reads that repository and makes itself match. The
repository, not the launcher, is the lasting source of truth.

This has a hard ordering consequence, which is why the journey is arranged the
way it is:

> **The private settings repository must exist, and contain the initial
> configuration, before any server is created or touched.** The very act of
> installing the cluster is to point it at that repository. There is no way to
> build the infrastructure first and configure it afterwards.

### How they all relate

```
   YOU
    │ ① download the launcher by hand, once
    ▼
┌───────────────────────┐                    ┌────────────────────────────────┐
│  Launcher Host        │  ② fetch signed    │  GITHUB                        │
│  (your laptop)        │     install pkg    │                                │
│                       │ ──────────────────►│  Public SmallWorlds project    │
│  smallworlds-admin    │                    │   · launcher builds, per OS    │
│  + browser on         │  ③ create + commit │   · signed install packages    │
│    127.0.0.1          │     your config    │   · shared config, at a tag    │
│                       │ ──────────────────►│                                │
│                       │                    │  YOUR private settings repo    │
└─────────┬─────────────┘                    │   · this installation's config │
          │                                  └───────────────┬────────────────┘
          │ ④ install Kubernetes + Argo CD,                  │
          │   pointed at the settings repo                   │ ⑤ Argo CD reads
          ▼                                                  │    it continuously
┌─────────────────────────────────────────────────────────────▼──────────────┐
│  Cluster Node (the server)                                                 │
│  Kubernetes + Argo CD + every application you selected                     │
└────────────────────────────────────────────────────────────────────────────┘
          ▲
          │ ⑥ from then on you administer it through the Private Network
   YOU ───┘
```

Steps ② and ③ both happen before step ④. Step ⑤ never stops: as long as the
cluster is running, it keeps making itself match what your repository says.

---

## 2. Glossary

These are the words used consistently throughout this document and throughout
the product. Where a familiar word is deliberately avoided, that is noted.

| Term | Meaning |
| --- | --- |
| **Operator** | You — the person who sets up and looks after the installation. (Not "admin user".) |
| **Launcher Host** | Your own laptop or PC, where the administration program runs. |
| **Bootstrap Launcher** | The program you download and run on your laptop. Its executable is called `smallworlds-admin`. (Not "installer script".) |
| **Cluster Node** | The Linux server that runs the community's cloud. |
| **Deployment Mode** | Which of the three supported shapes you are building: Hetzner-hosted, Local LAN-only, or Local internet-exposed. |
| **Cluster Profile** | The launcher's saved record of one installation: your answers, its history, its state, and references to its credentials. You can have several, one per installation. |
| **Setup Journey** | The whole ordered progression from an empty profile to a finished, protected installation. It can be paused and resumed. |
| **Journey Task** | One step of that progression, with prerequisites and evidence that it is done. |
| **Launcher Vault** | The encrypted store on your laptop holding tokens, passwords and keys. Values go in and never come back out to the screen. |
| **Cluster Capability** | One named thing the cluster provides. Either a Platform Service or a Community Application. |
| **Platform Service** | A capability that supports the system itself: identity, databases, storage, certificates, monitoring, backup, mail delivery, private networking. These are **not optional** — they are what the applications stand on, and they are installed in every mode. |
| **Community Application** | A capability people actually use: file sharing, photos, video calls, webmail, code hosting, project tracking, drawing, document editing. **Every one of these is optional** — you pick which ones your community gets, and an installation with none of them is still a valid installation. (One nuance: a few require another, so choosing collaborative document editing also brings in the file-sharing application it plugs into.) |
| **Settings repository (GitOps Overlay)** | Your own private Git repository holding this installation's configuration. It points at a fixed SmallWorlds release. (Not "the SmallWorlds repo" — that is the shared base.) |
| **Bootstrap assets** | The signed installation package belonging to one SmallWorlds release, downloaded by the launcher. See section 5. |
| **Change Plan** | An exact, reviewable description of one upcoming change: what it will do, what it costs, what could break, how to undo it. Nothing happens until you approve one. |
| **Workflow Run** | The record of an approved plan actually being carried out, including its progress and its proof of success. |
| **Private Network** | The private network (built on Headscale/Tailscale) through which you reach the administration interfaces after installation. |
| **Private Gateway** | The single private door into the cluster's administration interfaces. There is deliberately no public door. |
| **Operator Device** | A laptop or phone that has been enrolled in the Private Network and may therefore reach administration interfaces. |
| **Operator Console** | The administration interface itself. Before the cluster exists it is served by the launcher on your laptop; afterwards it runs inside the cluster behind the Private Gateway. |
| **Console Role** | What an administrator may do once inside: **Observer** (look only), **Operator** (routine changes and proposals), **Owner** (access and sensitive administration). |
| **Cluster CA** | The private certificate authority used when there is no public domain, so browsers on your own devices trust the cluster's HTTPS. |
| **Cluster Secret** | Secret material the running cluster needs. Deliberately kept out of Git. |
| **Recovery Point** | A moment in time you can actually restore a dataset back to. |
| **Protection Status** | Whether a dataset really has recent local *and* offsite recovery points — not merely whether a backup job exited successfully. |
| **Recovery Bundle** | An encrypted export that moves an installation's control to another laptop, or restores it after the first one is lost. |
| **Lifecycle Authority** | Whichever laptop currently holds the right to create, change or destroy the installation's infrastructure. Only one at a time. |

---

## 3. Choosing your Deployment Mode

This is the first real decision, and it changes what you have to bring with you.
Everything else in the journey is broadly the same.

| | **Hetzner-hosted** | **Local LAN-only** | **Local internet-exposed** |
| --- | --- | --- | --- |
| Where it runs | A rented cloud server | Your own machine at home/office | Your own machine at home/office |
| Reachable from the internet | Yes, for member apps | No, not at all | Yes, for member apps |
| Public domain name needed | Yes | No | Yes |
| Router changes needed | No | No | Yes, done by hand |
| Monthly cost | Yes, ongoing | Only electricity | Only electricity |
| Browser certificate trust | Public certificates, works everywhere | Private authority, must be installed on each device | Public certificates, works everywhere |
| Can you administer it from outside your home? | Yes | No | Yes |
| Good for | A community that needs to be reachable from anywhere | A household or office with no outside users | Someone who owns hardware and a domain and wants both |

A useful way to decide:

- **You want it reachable, you don't want to own hardware** → Hetzner-hosted.
- **You own a machine and everything stays inside one building** → Local LAN-only.
- **You own a machine and people outside must reach it** → Local internet-exposed.

### What you need to bring, per mode

**All modes:**

- A laptop or PC (Linux, macOS or Windows) to run the launcher.
- A GitHub account, *or* an existing empty Git repository reachable over HTTPS
  at another provider.
- An email address for administration and certificate notices.
- Somewhere to put offsite backups eventually — any S3-compatible storage
  service with an endpoint, a bucket, and access keys.
- Roughly an hour of uninterrupted time, though you may pause and resume.

**Hetzner-hosted, additionally:**

- A Hetzner Cloud account with a project, and an API token for that project with
  read *and* write permission.
- A domain name whose nameservers are delegated to Hetzner DNS.
- A payment method — the server bills by the hour from the moment it exists.

**Local LAN-only, additionally:**

- A Linux machine with systemd, reachable over SSH from your laptop, with root
  or sudo.
- Nothing else. No domain, no router change, no payment.

**Local internet-exposed, additionally:**

- The same Linux machine.
- A public domain name and access to its DNS provider (a token is needed so
  certificates can be issued by proving control of the domain).
- Administrative access to your router, because you will have to forward ports
  yourself. The launcher will tell you exactly which ones and why, but it will
  never change your router for you.
- Ideally a stable public IPv4 address; otherwise dynamic-DNS updating.

---

## 4. Getting the launcher

### Why there is a download at all

The Operator Console is the interface you use to build and run a SmallWorlds
installation. But at the very beginning there is no cluster, so there is nowhere
for that interface to live. The Bootstrap Launcher solves exactly that: it is the
same interface, temporarily hosted on your own laptop, until the cluster is able
to host it itself.

Its second reason for existing is to remove prerequisites. Building a Kubernetes
cluster normally needs Git, a GitHub CLI, Terraform or OpenTofu, `kubectl`, and a
JavaScript runtime installed and configured. The launcher contains or fetches
everything it needs, verified, so you install **one** thing.

This is the only download you fetch by hand. The launcher later fetches a second,
different package for the server itself — see section 5, which explains why the
two are separate.

### When you download it

Before anything else. It is step zero. You do not need an account, a domain, a
server or a token to download and start it — you can open it, look around, and
create a profile before you have decided anything.

### How you download it

Go to the SmallWorlds project's GitHub Releases page and open the latest
release. Each release contains one archive per platform, plus the files needed
to check that the archive is genuine.

| Your laptop | Download this |
| --- | --- |
| Linux, Intel/AMD 64-bit | `smallworlds-bootstrap-launcher_vX.Y.Z_linux_amd64.tar.gz` |
| Linux, ARM 64-bit (e.g. a Raspberry Pi 5, ARM laptop) | `smallworlds-bootstrap-launcher_vX.Y.Z_linux_arm64.tar.gz` |
| macOS, Intel | `smallworlds-bootstrap-launcher_vX.Y.Z_darwin_amd64.tar.gz` |
| macOS, Apple Silicon (M1 and later) | `smallworlds-bootstrap-launcher_vX.Y.Z_darwin_arm64.tar.gz` |
| Windows, Intel/AMD 64-bit | `smallworlds-bootstrap-launcher_vX.Y.Z_windows_amd64.zip` |

Alongside them the release publishes `SHA256SUMS` (the expected fingerprint of
each archive), `SHA256SUMS.sig` (a signature over that list),
`SHA256SUMS.pub` (the public key that signature belongs to), a software bill of
materials, and third-party licence notices.

**Check the download before you run it.** Compute the SHA-256 of the archive you
downloaded and compare it with the matching line in `SHA256SUMS`. If they differ,
delete the file and download it again. This takes thirty seconds and is the only
manual verification step in the whole process — everything the launcher fetches
later, it verifies by itself.

### Running it

Extract the archive and run the executable inside it, named `smallworlds-admin`.
There is no installer, no service, and nothing added to system startup. It runs
as an ordinary program under your own user account.

What happens when you start it:

1. It picks a random port on `127.0.0.1` — a loopback address, meaning nothing
   outside your own computer can reach it, not even other machines on your
   network.
2. It opens your browser at a one-time link that logs that browser session in.
3. If you start it a second time, it does not start a competing copy. It finds
   the one already running and reopens the browser on it.
4. Closing the browser does not stop work in progress. Anything the launcher is
   in the middle of doing keeps going, and you can reconnect later.

### Where the three modes differ

They don't. The same download, on any supported operating system, can build any
of the three Deployment Modes on a remote Linux server. The only
platform-specific restriction is that installing onto **the same computer the
launcher is running on** is offered only by the Linux build.

---

## 5. Bootstrap assets

### First: these are a different download from the launcher

It is worth being explicit, because the two are easy to conflate. There are
**two separate downloads**, and neither contains the other:

| | **The launcher** | **The bootstrap assets** |
| --- | --- | --- |
| Who downloads it | You, by hand | The launcher, automatically |
| When | Before anything else | Later, once you have chosen a release |
| Runs on | Your laptop | The Cluster Node (the server) |
| What it is | The administration program and its interface | The pinned Kubernetes and Argo CD software, plus the script that installs them |
| Roughly | The tool | The materials the tool works with |
| Verified how | You check the published checksum, once | The launcher checks the checksum and signature every time |

So the launcher is downloaded separately and first — it has to be, since nothing
else exists yet to fetch it for you. The bootstrap assets are then fetched *by*
the launcher, and they are not for your laptop at all: their contents are
destined for the server.

A useful consequence of keeping them apart: the launcher and the SmallWorlds
release it installs are versioned independently. A launcher you downloaded
months ago can install a SmallWorlds release that did not exist when that
launcher was built, because it learns about the release from a signed index at
install time rather than having it baked in. What it will *not* do is accept a
package signed by a key it does not already trust — that trust is compiled into
the launcher binary you verified by hand, which is exactly why that one manual
checksum check matters.

### Why they exist

To build a cluster, the installation needs specific software on the server: a
particular version of k3s (the lightweight Kubernetes), a particular version of
Argo CD (the component that keeps the cluster matching your Git repository), and
the script that installs them in the right order.

Two things could go wrong here, and bootstrap assets exist to prevent both:

1. **Version drift.** If the installer simply fetched "the latest k3s," two
   people installing the same SmallWorlds release a month apart would get
   different clusters, and neither would match what was tested. So every
   SmallWorlds release pins the *exact* versions it was built and tested
   against.
2. **Trusting the network.** Downloading and immediately running a script from
   the internet means trusting whatever answers. So the package is signed, and
   the launcher checks that signature against a key built into the launcher
   itself — not a key it learned from the same download.

Practically: this is why you never have to find, choose, or install Kubernetes
tooling yourself, and why you are never asked to supply a URL.

### What is in one

A bootstrap asset package belongs to exactly one SmallWorlds release and contains:

- The bootstrap script for that release, which turns a bare Linux machine into a
  running node.
- A runner that locates everything else in the package.
- The pinned k3s installer and the pinned Argo CD manifests.
- A `metadata.json` recording every version, source URL and cryptographic digest
  used to build the package.

What it deliberately does **not** contain: any credential, any of your
configuration, any secret, any key, or any kubeconfig. It is identical for
everyone who installs that release.

### When it is downloaded

Automatically, by the launcher, once you have chosen which SmallWorlds release
your installation should run — that is, after you have selected your
capabilities and before the server is touched. You do not initiate it as a
separate errand; you see it happen and you see its result.

The launcher will:

- Show you where the package is coming from (the official GitHub Release for
  that version).
- Download it, and resume rather than restart if the connection drops.
- Verify its checksum and its signature.
- Cache it privately on your laptop so a second installation of the same release
  does not download it again.
- Refuse outright if the package is damaged, substituted, signed by an unexpected
  key, older than expected, or does not match the release you selected. There is
  no "continue anyway."

Note that this is a *release payload*, not an offline installation kit. The
server itself still needs internet access during installation to pull container
images. Fully offline installation is explicitly future work.

---

## 6. The setup journey

What follows is the logical order of the whole installation. Steps are grouped
into phases; each phase has a purpose you can state in one sentence.

Three principles run through all of it and should be visible in the interface:

- **Nothing changes without an approved plan.** Before any step that creates,
  modifies or deletes something, you are shown exactly what will happen,
  what it will cost, what could break, and how it could be undone. You approve
  that specific plan. Approval expires if the situation changes underneath it.
- **Evidence beats optimism.** A step is complete when something was *observed*
  to be true, not when a command exited without error.
- **You can stop at any point.** Close the browser, close the laptop, come back
  tomorrow. The journey reopens where it was, re-checks what it had done, and
  continues.

### Phase A — Prepare your laptop

**Step 1. Create a Cluster Profile.**
Give this installation a name, choose your interface language, choose the
Deployment Mode from section 3, and choose which SmallWorlds release to install.
This creates the durable record everything else attaches to. If you administer
more than one installation, each gets its own profile.

**Step 2. Unlock the Launcher Vault.**
Decide how your secrets will be protected on this laptop. If your operating
system offers a credential store — macOS Keychain, Windows Credential Manager, a
Linux secret service — the launcher uses it, and you will not be asked again. If
it does not, you choose a passphrase, and you will be asked for it every time the
launcher starts.

Be aware: that fallback passphrase cannot currently be recovered by the product.
Keep it somewhere other than the laptop it protects.

From here on, every token, password and key you type goes into the vault. The
interface will afterwards tell you *that* a credential exists, where it came from,
when it expires and whether it should be rotated — but it will never show you the
value again.

### Phase B — Decide what the community gets

**Step 3. Choose capabilities.**
Choose what this cloud should be able to do, either by picking a preset —
Minimal, Collaboration, Full — or by choosing individually.

While you choose, you should be able to see:

- Which capabilities are mandatory platform services and cannot be turned off.
- What depends on what — turning on photo sharing turns on a database.
- A rough estimate of CPU, memory and storage that your selection needs.
- Which of them will be reachable by the public and which will not.
- What data each one will create that must later be backed up.

This choice determines how big a machine you need, so it comes before sizing.
It is also revisable later: applications can be added after installation, though
in the first release they cannot be removed.

**Step 4. Configure identity and naming.**
Give the administrative email address, the domain name (or the local naming, in
LAN-only mode), and the policy for how members will be invited to join.

### Phase C — Establish the settings repository

This phase comes before any server is created or touched, and it has to: the
installation in Phase E works by pointing the new cluster at this repository. It
must already exist and already hold the configuration by then.

**Step 5. Connect a Git provider.**
Provide credentials for where the configuration will live.

- **GitHub:** you are guided through creating a token with the right permissions,
  it is validated (correct scopes, correct account, not expiring next week), and
  a new private repository is created for you. Later changes will arrive as pull
  requests.
- **Another provider over HTTPS:** you supply the address of an existing *empty*
  repository and credentials for it. Access is validated. Later changes will
  arrive as clearly named branches with instructions for merging them yourself,
  because the launcher cannot open a pull request on a provider it has no API
  for. SSH repository addresses are not supported.

**Step 6. Write the initial configuration.**
The launcher renders the complete configuration for your chosen capabilities,
mode, domain and release, shows it to you as an exact file listing, and — once
approved — commits it to your repository. You now own a readable, versioned
description of your cloud, pinned to one immutable SmallWorlds release.

No secrets are ever written here. They go to the cluster separately.

**Step 7. Rotate the creation credential.**
The token used to *create* a repository is necessarily more powerful than the one
needed to *maintain* one. Immediately after creation you replace it with a
narrowly scoped, long-lived token. This step exists so that the powerful token is
short-lived by construction.

### Phase D — Prepare the machine

This is where the three modes diverge. Do the branch that matches your choice.

#### D-Hetzner — rent the machine

**Step 8H. Validate the Hetzner token and identify the project.**
The launcher confirms the token works, has write permission, and shows you which
Hetzner project it belongs to — so you cannot accidentally build into the wrong
one.

**Step 9H. Check that your domain is delegated.**
Your domain's nameservers must point at Hetzner DNS, otherwise certificates
cannot be issued later. This is checked now rather than discovered as a failure
in twenty minutes.

**Step 10H. Inspect the project.**
Read-only. The launcher lists what already exists in that project: servers,
volumes, IP addresses, firewalls, SSH keys, DNS zones. Anything that appears to
belong to a different installation is flagged as such, rather than quietly
reused. Nothing is created in this step.

**Step 11H. Size the installation.**
Based on your capability selection you are offered server sizes — smaller,
recommended, larger — with real availability by data-centre location and the real
recurring cost of each. You choose location and size.

**Step 12H. Review and approve the infrastructure plan.**
A single, complete, immutable plan: exactly which resources will be created,
which existing ones will be adopted, the monthly cost, and which resources will
keep costing money even if the installation is later removed. Nothing exists in
Hetzner until you approve this.

#### D-Local — use a machine you own

**Step 8L. Point at the machine.**
Give its address, the SSH user, and how you authenticate — an SSH agent, a key
file, or a password. Root or sudo both work.

**Step 9L. Confirm its identity.**
You are shown the server's SSH fingerprint and asked to confirm it. From then on
it is pinned: if the machine's identity ever changes, setup stops instead of
proceeding. Optionally, a dedicated key is generated for this installation so it
no longer depends on your personal key.

**Step 10L. Inspect the machine.**
Read-only, and thorough. It checks the Linux distribution and version, the
processor architecture, CPU cores, memory, free disk space, required network
behaviour, the ports it needs, the directories it will use, firewall state, and
whether sudo actually works. It also compares the machine's capacity against the
capabilities you selected and tells you whether they will fit.

Crucially it also looks for trouble: an existing Kubernetes installation it does
not recognise, or data in the paths it wants to use. These **block** setup rather
than being overwritten. If it finds an interrupted SmallWorlds installation that
belongs to *this* profile, it offers to resume it.

**Step 11L (internet-exposed mode only). Prepare public access.**
You supply the public domain and the DNS provider credentials used to prove
domain ownership when certificates are issued. You are then shown the exact
router forwarding rules required, what each one is for, and you confirm that you
have made them.

The launcher does not touch your router, by design — no UPnP, no vendor APIs.
You are also shown the limitations that come with running mail and video
conferencing from a home connection, because some of them cannot be worked
around.

**Step 12L. Review and approve the installation plan.**
What will be installed, which directories will hold data, what will be exposed,
what privileged operations are needed, how long it will be unavailable, and what
happens if it fails partway. This plan is bound to *that exact machine* — if the
machine changes, the approval is void.

### Phase E — Install

**Step 13. Install the cluster.**
The approved plan is carried out. In Hetzner mode this first creates the cloud
resources and the server; in local mode it goes straight to the machine. Then, in
both cases:

- k3s (Kubernetes) is installed.
- Argo CD is installed and pointed at your settings repository.
- The secrets the cluster needs are placed directly into the cluster, never into
  Git.

You watch progress as it happens. If your laptop sleeps or the network drops, the
work is resumed rather than restarted, and no creation step is repeated blindly.
You can cancel, and cancellation stops at a declared safe point rather than
tearing things down mid-operation.

Completion is not "the command succeeded." It is Kubernetes reporting the
expected components as actually ready.

**Step 14. Watch the cluster configure itself.**
Argo CD now reads your repository and installs everything you selected, in
dependency order. This takes a while — databases, storage, identity and
certificates come up before the applications that need them.

You see each capability move from planned to installing to healthy. Failures are
shown with the reason and are retried automatically where retrying is safe. Some
waiting here is normal, particularly for certificates: those depend on DNS, and
DNS takes as long as it takes.

### Phase F — Make administration private

This phase is the point of the whole design, and it is worth understanding.
During installation, temporary administrative access existed — an SSH path, a
Kubernetes API path. Leaving those open permanently is exactly how self-hosted
systems get compromised. So administration is moved onto a private network, and
the temporary paths are closed — but only after the private route has been
proven to work, because closing them first would lock you out of your own
cluster.

**Step 15. Establish trust for HTTPS.**

- *LAN-only:* there is no public domain, so no public certificate authority can
  issue certificates. The launcher creates a private certificate authority for
  this cluster and installs its root certificate on your device, with your
  explicit consent. This is what makes the padlock in your browser green rather
  than a warning. That root is stored in the vault and included in your Recovery
  Bundle — if it is lost, trust cannot be cleanly re-established.
- *Hetzner and internet-exposed:* public certificates are issued automatically,
  and your device already trusts them. Nothing to install.

**Step 16. Establish the Private Network and the Private Gateway.**
A coordination server (Headscale) and the Private Gateway are deployed. The
gateway becomes the one and only web entrance to the administration interfaces.
Stable private names are established — `console`, `grafana`, `argocd` — that
resolve only inside this private network.

Where the coordination server itself lives differs by mode: in LAN-only it stays
private and the whole thing is reachable only from your own network; in the two
public modes it is published under your domain, which is what lets you administer
the installation from somewhere else entirely. In both cases the administration
interfaces themselves remain private. A publicly reachable coordination point is
not a publicly reachable console.

**Step 17. Enroll your laptop as an Operator Device.**
The launcher acquires the private-network client software from its official
source, verified, asks you explicitly for permission to install it, and joins
your laptop to the network using a short-lived, single-use credential.

**Step 18. Verify private access.**
Prove, concretely, that from this laptop: the private names resolve, HTTPS works
and is trusted, the gateway is the one this cluster created and not something
pretending to be it, and the login service is reachable. Requests that arrive at
the public address pretending to be the console are checked to be rejected.

**Step 19. Close the temporary access paths.**
Only now — with the previous step's evidence in hand — the temporary SSH and
Kubernetes paths are narrowed or removed.

**Step 20. Claim the first Console Owner.**
A one-time invitation is created. You use it to register yourself as the first
Owner, with a passkey rather than a password. The invitation then disables itself
permanently, so it cannot be replayed. You are given the final private address of
your Operator Console.

### Phase G — Protect the data

An installation with no verified offsite copy is one hardware failure away from
being gone. This phase is part of setup, not an optional extra.

**Step 21. Understand what needs protecting.**
You are shown every dataset the installation now holds: which application owns
it, what produces its backups, on what schedule, and how long copies are kept.

**Step 22. Configure offsite storage.**
Supply an S3-compatible endpoint, region, bucket and access keys for storage
somewhere other than this machine. Access is verified. Where the storage service
supports being asked, versioning is inspected; where it does not, you are asked
to confirm it explicitly rather than being told something unverified.

**Step 23. Prove it works.**
A single, bounded validation run is triggered, and you watch for the evidence
that a recovery point genuinely arrived at the offsite destination. "The job
exited zero" is not accepted as proof and is not presented as such.

**Step 24. Export a Recovery Bundle.**
Export the encrypted bundle containing the profile, the infrastructure state,
the credentials and — critically for LAN-only — the private certificate
authority root. Protect it with a passphrase, and store it somewhere that is not
this laptop and not this cluster.

This is what lets you rebuild administration on a new computer if the laptop is
lost, stolen or replaced. It is the single most important file in the whole
system.

### Phase H — Finish

**Step 25. Final assessment and handoff.**
A last, honest review: everything that is healthy, everything that is degraded,
everything you acknowledged rather than resolved, and everything left incomplete.
Warnings are not hidden to make the screen look finished.

Then administration moves. From this point the Operator Console you use lives
*inside the cluster*, reached at its private address through the Private Network.
The launcher's job is done. You can close it.

Members reach their applications at the normal public addresses (or, in LAN-only
mode, from inside your network). They never see any of the above.

---

## 7. What the three modes look like side by side

Same journey, different middles:

| Phase | Hetzner-hosted | Local LAN-only | Local internet-exposed |
| --- | --- | --- | --- |
| A — Prepare laptop | identical | identical | identical |
| B — Choose capabilities | identical | identical | identical |
| C — Settings repository | identical | identical | identical |
| D — Prepare machine | validate token → check domain delegation → inspect project → choose size and location → approve costed plan | point at machine → pin SSH identity → inspect → approve plan | same as LAN-only, plus public domain, DNS credentials and manual router forwarding |
| E — Install | create cloud resources, then k3s + Argo CD | k3s + Argo CD over SSH | k3s + Argo CD over SSH |
| F — Private administration | public certificates; coordination published under your domain; administer from anywhere | private certificate authority installed on your devices; coordination stays private; administer from your network only | public certificates; coordination published under your domain; administer from anywhere |
| G — Protection | identical | identical | identical |
| H — Handoff | identical | identical | identical |
| Ongoing cost | server, volume and IP bill monthly | none | none |
| Notable caveat | resources keep billing until decommissioned | no access from outside; every new device needs the CA installed | mail and video conferencing may be limited by your home connection |

---

## 8. After installation

The launcher is no longer the everyday tool. These are the things you do from
the in-cluster Operator Console, through the Private Network:

- **Watch.** Every capability is assessed against five separate questions: is it
  configured correctly, has Argo CD delivered that configuration, is it actually
  running, is it reachable only where it should be, and is its data really
  protected. Each answer carries its evidence and when that evidence was
  collected. A perfectly running application with stale backups is shown as
  degraded, because it is.
- **Add an application.** Choose one that is not yet enabled, review its
  dependencies and its resource and backup implications, and open a pull request
  against your settings repository. Nothing is changed directly on the cluster,
  and nothing is merged for you. You merge, and then you watch it arrive.
- **Update SmallWorlds.** A newer release is offered with its release notes and
  the exact configuration change it implies. You review, propose, merge, and
  watch the cluster adopt it. Nothing updates silently.
- **Manage devices.** Issue a short-lived, single-use invitation for another
  laptop or phone; revoke one that is lost. Revocation warns you if it might lock
  out the last remaining Owner.
- **Check protection.** Coverage, local recovery points, offsite recovery
  points, retention and the last recorded restore test — kept distinct from one
  another rather than collapsed into a green tick.
- **Export diagnostics.** Assemble a support bundle, see a preview of exactly
  what it contains and what was redacted, and export it explicitly. Nothing is
  ever uploaded automatically; there is no telemetry.

And these are the things you go back to the launcher for:

- **Recover Owner access** if normal login or private access has broken. This
  works by proving you hold the installation's recovery authority, and creates
  one temporary single-use claim.
- **Move authority to another laptop** by exporting a Recovery Bundle on one and
  importing it on the other. Only one laptop holds authority at a time.
- **Shut the installation down**, in one of two ways:
  - *Preserve data:* stop and remove the running system while keeping persistent
    data, the settings repository and shared DNS. On Hetzner you are shown
    precisely which retained resources keep costing money.
  - *Full decommission:* remove everything the installation provably owns,
    including its data. This requires a fresh inspection, a review of your backup
    situation, and typing a confirmation tied to that specific installation.
    Anything whose ownership is uncertain is kept, never guessed at. Your Git
    repository and any shared DNS zone always survive.

Forgetting a profile on your laptop is a separate action from decommissioning,
and it changes nothing about the real installation.

---

## 9. Rules that never bend

If this document is used to redesign the interface, these are the properties the
design must not accidentally trade away. They are load-bearing.

1. **Approve before act.** Every mutation is preceded by a specific, immutable,
   reviewable plan. Approval covers that plan and expires if reality moves.
2. **Evidence, not exit codes.** Completion means something was observed to be
   true. Unknown and stale evidence are shown as such, never rounded up to
   healthy.
3. **Secrets are write-only.** A value is typed once. Afterwards the interface
   shows only that it exists, where it came from, when it expires and whether it
   should be rotated. Never the value — not in plans, logs, diagnostics, Git or
   the browser.
4. **The browser has no authority.** All planning, approval, execution and
   verification happen in the program on your laptop or in the cluster. The
   browser is a display.
5. **Configuration lives in Git.** Durable changes go through the repository —
   as a pull request or a branch — never as a direct edit to the running cluster.
   Nothing is merged for you and nothing is force-pushed.
6. **Administration is private.** The Operator Console, Grafana and Argo CD have
   no public route in any mode. Member applications may be public; the controls
   are not.
7. **Prove the new way before closing the old one.** Temporary access is
   withdrawn only after private access has been demonstrated to work.
8. **Resumable, always.** Closing the browser, restarting the launcher or
   losing the network never loses work and never causes a creation step to run
   twice.
9. **Refuse rather than guess.** Unrecognised Kubernetes installations,
   colliding data, changed machine identities, unsigned packages and
   ambiguously owned resources stop the process. They are not overwritten,
   adopted or deleted on a hunch.
10. **Nothing leaves without asking.** No telemetry, no crash reporting, no
    automatic upload of diagnostics.

---

## 10. Where the product actually is today

This document describes the complete intended journey. Implementation is
incremental, and it is worth being explicit about what an operator can do right
now versus what is still being built.

| Journey area | Status |
| --- | --- |
| Launcher, profiles, plan/approve/run/verify, resumability | Working |
| Launcher Vault and credential custody | Working |
| Recovery Bundle export and import | Working |
| Capability selection and configuration preview | Working |
| GitHub and generic-HTTPS settings repositories | Working |
| Signed bootstrap asset download and verification | Working; `v1.2.25` is published and verified |
| Local machine inspection | Working |
| Local installation of k3s and Argo CD | Working; final clean-machine qualification still outstanding |
| LAN-only private handoff and first Owner | Working; full dedicated-node browser test still outstanding |
| In-cluster capability observation | Working |
| Protection inspection and offsite configuration | Working; live S3 contract test outstanding |
| Adding an application by proposal | Working against test seams; live qualification outstanding |
| Operator device enrollment and revocation | Working against test seams; live qualification outstanding |
| Hetzner inspection, sizing, costing and provisioning | Implemented; blocked from real use until the signed OpenTofu toolchain descriptors are published |
| Local internet-exposed mode | Not yet built |
| Owner recovery, diagnostics export, release-update proposals | Not yet built |
| Preserve-data and full decommissioning | Not yet built |
| Native packaging for all five platforms | Working |
| English/German, accessibility, mobile | Implemented; final qualification pass outstanding |
| Full stable-release qualification across all three modes | Not yet done |

Two capabilities are explicitly *not* part of the first release: installing
without internet access (an Offline Bundle), and importing a cluster that was
built by the older shell scripts.
