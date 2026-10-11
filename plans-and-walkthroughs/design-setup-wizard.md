# SmallWorlds Setup Wizard — Design

**Status:** accepted and implemented; all four phases complete
**Date:** 2026-08-23
**Supersedes, in the install half only:** `implementation_plan-operator-console.md`

## 1. Why

Installing SmallWorlds today means running two interactive shell scripts in the
right order and answering roughly twenty prompts, several of which only make
sense once you already understand the two-repo model:

- [`admin-tools/prepare-community-repo.sh`](../admin-tools/prepare-community-repo.sh)
  — creates the private GitOps overlay repository, picks the optional apps,
  pins the upstream release tag, wires Renovate, pushes.
- [`smallworlds-init.sh`](../smallworlds-init.sh) — collects the domain, the
  deployment target and its credentials, generates every machine password,
  provisions (Terraform on Hetzner, or SSH bootstrap on a LAN machine), plants
  the cluster Secrets, and watches Argo CD converge.

The Operator Console was the answer to that: a Go + Svelte Bootstrap Launcher
plus a permanent in-cluster administration surface, some 63 000 lines across
25 internal packages, specified by 46 ADRs. It works, but it is a **second
implementation of the install** carrying a vault, a SQLite workflow store, a
capability-assessment engine, a lifecycle authority, recovery bundles and a
cross-platform release pipeline of its own — a permanent maintenance surface
far larger than the thing it installs.

The judgement recorded here is that the install does not need a second
implementation. It needs a **front end**.

## 2. What this is

A **single-use local web wizard**. It runs on the future operator's own laptop,
before any cluster exists, asks about five questions, drives the two existing
scripts, shows the rollout, hands over the credentials, and is never needed
again. The name *pre-dashboard* is literal: it lives before the cluster's own
dashboard exists, and the last thing it does is send you there.

**Its one architectural rule:** the wizard never reimplements install logic. It
collects answers, validates them, and executes the scripts that already exist.
There stays exactly one install code path, and the terminal route keeps working
unchanged for anyone who prefers it.

### It is not

| Not | Because |
| :--- | :--- |
| A console | It has no post-install role. It does not observe, maintain or change a running cluster. |
| A lifecycle authority | The overlay repo is the source of truth (ADR 0008), as before. The wizard writes it once. |
| A credential vault | Secrets live where they already live: the two `chmod 600` cache files, and cluster Secrets on the node. Nothing new is custodied. |
| A stateful service | No database, no workflow store, no profiles. Resume comes free from the scripts' existing idempotent cache files. |
| A remote-administration surface | Loopback only. It is never exposed, so it needs no authentication story beyond a session token. |

## 3. Architecture

```
admin-tools/setup-wizard/
  wizard.py          http.server: static files, JSON API, and (Phase 2) SSE
  checks.py          tool, credential, DNS, SSH and disk checks
  plan.py            defaults, the application catalog, and the dry-run plan
  runner.py          Phase 2: subprocess supervision, marker parsing, SSE fan-out
  test_wizard.py     server, defaults and plan
  test-interface.mjs the five panels, walked in a browser
  static/
    index.html       one page, five panels
    app.js           no framework, no bundler, no dependencies
    style.css
smallworlds-setup    top-level entry point: starts wizard.py, opens a browser
```

**Python 3.14 standard library only, no third-party packages, no build step** —
the same constraint the Hermes and Remediation agents run under, for the same
reason: the thing must start deterministically and offline, and shipping it is
copying files. `python3` is already a hard prerequisite of both scripts.

### Security posture

Small, but not optional. The wizard handles a Hetzner token, a Git token and
every generated cluster password.

1. Bind `127.0.0.1` only — never `0.0.0.0`.
2. Mint a random session token at startup; it goes into the URL the browser is
   opened with, and every API request must carry it. This stops other local
   users and stray browser tabs from driving the wizard.
3. Reject any request whose `Host` header is not loopback — defeats DNS
   rebinding, which is the one way a remote page can reach a loopback server.
4. Never return a submitted secret to the browser. Validation answers
   *"this token works, project `foo`"*, never the token itself.
5. Write secrets only to the files the scripts already own
   (`.smallworlds-cache.env`, `~/.config/smallworlds/community-setup.conf`),
   with their existing `600` mode. The wizard adds no new secret store.
6. Shut down after handover; do not linger as a background service.

## 4. The question set

The design target is **five required inputs** in the common case: domain, admin
email, one Hetzner token, one Git token, and the app selection. Everything else
has a defensible default and sits behind that panel's *Advanced settings*.

