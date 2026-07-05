# ArgoCD Applications (`infrastructure/kubernetes/apps/*.yaml`)

The YAML files in the `apps/` directory define the ArgoCD `Application` resources that map to our cluster components. These are the top-level definitions that tell ArgoCD where to find the manifests for each component (usually pointing to `infrastructure/kubernetes/tenants/`).

The most critical and non-boilerplate configuration in these files is the **Sync Wave** annotation (`argocd.argoproj.io/sync-wave`). 

## Sync Waves Explanation

Sync waves dictate the order in which ArgoCD deploys the applications. A lower number means the application is deployed earlier. This is crucial for infrastructure dependencies (e.g., a database operator must be running before a tenant tries to create a database cluster).

Here is the sync-wave rationale for the cluster:

### Wave -10 (Foundational Infrastructure)
* **`cert-manager.yaml`**: Required for issuing TLS certificates for all other ingress routes.
* **`cloudnative-pg.yaml`**: The CloudNativePG operator. Required before any tenant can declare a PostgreSQL `Cluster`.
* **`persistent-storage.yaml`**: Storage classes and basic volume provisioning. Required before any PVCs can be bound.
* **`traefik.yaml`**: The ingress controller. Required for routing external traffic.

### Wave -5 (Core Services)
* **`garage.yaml`**: S3-compatible object storage. Deployed early because databases (CNPG) and backup systems (Velero) rely on it for storing backups/blobs.

### Wave 0 (Identity & Management)
* **`keycloak.yaml`**: Central Identity Provider (OIDC). Deployed before end-user apps so that those apps can successfully register their OIDC clients during initialization.
* **`dashboard.yaml`**: The cluster homepage/dashboard.

### Wave 1 (Observability)
* **`kube-prometheus-stack.yaml`** & **`loki-stack.yaml`**: Monitoring and logging stacks. Deployed early enough to capture metrics/logs from the user applications as they spin up.

### Wave 2 & 3 (Cluster Utilities)
* **`velero.yaml`** (Wave 2): Backup controller. Depends on Garage (Wave -5) being available.
* **`auto-remediator.yaml`** (Wave 3): Automated remediation tasks for cluster health.

### Wave 5 (End-User Tenant Applications)
* **`excalidraw.yaml`**, **`forgejo.yaml`**, **`hermes.yaml`**, **`immich.yaml`**, **`jitsi.yaml`**, **`nextcloud.yaml`**, **`roundcube.yaml`**, **`stalwart.yaml`**, **`backup-replicator.yaml`**, **`trivy-operator.yaml`**
* These are the final workloads. They rely on the foundational layers (CNPG for DBs, Garage for S3, Keycloak for SSO, Traefik for Ingress) being fully operational.
