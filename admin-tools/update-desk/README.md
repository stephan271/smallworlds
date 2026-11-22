# Update Desk (local)

A page on the Operator's own machine that shows what Ring 0 has verified since
the base commit this community runs, and takes the community there **step by
step, along the path Ring 0 itself took**. Decision: `docs/adr/0067`. Design
background: `plans-and-walkthroughs/design-per-app-updates.md`.

```bash
./smallworlds-update --overlay ~/development/my-community \
                     --kubeconfig ~/.smallworlds/kubeconfigs/local.yaml
```

Both paths are remembered in `~/.smallworlds/update-desk/settings.json`. The
machine needs `git` with push access to the overlay, `kubectl` that reaches the
cluster (for Ring 1, that means being on its tailnet), and a GitHub token for
reading Ring 0's older verdicts: `GH_TOKEN`, or `gh auth login`. Any token
works, because the base repository is public.

## What it shows

- **Your applications**: one card per Update Unit this community runs
  (`infrastructure/kubernetes/update-units.yaml`), with the versions Ring 0 runs
  that yours does not.
- **Your path to Ring 0**: every *step* ends on a commit of `main` that Ring 0
  verified (`ring0/verified` = success), so every state the community passes
  through ran exactly like that on Ring 0. A step lists the commits since the
  previous step. Commits that do not reach this community count only through
  their net change: Stalwart updates for a community without mail, docs, or an
  update and its revert. Each step is marked:
  - *migrates* when a migrating tenant's application or PostgreSQL major changes.
    Undo is then a restore, not a revert.
  - *do something first / after* from `infrastructure/kubernetes/operator-steps.yaml`.
  - *Upgrade Stop* from `infrastructure/kubernetes/upgrade-stops.yaml`.
  - *release vX* where a tag was cut.
- **Two groupings.** *One migration at a time* (the default) keeps runs of
  simple steps together and puts every migrating step on its own, as Ring 0 took
  them. *Fewer, larger steps* splits only where it has to: at a migration the
  pre-update backup hook does not cover yet, at an Upgrade Stop, before a
  "do first" step and after a "do after" step. Any step can be chosen as the end
  point ("up to here").

## What it does

Each action is its own button, and each shows the command it ran ("What the
Desk did"):

1. **Plan**: version changes, databases that migrate (with *Back up now*
   buttons that create a CNPG `Backup`), steps to do by hand, how to undo, and
   the commit message.
2. **Write the overlay change**: rewrites every base ref in the overlay to the
   chosen commit. It adds a generated block that pins `targetRevision` of every
   base Application that tracks `HEAD` on main (ADR 0065 §7.3), renders the
   overlay, and refuses if any base Application would still read something else.
3. **Commit** only the files it wrote, with `Base-Pin:` / `Base-Pin-From:`
   trailers.
4. **Push** to `origin/main`. Applications the overlay routes to itself follow
   the overlay's `main` and start updating now.
5. **Move the root pin** (`smallworlds-root` → the pushed overlay commit), then a
   hard refresh.
6. It **follows the rollout** until every Application is Synced and Healthy at
   the new commits. Then it shows what is left to do by hand, and **Undo** (a
   revert to `Base-Pin-From`, refused when the step migrated a database).

Where an update stands is read from the overlay checkout and the cluster every
time, never stored. The Desk can stop and restart in the middle of an update,
and a step done by hand in a terminal shows up as done.

## Moving off a release tag

An overlay on a tag (`?ref=v1.3.0`) starts its path at the `main` commit that tag
was cut from. After the first step the overlay names commits, not tags. The
overlay's Renovate rule (`smallworlds:follow-policy`) matches only `v…` tags, so
it stops proposing release bumps from then on. That is intended: the Desk
replaces it.

## Writing the data files

- `operator-steps.yaml`: one entry per change that needs the Operator. Name the
  commit as it lands on main's first-parent line (the squash commit, or the merge
  commit of a merged branch). `test_desk.py` checks that.
- `update-units.yaml`: a new tenant or Application that belongs to an existing
  application goes into that unit. Anything not listed is Platform.

## Tests

`python3 admin-tools/update-desk/test_desk.py`, or `admin-tools/test-update-desk.sh`.
These tests use no network and no cluster: they build a base repository in a
temporary directory and use `tests/fake_kubectl.py` for the cluster.
