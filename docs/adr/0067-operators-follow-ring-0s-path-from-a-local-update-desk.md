---
status: proposed
---

# Operators follow Ring 0's path from a local Update Desk

The first Update Desk runs on the Operator's own machine (`./smallworlds-update`,
`admin-tools/update-desk/`), not as a Platform Service in the cluster. It moves a
community step by step along the path Ring 0 itself took: every step ends on a
commit of `main` that Ring 0 verified, and the whole overlay moves to that commit
at once. This narrows `docs/adr/0065` for now. The Update Units, the never-`HEAD`
rule, the direct commit to the overlay and the per-step backups stay. Two parts
are deferred: per-unit pins with Verified Pairs, and the in-cluster page at
`updates.<domain>`.

The decision rests on four judgements.

**The first community to need it cannot run it in its cluster.** Ring 1 runs
`v1.3.0`. A Desk inside the cluster arrives only with a Platform update, which
Ring 1 would have to take some other way first. A local page works against any
overlay and any cluster the Operator can already reach with `git` and `kubectl`.
It needs no new subdomain, no router pair, no Keycloak client and no git token
in the cluster, and it holds no credential of its own.

**Every state a community passes through is one Ring 0 ran.** Almost every
commit on `main` touches the Platform, so per-unit pins rarely find a Verified
Pair other than "the same commit": updating Photos drags the Platform along,
and the Platform drags the other units. Following Ring 0's own sequence gives
what the Operator wants from per-application updates (one change at a time,
named by application, with its own backup and undo) without running
combinations Ring 0 never saw. Ring 0 merges one update per verified commit, so
a step is usually one application's update. The cost is the order: a community
cannot hold one application back while taking later steps. Holding back
remains the reason to build per-unit pins later.

**Steps are grouped, and the Operator chooses the grouping.** Ring 1 is 46
steps behind `main`. The default grouping takes runs of simple steps together
and every migrating step alone, as Ring 0 took them. That answers the release
notes of `v2.0.0`, which had to warn that PostgreSQL 16 → 18 and Gallery 5.11
"happen in one sync" when a whole release is taken. The other grouping splits
only where the rules require it: at a migration the pre-update backup does not
cover yet, at an Upgrade Stop, and around steps the Operator must act on.
Steps the Operator must act on live in `infrastructure/kubernetes/operator-steps.yaml`,
keyed by commit, so a community that adopts commit by commit sees them on the
right step.

**Nothing about an update is stored outside git and the cluster.** The Desk
reads where an update stands every time: written, committed, pushed, root pin
moved, rolled out. Restarting it, or doing a step by hand, leaves nothing to
reconcile. Each action shows the command it ran.

## Consequences

- An overlay leaves release tags with its first step. Its Renovate Follow
  Policy rule matches only `v…` tags and goes quiet. Notices (design §8.5) are
  the Desk's job, and are not built yet.
- The Desk reads Ring 0's verdicts from `ring0.json` and, for older commits,
  from GitHub's API with the Operator's token. A follower-published history
  (design §6, `verified`) would remove the token. It is the next step for
  Ring 2.
- The in-cluster Desk of `docs/adr/0065` can reuse these modules unchanged. Only
  its transport differs: a git token instead of the Operator's checkout, a
  ServiceAccount instead of their kubeconfig.
