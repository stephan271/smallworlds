---
status: accepted
---

# The Operator Console is deleted

The Operator Console and its Bootstrap Launcher are removed from the repository:
`operator-console/` (the Go server and Svelte client), its tenant and Application,
its image workflow, the launcher's packaging and signing scripts, `docs/releases/`,
`doc/tenant-operator-console.md`, `gettingStarted.md` (the console's UX
specification) and the implementation plan. The last commit that carries them is
tagged `operator-console-final`.

`docs/adr/0051` deactivated the console rather than deleting it, because the setup
wizard replaced only its install half. The observation half (node readiness, every
Argo CD Application's sync and health, the reason something has not settled) was
left without a replacement, and whether it would return in a smaller form was kept
open. That question stays open. What changes is the answer to "keep the code for
it?":

**Kept code did not stay ready.** It rotted while nobody ran it. The launcher's
cross-platform contract test drifted from the shell install path and failed every
pull request that touched a workflow, which blocked Renovate's pinning of the
GitHub Actions (#38) until the workflow was removed. Renovate kept opening update
PRs for the console's images, and its exclusions had to be carried in the merge
queue's rules, the domain patch generator and four commented-out reactivation
points.

**A return would not be this code.** ADR 0051 already expected a smaller form.
The operator interfaces built since then (the Invitation Desk, `docs/adr/0058`)
are small stdlib services on the tailnet, not a Go and Svelte application with its
own workflow engine, and Grafana, ntfy alerts (`doc/alerting.md`) and the Ring 0
follower (`docs/adr/0060`) now cover part of what the console was to observe.

**Nothing that matters runs it.** No overlay references it, and a cluster on
`v1.2.44` or later has pruned it, Ring 0 and Ring 1 among them.

## Consequences

- Reviving any part starts from the tag `operator-console-final`, not from `main`.
- No live overlay is on a tag before `v1.2.44` any more
  (design-update-strategy.md §5.5). One that were would take the console's
  manifests from `main` (`targetRevision: HEAD`) and see its `operator-console`
  Application fail to render; bumping it to a current tag prunes it.
- ADRs 0001–0046 remain as the record of the console's decisions and are not
  rewritten. Where one states a principle that is independent of the console, such
  as private operator interfaces (0012, 0038), secrets out of git (0010), no
  default telemetry (0029) or the overlay as source of truth (0008), the principle
  still holds through the code that implements it today. Their terms remain in
  `CONTEXT.md`, marked *(console; deleted)*.
- The installer no longer carries `CONSOLE_SESSION_KEY`. An old cache file that
  still has the line is harmless.
- `admin-tools/release-notes.py` keeps `operator-console` among the tenants it does
  not report, because older tags still carry it.
