# Ring 0 Steward: setting it up

The Steward is the AI that takes over when Ring 0 cannot move on by itself
(`plans-and-walkthroughs/design-per-app-updates.md` §5, `docs/adr/0064`). The
Canary Follower decides when it is needed and opens an **incident**, an issue in
the private `stephan271/my-canary-installation`. This folder holds the Steward
itself:

| File | What |
|---|---|
| `PROMPT.md` | Its instructions. A protected path: a change the Steward proposes needs the owner's approval. |
| `steward.yml` | The workflow. Copy it to `.github/workflows/steward.yml` in the Ring 0 overlay; it runs nowhere else. |
| `../install-steward-runner.sh` | The runner on Ring 0's server, as the unprivileged user `steward`. |

The follower's half is in `infrastructure/kubernetes/tenants/canary-follower`
(`src/incidents.py`, `src/evidence.py`, `steward-rbac.yaml`; README, "When Ring 0
is stuck").

## Setting it up, in this order

Each step works on its own, and the follower keeps following without the later
ones. Steps 1–2 give you incidents with evidence and no AI at all.

1. **The follower's token may write issues on the overlay.** Edit the
   fine-grained token in the Secret `canary-follower/canary-follower-github`
   (GitHub → Settings → Developer settings → Fine-grained tokens): add the
   repository `my-canary-installation` with **Issues: read and write**. The token
   keeps its value, so nothing on Ring 0 changes.

2. **Deploy this branch's follower** (it comes with `main`). `INCIDENTS: "true"`,
   `STEWARD: "false"` in `config.yaml`: incidents are opened, filled and closed;
   nobody is asked. Create the five labels once, so they have colours:

   ```bash
   for l in ring0-stuck:b60205 ring0-rejected-update:d93f0b steward-requested:1d76db \
            steward-working:0e8a16 ring0-needs-person:e11d21; do
     gh label create "${l%%:*}" --color "${l##*:}" -R stephan271/my-canary-installation --force
   done
   gh label create steward --color 5319e7 -R stephan271/smallworlds --force \
     --description "Opened by the Ring 0 Steward"
   ```

3. **The Steward's GitHub App.** GitHub → Settings → Developer settings → GitHub
   Apps → New GitHub App:
   - name `smallworlds-steward` (if taken, another name, then set
     `STEWARD_LOGIN: "<slug>[bot]"` in `config.yaml`);
   - no webhook (untick *Active*);
   - repository permissions: **Contents**, **Issues**, **Pull requests**: read
     and write. Metadata: read. Nothing else, in particular **not** Workflows or
     Administration;
   - *Only on this account*.

   Then *Generate a private key* (a `.pem` download) and *Install App* →
   *Only select repositories*: `smallworlds` and `my-canary-installation`.

4. **Keep the App off `main`.** A ruleset on the base repository, so nothing but
   repository admins (you, and the follower's token, which acts as you) can
   update `main`:

   ```bash
   gh api -X POST repos/stephan271/smallworlds/rulesets --input - <<'EOF'
   {"name": "main: admins only", "target": "branch", "enforcement": "active",
    "conditions": {"ref_name": {"include": ["~DEFAULT_BRANCH"], "exclude": []}},
    "rules": [{"type": "update"}, {"type": "deletion"}, {"type": "non_fast_forward"}],
    "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}]}
   EOF
   ```

   (`actor_id` 5 is the Admin role.) Renovate is not affected: it only opens PRs.

5. **The Claude credential.** On your own machine: `claude setup-token`. It
   prints a long-lived token for your Claude Pro subscription.

6. **The overlay's settings** (`my-canary-installation` → Settings → Secrets and
   variables → Actions):
   - secrets `CLAUDE_CODE_OAUTH_TOKEN` (step 5) and `STEWARD_APP_PRIVATE_KEY`
     (the `.pem` of step 3, whole);
   - variable `STEWARD_APP_ID` (the App's *App ID*, on its settings page);
   - variable `STEWARD_MODEL` only if your plan does not offer Opus 5.5 in
     Claude Code (check with `/model` in `claude`): then `claude-sonnet-5-5`.

7. **The runner.** On Ring 0's server, as root, with the three values from
   my-canary-installation → Settings → Actions → Runners → *New self-hosted
   runner* → Linux x64:

   ```bash
   RUNNER_URL=… RUNNER_SHA256=… RUNNER_TOKEN=… ./install-steward-runner.sh
   ```

   It refuses to finish if the ServiceAccount can read Secrets or exec into pods.

8. **The workflow.** Copy `steward.yml` to the overlay as
   `.github/workflows/steward.yml` and push. Pushes to the overlay do not move
   Ring 0 (its root Application is pinned to an overlay commit).

9. **Switch it on:** `STEWARD: "true"` in `config.yaml`. From the next incident
   on, the follower asks the Steward.

10. **After about four weeks** of Steward PRs you agreed with: repository variable
    `STEWARD_ARMED` = `true` on the overlay. Its PRs then join the merge queue by
    themselves, except those on protected paths, which keep waiting for your
    approving review.

## Trying it

Open an incident by hand to see one run without breaking Ring 0, for instance:

```bash
gh issue create -R stephan271/my-canary-installation --label ring0-rejected-update \
  --title "Steward test: explain the last rejected update" --body-file - <<'EOF'
<!-- ring0-incident key=R:manual-test kind=pr phase=new since=0 attempts=0 missed=0 -->
## Test

Find the most recent PR on stephan271/smallworlds labelled `ring0-rejected-*`, explain
why Ring 0 rejected it, and answer `needs-person` with your diagnosis. Change nothing.
EOF
```

With `STEWARD: "true"` the follower asks the Steward within one poll (5 minutes).

## When it needs you

`Ring0NeedsPerson` reaches you through ntfy like any alert. The incident says what
was tried, each attempt's verdict, and why the Steward stopped. Close the issue
when you have resolved it; the follower also closes it by itself once Ring 0 is
green again.

To stop the Steward at once: `STEWARD: "false"` in `config.yaml`, or disable the
workflow in the overlay (Actions → Ring 0 Steward → Disable). To revoke it: delete
the App's installation, and run `claude setup-token` again to replace the Claude
token.
