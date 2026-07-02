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

## Phase 3: Version Update Management

We have successfully implemented the foundations for automated version management:

### 1. Renovate Bot Setup
- **Configuration**: Created `renovate.json` in the repository root.
- **Scope**: Renovate is configured to monitor our ArgoCD `Application` files (for Helm chart version bumps), Terraform provider versions (`.tf` files), and standard Kubernetes manifests.
- **Workflow**: It will now automatically detect new versions of the software we use (like Nextcloud, Immich, Keycloak) and create Pull Requests.

### 2. Staging Namespace Testing
- **Kustomize Base**: Added `infrastructure/kubernetes/bases/staging-test/` to act as a pre-flight test environment.
- **Smoke Testing**: Included an automated `smoke-test-job.yaml` that can be patched by the Hermes agent during a test run. The job waits for the newly updated service to start and perform an HTTP health check. This ensures that updates don't break functionality before they are deployed to production.

## Phase 4: Hybrid Auto-Remediation System

We implemented a robust, cost-effective two-tiered system for infrastructure management:

### 1. Tier 1: Deterministic Remediator (Robusta)
- **Deployment**: Added `infrastructure/kubernetes/apps/auto-remediator.yaml` which deploys Robusta to the cluster.
- **Function**: Automatically handles "known" and "standard" cases. For example, if it detects a `KubePodCrashLooping` alert that is caused by an OOMKill, it uses the `increase_memory_limit` action to automatically patch the pod with 20% more memory, without requiring any AI tokens or GPU compute.

### 2. Tier 2: The Hermes AI Agent
- **Deployment**: Configured the Hermes agent inside `infrastructure/kubernetes/tenants/hermes/`.
- **System Prompt**: Created a `system-prompt.txt` ConfigMap that instructs the AI on its constraints (e.g., *never* delete user data, always generate pull requests instead of applying directly).
- **Security Posture**: Provisioned a scoped ServiceAccount (`hermes-rbac.yaml`) granting the agent read-only access to cluster state (to debug issues) and write access only to the dashboard's `status.json` ConfigMap.
- **Integration**: Plumbed the required secrets (`hermes-sa-secret.yaml`) and config structure (`hermes-config.yaml`) so the Python agent loop can securely talk to GitHub to propose complex fixes that Tier 1 cannot resolve.

## Phase 5: Status Page and User Communication

To ensure Hermes is completely transparent with the community, we've set up its communication framework:

### 1. Dashboard Status ConfigMap
- **JSON Datastore**: Created `infrastructure/kubernetes/tenants/dashboard/status-data.yaml`, which provisions the raw `status.json` structure (`system_status`, `incidents`, `maintenance`).
- **Integration**: The front-end dashboard natively reads this JSON structure, and the Hermes agent has RBAC permissions to dynamically patch it in real-time when an incident occurs or is resolved.

### 2. Notification Channels and Templates
- **Channel Configuration**: Created `infrastructure/kubernetes/tenants/hermes/notification-channels.yaml` to define how Hermes routes messages.
- **Email Gateway**: Plumbed the configuration so Hermes can securely send emails through the existing Stalwart SMTP server.
- **Message Templates**: Pre-configured standard templates for `incident-opened`, `incident-resolved`, `maintenance-scheduled`, and `approval-required`, ensuring clear, professional communication from the AI to the human administrators.

## Next Steps

With the Hermes agent now possessing "a voice", we are ready to proceed to **Phase 6: Security Hardening**, where we'll set up automated CVE scanning and certificate lifecycle management!
