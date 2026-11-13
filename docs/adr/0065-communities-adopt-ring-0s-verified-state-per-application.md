---
status: proposed
---

# Communities adopt Ring 0's verified state per application

A community no longer moves from one release tag to the next with everything at
once. Its GitOps Overlay pins each **Update Unit** (the Platform, or one
Community Application with the applications it cannot work without) to a
commit of `main` that Ring 0 has verified. The Operator changes those pins on the
**Update Desk**, a page on the Private Network that shows, per application, what
this community runs, what smallworlds.network runs, and what Ring 0 has
verified. The Desk commits the change directly to the overlay. The design is
`plans-and-walkthroughs/design-per-app-updates.md` §6–9.

This replaces the parts of `docs/adr/0060` that say Ring 1 and Ring 2 adopt a
whole release by merging a Renovate PR. Ring 0, the verdicts, pre-update backups
and "only Ring 0 rolls back automatically" are unchanged.

The decision rests on five judgements.

**Operators update what they understand.** "Gallery 5.8 → 5.10, database
migrates, undo is restore only, running on smallworlds.network for 9 days" is a
decision an Operator can make. "v1.3.0 → v1.4.0" with nine applications in the
notes is one they postpone. Moving one application at a time also makes the
backup, the alert and the undo concern one application.

**Pins are commits, not per-application tags.** A commit hash is as immutable as
a tag, and Ring 0's `ring0/verified` status already marks which commits are
approved. Tagging per application would add a release ceremony for every update
and prove nothing more.

**Only combinations Ring 0 has run are offered.** Ring 0 runs every unit at the
same commit. A community that updates units at different times may not run a
combination Ring 0 has never seen: each application's content must have run on
Ring 0 together with the Platform content this community runs (a Verified
Pair). Updating one unit therefore sometimes moves another with it, and the Desk
says so before anything happens. Applications that depend on each other
(Nextcloud and its office suite, Stalwart and Bulwark) form one unit for the
same reason.

**The Desk commits; it does not open a PR.** The Operator has already decided
on the Desk's confirmation screen. A PR would add a second place to approve the
same thing. The overlay's history stays the audit trail and the rollback point:
every Desk commit names what moved and who moved it. This replaces, for
updates, `docs/adr/0008`'s rule that changes after the first commit are
normally proposed as pull requests; the overlay stays the durable source of
truth, which is the part of 0008 that matters.

**No overlay ever follows `HEAD`.** On `main`, Applications say
`targetRevision: HEAD`, and before `v1.2.44` that made overlays take tenants
straight from `main`. Pinning to a `main` commit would bring that back, so the
pin tooling writes an explicit commit for every Application from the base
repository, and a render test rejects any overlay where one is missing.

## Consequences

- The Ring 0 follower publishes what it verified, per unit, to the public
  `ring-state` branch (`ring0.json`). The Ring 1 overlay publishes what
  smallworlds.network runs (`ring1.json`) from a workflow, so no base-repository
  credential sits in Ring 1's cluster.
- Each community gives its Desk a fine-grained token with write access to its
  own overlay repository only. Without it the Desk is read-only. GitHub is the
  only Git Provider at first.
- Applications that must not skip versions get Upgrade Stops. The Desk leads a
  lagging community through them one verified step at a time.
- The Follow Policy keeps its meaning but becomes a notice from the Desk and a
  minimum soak, not a Renovate rule in the overlay.
- Release tags remain as install baselines and for Bootstrap Pin changes. The
  weekly release cadence of `docs/adr/0060` is dropped.
- `updates.<domain>` is a new core subdomain, with the usual four-map change.
