# Canary Follower

Ring 0's follower (`docs/adr/0060`, `plans-and-walkthroughs/design-update-strategy.md`
§3.4–3.5). It runs on the Canary Cluster only and makes that cluster follow `main`:

1. Every `POLL_SECONDS` it reads `main`'s head commit from GitHub.
2. On a new commit it hard-refreshes and fully syncs `smallworlds-root`, then every
   other Application. A hard refresh is needed because a remote Kustomize base is
   served from cache; a full sync because Sync hooks do not run on self-heal.
3. It waits until every Application is Synced and Healthy (`CONVERGE_TIMEOUT_SECONDS`).
4. It checks that every Application sourced straight from the base runs *that*
   commit. If `main` moved on meanwhile, the result is `error` and the newer commit is
   verified next.
5. **Smoke:** every Ingress host must answer `https://<host>/` below 500, with a
   valid certificate, from inside the cluster.
6. **Quiet:** no `critical` alert in Alertmanager for `QUIET_SECONDS`.
7. It posts the verdict as the commit status `ring0/verified`.

| State | Meaning |
|---|---|
| `pending` | being synced and verified |
| `success` | every step passed (e2e and data checks are not part of it yet, and the description says so) |
| `failure` | Ring 0 did not take the commit: not converged, smoke or a critical alert |
| `error` | the commit could not be judged: `main` moved on, or the follower failed |

The status is also the follower's memory. A restarted pod skips a commit that has a
final state and verifies again one left at `pending`. An `error` is final for that
commit too, so a commit is not re-synced every five minutes; the next commit is
verified as usual. `GET /status` on port 8080 shows the last verdict.

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

Not yet (later phases): the in-cluster Playwright run with full OIDC, the data checks,
and the automatic revert (§3.6). When the revert comes, it must skip the commits the
alert marks "restore".

## Deploying it (Ring 0 only)

The tenant is **not** in the base's root kustomization. The canary overlay adds
`apps/canary-follower.yaml` to its root resources; no other overlay may.

The token is a fine-grained GitHub token on `stephan271/smallworlds` only, with
**Commit statuses: read and write** (the revert will later add Pull requests and
Contents). It lives on Ring 0 only, which holds no member data:

```bash
kubectl create namespace canary-follower --dry-run=client -o yaml | kubectl apply -f -
kubectl -n canary-follower create secret generic canary-follower-github \
  --from-literal=token='<token>'
kubectl -n canary-follower rollout restart deployment/canary-follower
```

Without the Secret the follower still syncs and verifies, and only logs its verdicts.

## Tests

```bash
python3 -m unittest discover -s infrastructure/kubernetes/tenants/canary-follower/tests
```

Fakes stand in for the Kubernetes API (behaving like the Argo CD controller), GitHub
and the clock; the PR checks run them.
