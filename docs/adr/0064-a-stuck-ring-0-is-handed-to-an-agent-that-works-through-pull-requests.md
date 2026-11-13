---
status: proposed
---

# A stuck Ring 0 is handed to an agent that works through pull requests

When the Canary Follower cannot move Ring 0 forward on its own, it hands the case
to the **Ring 0 Steward**: Claude Code running in a GitHub Actions workflow. The
Steward may open pull requests and comment. It may never merge, touch a cluster,
or change what judges its work. The design is
`plans-and-walkthroughs/design-per-app-updates.md` §5.

The follower decides what "stuck" means: a rejected commit, an error that
outlasts its retries, a queue that has merged nothing for 24 hours, a queued PR
that stays unmergeable, or a failed restore. It then opens an issue with the
evidence (Argo CD status, events, pod logs, the checks' output) in the private
Ring 0 overlay repository, and the issue starts the workflow.

The decision rests on four judgements.

**Deterministic first.** Where the way out is known, the follower takes it
itself. A failed update of a migrating application is restored from its
pre-update backup on Ring 0 without a person, because Ring 0 holds no member
data. The agent is for cases nobody has written a rule for.

**The fix is a commit, so the agent works in git.** Ring 0 gets stuck because a
chart renamed a setting, an upstream release is broken, or a test assumed
something that changed. Claude Code already reads, renders, tests and writes
PRs. Building another tool loop inside a cluster, as Hermes did, would mean
maintaining one and still not reaching the repository.

**Ring 0 stays the judge.** A Steward PR goes through the PR checks, the merge
queue and Ring 0's full verification like any update. PRs that touch the tests,
the canary checks, the follower, `renovate.json` or workflows need the
repository owner's approval before the queue merges them, so the agent cannot
make Ring 0 green by weakening what checks it. The Steward starts in a
propose-only stage, where a person adds the queue label, until its proposals
have earned trust.

**No community needs an AI.** The API key and the agent live in GitHub. Every
community's cluster stays free of AI providers and keys, which suits a project
about running your own infrastructure.

## Consequences

- The follower's token gains *Issues* on the Ring 0 overlay repository, and
  later *Contents* there for the automatic restore.
- The Steward's token covers *Contents*, *Pull requests* and *Issues* on the
  base repository and *Issues* on the Ring 0 overlay. It has no *Workflows*
  permission, so GitHub refuses any change it makes to a workflow file.
- After two Steward attempts on one incident, or when it says it needs a person,
  the Operator gets one ntfy notice.
- Majors can follow: the Steward prepares the PR from the upstream upgrade
  notes, and a person adds the queue label.
- The workflow runs in a private repository, so its cost is Actions minutes plus
  Claude API usage, both bounded by a daily run limit.