| Panel | Required | Defaulted (Advanced) |
| :--- | :--- | :--- |
| **1 · Your community** | domain, admin email | environment extension `""` (production), onboarding mode `invitation` |
| **2 · Where it runs** | Hetzner Cloud + API token — *or* — an existing machine + SSH target | data dir `/var/lib/smallworlds-data`, backup dir `/var/lib/smallworlds-backup`, certificates `letsencrypt`, internet exposure `no` |
| **3 · Settings repository** | one Git token → the wizard creates the private repo itself | repo name `my-community<ext>-config`, local path, pinned upstream tag (prefilled with the newest release) |
| **4 · Applications** | checkboxes plus one Office radio group, sensible defaults | — |
| **5 · Review** | reads back the plan in plain language, plus the exact commands | — |

### Defaults for applications

Following ADR 0049 (mail is opt-in) and ADR 0037 (capacity-aware opt-in apps):

- **On:** `nextcloud`, `immich`, `forgejo`, `excalidraw`
- **Off:** `stalwart`, `bulwark`, `jitsi`, `plane`, `pod-gateway`; the Office
  group explicitly starts at `none` rather than `collabora` or `euro-office`

Collabora, Euro-Office and no online editor are one mutually exclusive choice.
Selecting either editor also selects Nextcloud, because both are document
servers rather than standalone file stores.

The bulwark-without-stalwart warning that
[`prepare-community-repo.sh`](../admin-tools/prepare-community-repo.sh) prints
after the fact appears inline, at the moment the box is ticked.

### Live validation

This is where a browser genuinely beats a terminal, and it is cheap: every
check below is an API call the scripts already make, moved earlier.

- Hetzner token → valid? which project? does the DNS zone exist?
- The domain's registrar NS records → do they actually point at Hetzner DNS?
  (Silently wrong today; the ACME DNS-01 challenge then hangs with nothing
  saying why.)
- Git token → valid? does it carry repository-creation and PR scopes, which
  the in-cluster Renovate CronJob later needs?
- Local target → SSH reachable? systemd? sudo?
- Data dir and backup dir → **on different physical devices** (ADR 0048). The
  bootstrap refuses otherwise; refusing here costs seconds instead of minutes.
- Local tooling → `git`, `terraform`, `kubectl`, `ssh`, `python3`; an SSH
  public key exists for the Hetzner path.

Every one of these currently fails somewhere in the middle of a long run. The
wizard's real value is not the form — it is that the form cannot be submitted
in a state that is already known to fail.

## 5. Contract with the two scripts (Phase 0)

