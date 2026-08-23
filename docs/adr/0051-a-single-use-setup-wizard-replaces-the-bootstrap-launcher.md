---
status: accepted
---

# A single-use setup wizard replaces the Bootstrap Launcher

Installing SmallWorlds is a browser wizard that runs on the future Operator's own machine,
collects the settings, drives `admin-tools/prepare-community-repo.sh` and
`smallworlds-init.sh`, hands over the credentials, and stops. It never reimplements install
logic. The Operator Console — the Bootstrap Launcher of `docs/adr/0001` together with the
in-cluster administration surface it grew into — is deactivated but not deleted.

The launcher answered a real problem. Installing meant running two interactive shell
scripts in the right order and answering roughly twenty prompts, several of which only make
sense once the two-repo model of `docs/adr/0008` is already understood, and several of
which fail minutes later rather than at the moment they are answered wrongly. What it
answered that problem with was a second implementation of the install: some 63 000 lines of
Go and Svelte across 25 internal packages, carrying a credential vault
(`docs/adr/0010`), a SQLite workflow store (`docs/adr/0025`), a versioned capability
catalog (`docs/adr/0019`), a resumable journey (`docs/adr/0021`), an
inspect–plan–approve–execute–verify contract (`docs/adr/0022`), a lifecycle authority
(`docs/adr/0009`), age-encrypted recovery bundles (`docs/adr/0036`), and a cross-platform
signed release pipeline for five native archives (`docs/adr/0004`, `docs/adr/0046`). Every
one of those is a defensible decision on its own terms. Together they are a permanent
maintenance surface substantially larger than the thing being installed, and each release
of this repository had to build and sign artifacts for it before an Operator could obtain a
launcher at all.

The judgement recorded here is that the install does not need a second implementation. It
needs a front end. The scripts already cache every answer they take and pre-fill their
prompts from that cache, so making them run with no terminal is a shared helper and a
`SMALLWORLDS_NONINTERACTIVE=1` check rather than a rewrite; the wizard writes those same
two files and runs those same two scripts. One install code path survives, the terminal
route keeps working unchanged and is now scriptable as well, and a change to how a cluster
is installed is made in one place instead of two that must be kept in agreement.

The wizard is therefore constrained in ways the launcher deliberately was not. It is
single-use: it has no post-install role, and closing it is offered as soon as the cluster is
up. It holds no durable state, because the two cache files and the GitOps Overlay already
hold all of it and the Overlay remains the source of truth. It custodies no credentials,
because it lives only as long as the install and the values it handles are already written
to `0600` files by the scripts. It binds to loopback and is never exposed, so it needs no
authentication beyond a session token and a `Host` header check. It has no dependencies and
no build step — Python standard library and plain HTML — so it ships as files in this
repository and there is nothing to release. `docs/adr/0041`'s browser-first commitment is
kept; `docs/adr/0031`'s promise to maintain the legacy scripts is superseded by making them
the only implementation.

What the wizard adds over the scripts is not the form but the refusal: it will not submit
answers already known to fail. Whether the registrar in fact delegates the domain to
Hetzner DNS, whether each token is valid and carries the rights it needs, whether the data
and backup directories sit on different physical devices as `docs/adr/0048` requires — all
three are calls the scripts already make, moved to the moment the answer is given rather
than the moment it is used.

What is lost is the console's observation surface: node readiness, every Argo CD
Application's sync and health, and the reason the cluster itself gives for what has not
settled. The wizard does not provide it and does not pretend to; `kubectl` and Grafana
remain the answer for now. Whether that surface returns in a smaller form is deliberately
left open, and is the reason the console is deactivated rather than removed: its code, its
manifests and ADRs 0001 through 0046 remain in the repository unchanged, and only the four
places that caused it to be deployed are commented out. An Operator adopting a release from
this point prunes a running console, which is intended and is why this is a decision rather
than an omission.

See `plans-and-walkthroughs/design-setup-wizard.md` for the design and phasing.
