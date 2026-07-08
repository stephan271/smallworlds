# Other Tenants (`infrastructure/kubernetes/tenants/`)

This document covers the infrastructure integrations for the smaller or more specialized tenants in the cluster, including Roundcube, Velero, Backup-Replicator, Excalidraw, and Jitsi.

## Roundcube (`tenants/roundcube/*.yaml`)
Roundcube provides webmail access. It doesn't host mail itself, so it acts strictly as a client to Stalwart and Keycloak.
- **SSO (Keycloak)**: In `roundcube-oauth-config.yaml`, the `oauth-config.inc.php` integrates with the cluster's Keycloak instance. It pulls the client secret from the `keycloak-secret` dynamically.
- **Internal Mail Routing (Stalwart)**: Rather than routing out to the public internet and back in via `mail.smallworlds.network`, the configuration forces Roundcube to talk directly to the internal cluster service `stalwart-mail.stalwart.svc.cluster.local` for IMAP and SMTP.
- **TLS Bypass**: Because it talks to the internal service IP instead of the public domain, `verify_peer` is set to `false` in the PHP configuration to prevent TLS hostname mismatch errors.

- **Dynamic Keycloak client** (`57b040e`, `2dd95d7`): Roundcube was migrated to consume the shared `keycloak-secret` instead of a dedicated `roundcube-oauth-secret`, so it no longer carries its own committed OAuth secret.
- **Mail-domain handling** (`3ba4fb2`, `8233eb7`): the `username_domain` setting was removed in favor of `mail_domain`, fixing how Roundcube maps the OIDC identity to a mailbox address (avoids appending the wrong domain).

## Velero (`tenants/velero/*.yaml`)
Velero is the cluster's disaster recovery solution.
- **S3 Backup Target**: Velero is configured using the AWS S3 plugin, but it's pointed at the internal Garage cluster (`http://garage.garage-system.svc.cluster.local:3900`) and the `velero-backups` bucket.
- **Initialization**: It includes the `velero-garage-init-job` base via Kustomize to automatically provision these S3 credentials during deployment (`cb82b4e`).
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

### Excalidraw — notable changes (from git history)
- **Ingress host `whiteboard.`** (`1edcdf8`): the default host was changed to `whiteboard.smallworlds.network`; the E2E/staging flow adds this subdomain to `/etc/hosts` so tests can resolve it.
- **Dashboard auto-display** (`bd43371`): the `gethomepage.dev/*` annotations were added so Excalidraw appears on the dashboard automatically.
- **Image pinning** (`4bb426f`, `62aa15b`): unlike the other apps, Excalidraw keeps a `latest@sha256` digest reference — the digest makes it immutable while letting Renovate bump it via digest updates.
