# SmallWorlds Setup Wizard — Design

**Status:** accepted; Phase 0 complete, Phase 1 next
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
  wizard.py          http.server: static files + JSON API + SSE log stream
  preflight.py       tool, credential, DNS, SSH and disk checks
  runner.py          subprocess supervision, marker parsing, SSE fan-out
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
| **4 · Applications** | checkboxes, sensible defaults | — |
| **5 · Review** | reads back the plan in plain language, plus the exact commands | — |

### Defaults for applications

Following ADR 0049 (mail is opt-in) and ADR 0037 (capacity-aware opt-in apps):

- **On:** `nextcloud`, `immich`, `forgejo`, `excalidraw`
- **Off:** `stalwart`, `bulwark`, `jitsi`, `collabora`, `plane`, `pod-gateway`

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
!!! missing:<VAR>                        a required value was absent
```

Stage ids are stable strings (`preflight`, `dns-zone`, `ssh-key`,
`terraform-apply`, `secrets`, `bootstrap`, `kubeconfig`, `argocd-converge`).

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

### Phase 1 — Wizard skeleton

Server, session token, `Host` guard, preflight checks, the five panels, and a
**dry run** that renders the plan and the exact commands without executing.

### Phase 2 — Execution

Subprocess supervision, SSE streaming, stage progress, cancel, resume.

### Phase 3 — Convergence and handover

Rollout view, credential panel, hosts snippet, dashboard handoff, shutdown.

### Phase 4 — Documentation

README "Deployment Instructions" collapses Steps 1–3 into one: run
`./smallworlds-setup`. The script route stays documented beneath it as the
advanced path. An ADR records this direction change and the console's status.

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
becomes optional; the 4 bootstrap assets are still required.

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
   capability. LAN installs currently choose between real Let's Encrypt over
   DNS-01 (which works, and is the default) and self-signed (which breaks OIDC
   outright). The wizard should probably not offer self-signed at all.
