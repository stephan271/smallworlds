---
status: accepted
---

# Hermes and Remediation are removed

The two in-cluster agents are deleted: `tenants/hermes` (the Tier 2 AI SRE agent)
and `tenants/remediation` (the Tier 1 deterministic responder), their
Applications, namespaces and Renovate groups, the `status-data` ConfigMap in the
dashboard tenant that Hermes was said to update, and `doc/tenant-hermes.md`. The
last commit that carries them is tagged `hermes-final`. The plan is
`plans-and-walkthroughs/design-per-app-updates.md` §4.

**They never ran.** Both have shipped with `DRY_RUN: "true"` since 2026-07 and
were never armed on any cluster. Remediation opened its pull requests against
`my-community-config`, an overlay that no longer exists. Hermes could only report
by email through Stalwart, which is optional since `docs/adr/0049` and absent on
smallworlds.network. The status ConfigMap was never written: Hermes's RBAC only
allowed reading pods and events.

**They sat in the wrong place.** Both were in the root kustomization, so every
community ran two idle pods, and arming Hermes meant an AI provider key inside
each community's cluster. The agent work the project actually needs is keeping
Ring 0 moving (`docs/adr/0064`). That happens in the base repository's git
history, not in a community's cluster.

**Kept code does not stay ready** (`docs/adr/0062`). Renovate kept proposing
updates for both, and the docs described behaviour that did not exist.

## Consequences

- A community loses both when it next adopts a Platform state without them: the
  root Application prunes their Applications. No overlay needs editing.
- Alerts reach the Operator through ntfy and healthchecks.io only
  (`doc/alerting.md`). Nothing escalates to an agent inside the cluster any more.
- Reviving either starts from the tag `hermes-final`, not from `main`.
- `implementation_plan_hermes.md` and `walkthrough-auto-operate.md` stay as a
  record, marked as replaced.
