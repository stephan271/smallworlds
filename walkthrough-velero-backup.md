# Walkthrough: Phase 2 - Backup and Restore System

We've successfully implemented Phase 2 of the Hermes AI implementation plan, bringing robust backup and restore capabilities to the SmallWorlds cluster.

## What Was Completed

### 1. Cluster State Backups (Velero)
- **Velero Deployment**: Added an ArgoCD `Application` in `infrastructure/kubernetes/apps/velero.yaml` that deploys Velero via Helm using the official VMware Tanzu chart.
- **Namespace Provisioning**: Added the `velero` namespace to `namespaces.yaml`.
- **Integration**: Velero is configured with the AWS plugin to use the internal Garage S3 endpoint (`http://garage.garage-system.svc.cluster.local:3900`).
- **Schedules**: A daily cluster state backup job runs automatically, excluding `kube-system` resources.

### 2. Application Data Backups
- **Reusable Backup Base**: Created a Kustomize base at `infrastructure/kubernetes/bases/backup-job/` containing an `rclone` `CronJob`.
- **Purpose**: Any future app can include this base in their Kustomization, patch it with their specific `S3_BUCKET` env var, and get automatic, scheduled data synchronization using `rclone`.

### 3. Off-Site Data Replication
- **Replicator App**: Created an ArgoCD Application `backup-replicator.yaml` that targets a new tenant `infrastructure/kubernetes/tenants/backup-replicator/`.
- **Replication Job**: A cluster-wide `CronJob` runs nightly at 4:00 AM, using `rclone` to execute a full S3-to-S3 sync to a secondary Garage instance (e.g., a home server), ensuring off-site redundancy.

### 4. Restore Documentation
- **Procedure Guide**: Created `admin-tools/restore-procedures.md` documenting clear, step-by-step instructions for recovery.
- **Coverage**: Includes procedures for bootstrapping CloudNativePG clusters via `barmanObjectStore` recovery, restoring namespaces with Velero, recovering S3 buckets, and complete disaster recovery.

## Next Steps

With the backup and replication layers in place, we can move towards **Phase 3: Version Update Management**, where we'll set up Renovate Bot and the staging namespace testing pipeline.
