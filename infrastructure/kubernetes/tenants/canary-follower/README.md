# Canary Follower

Ring 0's follower (`docs/adr/0060`, `plans-and-walkthroughs/design-update-strategy.md`
§3.4–3.5). It runs on the Canary Cluster only and makes that cluster follow `main`:

1. Every `POLL_SECONDS` it reads `main`'s head commit from GitHub.
2. **Backup:** if the commit touches a tenant whose database an update may migrate
   (`MIGRATING_TENANTS`), every CNPG cluster of that tenant gets an on-demand Backup
   `ring0-before-<sha12>` first, and the follower waits until it has completed. Those
   tenants reach Ring 0 through the overlay's remote base, so nothing of the new commit
   runs before the backup. A failed backup stops here: nothing is synced. The newest
   three of these Backups are kept per namespace.
3. On a new commit it hard-refreshes and fully syncs `smallworlds-root`, then every
   other Application. A hard refresh is needed because a remote Kustomize base is
   served from cache; a full sync because Sync hooks do not run on self-heal.
4. It waits until every Application is Synced and Healthy (`CONVERGE_TIMEOUT_SECONDS`).
5. It checks that every Application sourced straight from the base runs *that*
   commit. If `main` moved on meanwhile, the result is `error` and the newer commit is
   verified next.
6. **Smoke:** every Ingress host must answer `https://<host>/` below 500, with a
   valid certificate, from inside the cluster.
7. **Checks:** a Job cut from the suspended CronJob `canary-checks`
   (`checks-job.yaml`, `checks/run-checks.sh`) fetches that commit's `e2e-tests/` and
   `admin-tools/canary/`, runs the e2e suite with full OIDC (test users onboarded with a
   passkey through a real action-token link), then `seed.py check`: the example
   members' photos, files, repository and mail, read back with a fresh passkey
   sign-in. Its summary line goes into the status.
8. **Quiet:** no `critical` alert in Alertmanager for `QUIET_SECONDS`.
9. It posts the verdict as the commit status `ring0/verified`.
10. **Merge queue:** while the head is green, it squash-merges the oldest Renovate PR
    labelled `ring0-queue` that is mergeable and has passed its PR checks. One update
    per commit, so each verdict is about one update. A rejected commit is handled as in
    "When a commit is rejected" below.

| State | Meaning |
|---|---|
| `pending` | being synced and verified |
| `success` | every step passed |
| `failure` | Ring 0 did not take the commit: not converged, smoke, checks or a critical alert |
| `error` | the commit could not be judged: `main` moved on, the pre-update backup failed, or the follower failed |

The status is also the follower's memory. A restarted pod skips a commit that has a
final state and verifies again one left at `pending`. An `error` is no verdict on
the commit (GitHub, the object store or npm was away, or the checks Job could not
fetch the commit: it says `CHECKS-INFRA`), so it is never reverted, and the head is
verified again after `ERROR_RETRY_SECONDS` (an hour), at most `ERROR_RETRIES` (3)
times; meanwhile the merge queue waits. A `failure` is final. `GET /status` on port 8080 shows the last verdict.

## When a commit is rejected

`GET /metrics` reports the last verdict as `smallworlds_canary_failed` (1 for a
`failure`, or for an `error` other than "superseded"). `alerting.yaml` turns that into
the warning `CanaryCommitFailed`, which reaches ntfy like every other alert
(`doc/alerting.md`). It is a warning, not critical, on purpose: the quiet check fails a
commit on any critical alert, so a critical one would also fail the fix that follows.

The follower reads which files the commit changed. If it touches a tenant whose
database an update may migrate (`MIGRATING_TENANTS`: immich, gallery, nextcloud,
forgejo, stalwart, keycloak), the status and the alert say **restore, do not revert**.
On Ring 0, Gallery v5.10.0 could not be taken back to v5.8.0 by reverting; only the
pre-update backup leads back across an Immich base change (`doc/tenant-immich.md`,
"Rolling Gallery back"). A restarted pod reads the head's verdict back from GitHub, so
the alert does not resolve itself on a restart.

Then the merge queue (`src/mergequeue.py`) reacts, and stops merging until `main` is
green again:

- The PR the commit came from is labelled `ring0-rejected-<fingerprint>` and gets a
  comment. The fingerprint covers the lines the update adds, so the same update is not
  merged again when Renovate re-opens it; a newer upstream release is tried.
- A queued update that touches no migrating tenant, and is still `main`'s head, is
  **reverted**: a revert commit on `ring0/revert-<sha12>`, a PR, merged at once. Ring 0
  then follows the revert and verifies it like any commit.
- A person's commit gets the same revert PR, **not merged**: it may be a fix-forward.
- A commit that touches a migrating tenant is **not reverted**: the way back is
  restoring `ring0-before-<sha12>` (`doc/tenant-immich.md`, "Rolling Gallery back").
  The queue stays stopped until someone restores or fixes forward.

`GET /status` shows the last action (`merged #41`, `reverted by #57`, …) and why the
queue is waiting.

## Deploying it (Ring 0 only)

The tenant is **not** in the base's root kustomization. The canary overlay adds
`apps/canary-follower.yaml` to its root resources; no other overlay may.

The token is a fine-grained GitHub token on `stephan271/smallworlds` only, with
**Commit statuses**, **Contents**, **Pull requests**, **Issues** (labels) and
**Workflows** (a Renovate PR that pins GitHub Actions changes `.github/workflows/`, and
GitHub refuses to merge that without it): read and write. It lives on Ring 0 only,
which holds no member data.

The checks Job needs the Secret `canary-checks`: join's service account (which mints
the e2e test users' onboarding links, as the Invitation Desk does) and the seeder's
state file with the example members' passkeys (`~/.smallworlds/canary/seed-state.json`).
Re-create it whenever the seeder has run again.

```bash
kubectl create namespace canary-follower --dry-run=client -o yaml | kubectl apply -f -
kubectl -n canary-follower create secret generic canary-follower-github \
  --from-literal=token='<token>' --dry-run=client -o yaml | kubectl apply -f -
kubectl -n canary-follower create secret generic canary-checks \
  --from-literal=clientId="$(kubectl -n join get secret keycloak-secret -o jsonpath='{.data.clientId}' | base64 -d)" \
  --from-literal=client-secret="$(kubectl -n join get secret keycloak-secret -o jsonpath='{.data.client-secret}' | base64 -d)" \
  --from-file=seed-state.json="$HOME/.smallworlds/canary/seed-state.json" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl -n canary-follower rollout restart deployment/canary-follower
```

Without the token the follower still syncs and verifies, only logs its verdicts, and
merges nothing. Switches in `config.yaml`: `PRE_UPDATE_BACKUPS`, `CHECKS_CRONJOB`
(empty: no checks Job, and the status says e2e and data checks were not run),
`MERGE_QUEUE`, `AUTO_REVERT`.

To run the checks by hand against `main`:

```bash
kubectl -n canary-follower create job --from=cronjob/canary-checks by-hand-$(date +%s)
kubectl -n canary-follower logs -f job/by-hand-…
```

## Tests

```bash
python3 -m unittest discover -s infrastructure/kubernetes/tenants/canary-follower/tests
```

Fakes stand in for the Kubernetes API (behaving like the Argo CD controller), GitHub
and the clock; the PR checks run them.