Both scripts already cache their answers and pre-fill prompts from them
([`smallworlds-init.sh:29`](../smallworlds-init.sh#L29) `ask_with_default`, and
the `STORED_*` config in `prepare-community-repo.sh`). Non-interactive mode is
therefore a small change, not a rewrite: **if the value is already known and
`SMALLWORLDS_NONINTERACTIVE=1`, do not prompt.**

A shared `admin-tools/lib/ask.sh` provides one implementation for both:

```
ask_value  "<prompt>" <VAR> [secret|plain] [required|optional]
ask_choice "<prompt>" <VAR> <default> <option>...
```

Behaviour:

| Mode | Value known | Value missing |
| :--- | :--- | :--- |
| Interactive (default) | prompt, pre-filled | prompt, empty |
| `SMALLWORLDS_NONINTERACTIVE=1` | take it, print what was taken | fail immediately with `!!! missing:<VAR>` on stderr, exit 78 |

Failing loudly on a missing required value is the point: a non-interactive run
that silently accepted a blank domain would produce a cluster named after
nothing.

### Progress markers

Both scripts emit single-line markers on stdout so the UI can show stages
rather than a wall of log. They are ordinary `echo`s, invisible in normal use:

```
>>> step:<id> <human-readable text>      a stage began
>>> ok:<id>                              a stage finished
>>> fail:<id> <reason>                   a stage failed
>>> guidance\t<heading>\t<detail>        what the operator can do about it
!!! missing:<VAR>                        a required value was absent
```

Stage ids are stable strings (`preflight`, `dns-zone`, `ssh-key`, `primary-ip`,
`terraform-apply`, `secrets`, `bootstrap`, `kubeconfig`, `argocd-converge`).

`guidance` exists because *"the installer failed, the log below has the
details"* is not something a first-time operator can act on, and the wizard is
in no position to invent advice — it does not know why a script stopped. The
scripts do, so they say it, once, in wording both routes share: a person at a
terminal reads it as an indented note, and the wizard renders it under the
failure as **What you can do now**. When a script stops without saying anything,
the wizard falls back to advice keyed on the stage that failed, and always ends
with the same two sentences: read the end of the log, and nothing has to be
undone.

### Non-goals for Phase 0

No behaviour change in interactive mode. A person running either script by hand
must see exactly what they see today — same prompts, same defaults, same order.
The regression test asserts this.

## 6. Execution and handover

1. The wizard writes the two cache files, then runs
   `prepare-community-repo.sh` and `smallworlds-init.sh` as subprocesses with
   `SMALLWORLDS_NONINTERACTIVE=1`.
2. stdout/stderr stream to the browser over Server-Sent Events: a log pane, and
   a stage list driven by the markers.
3. Convergence reuses the watcher already at
   [`smallworlds-init.sh:902`](../smallworlds-init.sh#L902), rendered as
   *n/total applications healthy* with the unhealthy ones named — instead of a
   scrolling counter that says nothing about what is stuck.
4. The final panel shows the generated credentials (Keycloak `admin` and
   `operator`, Argo CD, Grafana, the bulk-invite secret), a **Save
   credentials** button, the LAN hosts snippet where one applies, and a link to
   `https://dashboard<ext>.<domain>`.
5. Closing the browser mid-run loses nothing. Both scripts are idempotent and
   cache-backed; relaunching the wizard reads the same cache and resumes. This
   is why no workflow-persistence layer is needed.

## 7. Phases

### Phase 0 — Non-interactive scripts *(done, 2026-08-23)*

- `admin-tools/lib/ask.sh`, sourced by both scripts.
- Convert the 8 prompts in `prepare-community-repo.sh` (including the per-app
  loop) and the ~14 in `smallworlds-init.sh`.
- Emit progress markers.
- `admin-tools/test-noninteractive-install.sh`: drives both scripts from a
  fixture env file with no tty, asserts they complete without prompting,
  asserts a missing required value exits 78 naming the variable, and asserts
  interactive behaviour is byte-identical to before.

**Ships value on its own:** `SMALLWORLDS_NONINTERACTIVE=1 ./smallworlds-init.sh`
becomes scriptable, which `test-pr-locally.sh` and the lab drills can use.

40 checks pass, including a pty-driven assertion for each script that the
interactive prompts are byte-identical to before — `read -p` renders its prompt
only on a terminal, so a pipe-based test would have passed for the wrong
reason.

### Phase 1 — Wizard skeleton *(done, 2026-08-23)*

- `wizard.py` — loopback-only `http.server`, session token, `Host`-header
  guard, static files and the JSON API.
- `checks.py` — the live validation above, including a ~40-line stdlib DNS
  resolver for the nameserver check (`dig` is absent on plenty of desktops).
- `plan.py` — defaults read back from the two cache files (parsed, never
  sourced), the application catalog, field validation, and the dry-run plan.
- `static/` — the five panels: one page, no framework, no bundler, light and
  dark.
- `smallworlds-setup` — the top-level entry point.

`admin-tools/test-setup-wizard.sh` runs both halves: 51 checks over the server,
its defaults and its plan, and 34 more walking the five panels in a real
browser via the Playwright already installed for `e2e-tests/` (skipped, not
failed, when it is absent). The browser half matters here more than usual —
this is the one part of SmallWorlds whose entire purpose is what a person sees,
so "the API returned a plan" is not evidence that the wizard works.

Phase 1 explains and does not act: the Install button is inert, and a test
asserts that planning creates nothing.

### Phase 2 — Execution *(done, 2026-08-23)*

`runner.py` renders the answers into the two cache files — quoted for bash, and
created `0600` from the open, not chmodded after — creates the private
repository through the Git host's API rather than shelling out to `gh`, then
runs both scripts with `SMALLWORLDS_NONINTERACTIVE=1` in their own process
group. Markers become a stage list, `[n/120] x/y apps healthy` becomes a
progress bar, and everything else becomes a log streamed over SSE. Each new
watcher is handed a snapshot first, so reopening the page mid-install shows
everything that has happened rather than only what arrives next.

Two properties are worth stating because they are easy to get wrong:

- **Redaction.** Every value the operator supplied is stripped from the log
  before it leaves the runner, so a script that carelessly prints its own token
  cannot leak it into the browser. The log lives in memory and is never written
  to disk.
- **Generated credentials survive a re-run.** The installer cache is the only
  place the Keycloak, Argo CD and Garage passwords exist before the node does;
  regenerating them on a second attempt would lock the operator out of a cluster
  that is already up.

The browser tests grew a second pass that actually clicks Install, against a
checkout whose two scripts print markers and exit — the whole browser path
without touching a cloud account. It found four real bugs: `hidden` losing to
`display: flex` on the step rail and the footer, a load-order race that dropped
a running install back to the first panel, a stage left pulsing after the
installer had exited, and — the one that mattered — `_install` reading tokens
only from the session, so a token typed and immediately submitted, before the
debounced check behind that field had fired, never reached the installer at
all.

### Phase 3 — Convergence and handover *(done, 2026-08-23)*

The marker contract gained `sw_result <kind> <name> <value>` (tab-separated;
kinds `credential`, `note`, `url`, `kubeconfig`, `hosts`), and
`smallworlds-init.sh` emits the same facts it already prints in prose. Scraping
the prose with regexes was the obvious alternative and the wrong one: rewording
a message would silently empty the handover screen, and nothing would fail.
Like every other marker these are silent unless markers were asked for, so a
person at a terminal never sees them.

The handover screen shows the credentials with the notes that explain them, a
**Save to a file** button (built in the browser — the wizard never writes a
credential to disk), the addresses **for the applications actually selected**
rather than every address the installer prints, the `/etc/hosts` line where one
applies, the kubeconfig path, and a link through to the dashboard.

**Closing the wizard** is offered once the install is done, and refused while
one is running — shutting down mid-apply would orphan terraform. The wizard is
single-use, and a loopback server left running still holds the credentials in
memory.

### Phase 4 — Documentation *(done, 2026-08-23)*

README "Deployment Instructions" now opens with the wizard and keeps the
scripted route documented beneath it as a fully supported path — it is what you
want for automation, and it is what the wizard runs.
`docs/adr/0051-a-single-use-setup-wizard-replaces-the-bootstrap-launcher.md`
records the decision, what it supersedes, and what is lost by it.

## 8. The Operator Console's status

**Deactivated, not deleted.** Every line of
[`operator-console/`](../operator-console/),
`infrastructure/kubernetes/tenants/operator-console/`,
`infrastructure/kubernetes/apps/operator-console.yaml`,
[`doc/tenant-operator-console.md`](../doc/tenant-operator-console.md),
`implementation_plan-operator-console.md` and ADRs 0001–0046 is untouched. Only
the lines that caused it to be **deployed** are commented out:

| File | Change |
| :--- | :--- |
| `infrastructure/kubernetes/kustomization.yaml` | `- apps/operator-console.yaml` commented out |
| `infrastructure/kubernetes/namespaces.yaml` | the `operator-console` Namespace commented out |
| `admin-tools/prepare-community-repo.sh` | dropped from the always-installed `APPS` list |
| `smallworlds-init.sh` | `CONSOLE_SESSION_KEY` generation and the console Namespace/Secret in the cluster-secrets manifest commented out (the cache line is kept, so re-activation reuses the same key rather than logging everyone out) |
| `README.md`, `CLAUDE.md`, `doc/tenant-operator-console.md` | deactivation notices |

The two publish workflows are `workflow_dispatch`-only and the contracts
workflow only fires on `operator-console/**` paths, so **no CI change was
needed** — they simply stop being dispatched. Step 1b of the release checklist
becomes optional; the 4 bootstrap assets are still required (no longer —
`docs/adr/0057` replaced them with pins in the repository).

> **Effect on a running cluster:** adopting a release with this commented out
> **prunes** the console — the root app removes the Application, whose
> finalizer deletes its resources. That is intended, and it is the reason this
> is a deliberate deactivation rather than a quiet omission.

Reversing it is un-commenting those four places.

## 9. Open questions

1. **Does the observe half come back?** The console's *What happened* view —
   node readiness, every Argo CD Application's sync and health, and the reason
   the cluster itself gives for what has not settled — is genuinely useful
   after the install, and the wizard deliberately does not provide it. Options:
   revive just that tenant later on a much smaller footprint; fold the useful
   parts into the Homepage dashboard; or leave `kubectl` and Grafana as the
   answer. Not decided.
2. **Where does the wizard live for a first-time operator?** Today it requires
   a clone of this repository, which is fine for the current audience. If it
   should be obtainable on its own, the smallest honest answer is a single
   Python file plus a `curl | sh` bootstrap — explicitly *not* a return to
   cross-platform signed launcher archives.
3. **Local certificate authority.** ADR 0034's managed local CA was a console
   capability. LAN installs choose between real Let's Encrypt over DNS-01
   (which works, and is what the wizard defaults to) and self-signed (which
   breaks OIDC outright). Self-signed is still offered, behind *Advanced
   settings* and with the consequence spelled out on the panel and again in the
   plan. Removing it entirely is still arguably right.
