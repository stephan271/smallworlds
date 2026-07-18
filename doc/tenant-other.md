# Other Tenants (`infrastructure/kubernetes/tenants/`)

This document covers the infrastructure integrations for the smaller or more specialized tenants in the cluster, including Bulwark, Velero, Backup-Replicator, Excalidraw, and Jitsi.

## Bulwark (`tenants/bulwark/*.yaml`)
Bulwark (`ghcr.io/bulwarkmail/webmail`) provides webmail access at `webmail.<domain>`. It doesn't host mail itself — it's a JMAP client to Stalwart and an OIDC client to Keycloak, configured entirely through environment variables in `bulwark-deployment.yaml`.
- **SSO (Keycloak)**: runs OIDC-only (`OAUTH_ONLY=true`) against the cluster realm, consuming the shared `keycloak-secret` (`clientId`/`client-secret`) per the project-wide contract — no dedicated OAuth secret.
- **Mail access (Stalwart)**: talks JMAP to `https://mail.<domain>` (`JMAP_SERVER_URL`), with `STALWART_FEATURES=true` for Stalwart-specific capabilities. The in-cluster CoreDNS override maps that hostname to the node, so the traffic never leaves the cluster.
- **Session secret**: `session-secret-job.yaml` is an init Job that generates the random `session-secret` Secret consumed via `SESSION_SECRET`.
- **State**: a small PVC (`bulwark-data`) persists admin data.

### Bulwark — notable changes (from git history)
- **v1.4.8 pinned, then found broken, bumped to v1.5.0** (`808b1d9`, `27dee50`): the digest-pinned v1.4.8 image shipped a defective build — its pages identify as v1.4.7 and their Next.js server-action IDs don't exist in the running server ("Failed to find Server Action"), so the login page's automatic SSO launch 404'd and every user was stuck on "Sign in with SSO". v1.5.0 fixed it (digest-pinned as usual).
- **`AUTO_SSO_ENABLED=true`** (`8bbe1a3`): `OAUTH_ONLY=true` only hides the password form; the automatic redirect into Keycloak is a *separate* switch that defaults to false. Without it, users (and the e2e suite) landed on a "Sign in with SSO" interstitial instead of going straight to Keycloak.
- **Redirect URI widened to a wildcard** (`855fa5e`): v1.5.0 calls back at `/{locale}/auth/callback` instead of the fixed `/api/auth/callback`. Keycloak only supports a *trailing* wildcard in redirect URIs, so the registered URI is now `https://webmail.<domain>/*` (the same pattern Jitsi uses), both in the base tenant's `REDIRECT_URIS` patch and the overlay generator.

## Velero (`tenants/velero/*.yaml`)
Velero is the cluster's disaster recovery solution.
- **S3 Backup Target**: Velero is configured using the AWS S3 plugin, but it's pointed at the internal Garage cluster (`http://garage.garage-system.svc.cluster.local:3900`) and the `velero-backups` bucket.
- **Initialization**: It includes the `velero-garage-init-job` base via Kustomize to automatically provision these S3 credentials during deployment (`cb82b4e`). The job was later moved from a `PreSync` to a `Sync` hook (`7755200`) — PreSync hooks run before *any* of the sync's plain resources exist, including the `setup-rbac` ServiceAccount the job runs as (see `doc/bases.md`).
- **Schedule**: It automatically backs up the entire cluster state daily at 2:00 AM.
- **CRD handling for fresh clusters** (`173b2e2`, `901d1f6`): the Helm generator was configured to render/upgrade Velero's CRDs (`includeCRDs`) and skip the dry-run against missing resources, so a brand-new cluster can sync Velero without the CRDs pre-existing. The chart was later bumped to v12 (`f4f6127`).

## Backup-Replicator (`tenants/backup-replicator/*.yaml`)
While Garage and Velero handle local cluster backups, `backup-replicator` ensures offsite disaster recovery.
- **Rclone CronJob**: This runs a daily CronJob at 4:00 AM (after Velero finishes at 2:00 AM and CNPG completes its 2:00 AM backups).
- **Global Sync**: It mounts an external `rclone-config-secret` and runs `rclone sync source: dest:`. This mirrors the entire local Garage S3 cluster to a remote, offsite storage system.
- **Origin** (`db13931`, "Add Phase 2 and 3: backup and version management"): introduced together with the offsite-backup phase, using the same `backup-job` base CronJob template documented in `bases.md`.

## Jitsi & Excalidraw
These applications are mostly standalone.
- **Dashboard Auto-Discovery**: Both apps use standard Ingress configurations but include annotations like `gethomepage.dev/enabled: "true"` so they automatically appear on the cluster dashboard without any dashboard config changes.
- **Jitsi JVB**: Jitsi exposes a `LoadBalancer` service specifically for its JVB (Jitsi Videobridge) component to handle WebRTC UDP traffic externally (`d523b41`).

### Jitsi — notable changes (from git history)
- **JWT secret via idempotent init job, not `secretGenerator`** (`42e3b6a`): a Kustomize `secretGenerator`'s hash-suffixed name was resolved inconsistently across Kustomize versions (pods referenced the *unhashed* name), and using a literal committed a static secret to git. The secret is now created by an idempotent `jitsi-secret-init-job.yaml` instead.
- **Missing `setup-rbac` base added** (`e628268`): the init job needs the shared `setup-sa` ServiceAccount, which was absent from the kustomization.
- **`nodeAffinity` override in staging** (`e33292e`): overridden so Jitsi's pod schedules onto the node where its Garage-backed PV can bind (single-node staging PV binding constraint).
- **Chart repo/version fix** (`c7e2bae`) and **Homepage pod-selector fix** (`560b0ae`).
- **Overlay domain patches rewritten** (`f93b314`, in `admin-tools/generate_domain_patches.py`): the generated overlay patches targeted a `jitsi-jitsi-meet-jwt-app` Deployment that no longer exists and appended `env` to the web container, which only uses `envFrom` — the JSON patch failed, `kustomize build` errored, and the jitsi Application deployed **zero resources** (`meet.<domain>` 404). The generator now patches the `-common` ConfigMap (`PUBLIC_URL`/`TOKEN_AUTH_URL`) and the `jitsi-oidc-adapter` sidecar env via strategic merge. A reminder that a broken overlay patch fails the whole app's render, not just the patched field.

### Excalidraw — notable changes (from git history)
- **Ingress host `whiteboard.`** (`1edcdf8`): the default host was changed to `whiteboard.smallworlds.network`; the E2E/staging flow adds this subdomain to `/etc/hosts` so tests can resolve it.
- **Dashboard auto-display** (`bd43371`): the `gethomepage.dev/*` annotations were added so Excalidraw appears on the dashboard automatically.
- **Image pinning** (`4bb426f`, `62aa15b`): unlike the other apps, Excalidraw keeps a `latest@sha256` digest reference — the digest makes it immutable while letting Renovate bump it via digest updates.
