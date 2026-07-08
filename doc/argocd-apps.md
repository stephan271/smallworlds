# ArgoCD Applications (`infrastructure/kubernetes/apps/*.yaml`)

The YAML files in the `apps/` directory define the ArgoCD `Application` resources that map to our cluster components. These are the top-level definitions that tell ArgoCD where to find the manifests for each component (usually pointing to `infrastructure/kubernetes/tenants/`).

The most critical and non-boilerplate configuration in these files is the **Sync Wave** annotation (`argocd.argoproj.io/sync-wave`). 

## Sync Waves Explanation

Sync waves dictate the order in which ArgoCD deploys the applications. A lower number means the application is deployed earlier. This is crucial for infrastructure dependencies (e.g., a database operator must be running before a tenant tries to create a database cluster).

> **Important — the waves were flattened from six tiers to three** (`8496e89`). The doc below reflects the *current* four numeric waves (`-10`, `-5`, `0`, `1`). The history and rationale are in the next section.

Here is the current sync-wave rationale for the cluster:

### Wave -10 (Foundational Infrastructure)
* **`cert-manager.yaml`**: Required for issuing TLS certificates for all other ingress routes.
* **`cloudnative-pg.yaml`**: The CloudNativePG operator. Required before any tenant can declare a PostgreSQL `Cluster`.
* **`persistent-storage.yaml`**: Storage classes and basic volume provisioning. Required before any PVCs can be bound.
* **`traefik.yaml`**: The ingress controller. Required for routing external traffic.

### Wave -5 (Core Object Storage)
* **`garage.yaml`**: S3-compatible object storage. Deployed early because databases (CNPG) and backup systems (Velero) rely on it for storing backups/blobs.

### Wave 0 (Everything that does *not* need Keycloak)
Identity, management, observability, and cluster utilities all deploy together in parallel:
* **`keycloak.yaml`**: Central Identity Provider (OIDC). Must be up before end-user apps register their OIDC clients.
* **`dashboard.yaml`**, **`kube-prometheus-stack.yaml`**, **`loki-stack.yaml`**: homepage, monitoring, logging.
* **`velero.yaml`**: backup controller (only depends on Garage from wave -5, not on Keycloak).
* **`hermes.yaml`**, **`trivy-operator.yaml`**, **`backup-replicator.yaml`**: the AI-driven auto-remediation agent, security scanning, and backup utilities.

### Wave 1 (End-User Tenant Applications)
* **`nextcloud.yaml`**, **`collabora.yaml`**, **`forgejo.yaml`**, **`immich.yaml`**, **`jitsi.yaml`**, **`roundcube.yaml`**, **`excalidraw.yaml`**, **`stalwart.yaml`**
* These are the final workloads. They rely on the foundational layers (CNPG for DBs, Garage for S3, Keycloak for SSO, Traefik for Ingress) being operational. **Stalwart stays in this wave** despite being "infrastructure" because it depends on Keycloak's OIDC/directory integration.

## Why the waves were flattened (`8496e89`)

The old scheme serialized six tiers: `keycloak(0) → monitoring(1) → velero(2) → auto-remediator(3) → tenants(5)`. ArgoCD waits for each wave to be fully **Healthy** before starting the next, so this chain was **stricter than the real dependency graph** — roughly **5 minutes of serialization per fresh install**, and it widened the blast radius: one app's transient failure in an early wave stalled everything behind it.

The new layout collapses this to: infra CRDs/storage (`-10`) → Garage (`-5`) → *everything that doesn't need Keycloak* (`0`) → *all end-user tenants* (`1`). Intra-wave ordering that the old tiers used to enforce is instead handled by:
- the init jobs' **poll-and-retry loops** (they wait for their dependencies to become ready), and
- the apps' **sync retry policies**.

## Self-healing bootstrap (`argocd-root-app.yaml`, `5ddec3b`)

A fresh install used to lose hours to ArgoCD's *park-on-failure* behavior: the root app had only the default 5 sync retries and never re-attempted the same Git revision, so a single transient wave failure silently froze the rollout until someone intervened.
- **Retry policy**: the root app now uses a retry limit of **20 with exponential backoff capped at 5m**.
- **Rollout watchdog**: `smallworlds-init.sh` watches all Applications until Healthy after the credentials banner, automatically re-kicking any whose sync ended `Failed`/`Error`, bounded at 40 minutes (skipped gracefully when `kubectl` is absent).

## Namespaces (`namespaces.yaml`)
Namespaces are declared centrally and were extended as tenants were added (e.g. `01dedef` added the excalidraw and jitsi namespaces; the Hermes/observability and backup phases added theirs — `dfa21bf`, `db13931`). `10eb3b6` also extended the shared `tenant-setup-role` with `configmaps` access so Immich can read the `smallworlds-global-config` ConfigMap.
