# Other Tenants (`infrastructure/kubernetes/tenants/`)

This document covers the infrastructure integrations for the smaller or more specialized tenants in the cluster, including Roundcube, Velero, Backup-Replicator, Excalidraw, and Jitsi.

## Roundcube (`tenants/roundcube/*.yaml`)
Roundcube provides webmail access. It doesn't host mail itself, so it acts strictly as a client to Stalwart and Keycloak.
- **SSO (Keycloak)**: In `roundcube-oauth-config.yaml`, the `oauth-config.inc.php` integrates with the cluster's Keycloak instance. It pulls the client secret from the `keycloak-secret` dynamically.
- **Internal Mail Routing (Stalwart)**: Rather than routing out to the public internet and back in via `mail.smallworlds.network`, the configuration forces Roundcube to talk directly to the internal cluster service `stalwart-mail.stalwart.svc.cluster.local` for IMAP and SMTP.
- **TLS Bypass**: Because it talks to the internal service IP instead of the public domain, `verify_peer` is set to `false` in the PHP configuration to prevent TLS hostname mismatch errors.

## Velero (`tenants/velero/*.yaml`)
Velero is the cluster's disaster recovery solution.
- **S3 Backup Target**: Velero is configured using the AWS S3 plugin, but it's pointed at the internal Garage cluster (`http://garage.garage-system.svc.cluster.local:3900`) and the `velero-backups` bucket.
- **Initialization**: It includes the `velero-garage-init-job` base via Kustomize to automatically provision these S3 credentials during deployment.
- **Schedule**: It automatically backs up the entire cluster state daily at 2:00 AM.

## Backup-Replicator (`tenants/backup-replicator/*.yaml`)
While Garage and Velero handle local cluster backups, `backup-replicator` ensures offsite disaster recovery.
- **Rclone CronJob**: This runs a daily CronJob at 4:00 AM (after Velero finishes at 2:00 AM and CNPG completes its 2:00 AM backups).
- **Global Sync**: It mounts an external `rclone-config-secret` and runs `rclone sync source: dest:`. This mirrors the entire local Garage S3 cluster to a remote, offsite storage system.

## Jitsi & Excalidraw
These applications are mostly standalone.
- **Dashboard Auto-Discovery**: Both apps use standard Ingress configurations but include annotations like `gethomepage.dev/enabled: "true"` so they automatically appear on the cluster dashboard without any dashboard config changes.
- **Jitsi JVB**: Jitsi exposes a `LoadBalancer` service specifically for its JVB (Jitsi Videobridge) component to handle WebRTC UDP traffic externally.
