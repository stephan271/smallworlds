---
status: proposed
---

# A stuck Ring 0 is handed to an agent that acts on Ring 0

When the Canary Follower cannot move Ring 0 forward on its own, it hands the case
to the **Ring 0 Steward**: Claude Code running in a GitHub Actions workflow on a
self-hosted runner on Ring 0's server, signed in with the project owner's Claude
Pro subscription. The Steward may act on Ring 0, open pull requests and comment.
It tries at least three times before it hands the case to a person. It may never
merge, touch any other cluster, or change what judges its work. The design is
`plans-and-walkthroughs/design-per-app-updates.md` §5.

The follower decides what "stuck" means: a rejected commit, an error that
outlasts its retries, a queue that has merged nothing for 24 hours, a queued PR
that stays unmergeable, or a failed restore. It then opens an issue with the
evidence (Argo CD status, events, pod logs, the checks' output) in the private
Ring 0 overlay repository, and the issue starts the workflow.

The decision rests on five judgements.

**Deterministic first.** Where the way out is known, the follower takes it
itself. A failed update of a migrating application is restored from its
pre-update backup on Ring 0 without a person, because Ring 0 holds no member
data. The agent is for cases nobody has written a rule for.

**Most fixes are commits; some need the cluster.** Ring 0 usually gets stuck
because a chart renamed a setting, an upstream release is broken, or a test
assumed something that changed. Claude Code already reads, renders, tests and
writes PRs. But diagnosis needs the live cluster, and some cases end with a pod
that has to start again or a Job that has to run again. So the Steward reads
Ring 0 and may take the actions that leave no lasting difference from git:
delete a pod or a failed Job, start a Job from a CronJob, ask Argo CD to sync,
take a backup. It cannot read Secrets, `exec` into pods or delete data. Ring 0's
S3 key reaches Ring 1's backups, and a misled agent must have no path to it.

**Ring 0 stays the judge.** After every attempt the follower verifies the head of
`main` from the start. A Steward PR goes through the PR checks, the merge queue
and Ring 0's full verification like any update. PRs that touch the tests, the
canary checks, the follower, `renovate.json` or workflows need the repository
owner's approval before the queue merges them, so the agent cannot make Ring 0
green by weakening what checks it. Because its cluster actions leave nothing
behind, a green verdict still means "this commit works as written". Its PRs
start in a propose-only stage, where a person adds the queue label, until they
have earned trust.

**Three attempts, then a person.** One attempt is one run followed by a full
verification. A person is called after the third failure, or at once when the
next step needs something the Steward may not do (a restore while automatic
restore is off, a reviewed change to a protected path, a Secret, a refused
credential).

**No community needs an AI.** The credential and the agent belong to Ring 0
only. Every community's cluster stays free of AI providers and keys, which suits
a project about running your own infrastructure.

## Consequences

- The follower's token gains *Issues* on the Ring 0 overlay repository, and
  later *Contents* there for the automatic restore. The follower counts
  attempts and starts no verification while a Steward run is active.
- The Steward's token covers *Contents*, *Pull requests* and *Issues* on the
  base repository and *Issues* on the Ring 0 overlay. It has no *Workflows*
  permission, so GitHub refuses any change it makes to a workflow file.
- Ring 0's server runs a GitHub Actions runner under its own unprivileged user,
  registered to the private Ring 0 overlay repository only. Its only cluster
  credential is the `ring0-steward` ServiceAccount, whose RBAC enforces the
  limits above.
- Runs share the Claude Pro plan's usage limits with its owner's own use. A run
  stopped by a limit is retried later and is not counted as an attempt. If the
  Steward has to run without its owner, the workflow takes an API key with a
  spend limit instead.
- Majors can follow: the Steward prepares the PR from the upstream upgrade
  notes, and a person adds the queue label.
