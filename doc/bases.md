# Kustomize Bases (`infrastructure/kubernetes/bases/**/*.yaml`)

The `bases/` directory contains reusable Kubernetes resources that are referenced by multiple tenants via Kustomize. Most of these are initialization jobs that run as ArgoCD Sync Hooks to wire up tenant components to the central infrastructure before the tenant's primary application starts.

## Base Components

### 1. Keycloak Client Job (`keycloak-client-job`)
This job automates the registration of an OIDC client for a tenant application into the central Keycloak instance.

**Key Configuration Highlights:**
- **Argocd Hooks**: Runs during `Sync` with `sync-wave: "-1"`. This ensures the client is registered before the main application pods start (which typically wait for or require the OIDC credentials).
- **Environment Variables**: The job expects `REDIRECT_URIS` to be provided by the tenant's kustomize patch. It can optionally use `OIDC_AUDIENCE_MAPPER`.
- **Contract Fulfillment**: It securely fetches the Keycloak admin password, uses `kcadm.sh` to create the client named after the tenant's namespace, and creates a secret named `keycloak-secret`.
- **Why `keycloak-secret`?** As per the project guidelines, any tenant utilizing this job MUST consume the generated credentials from `keycloak-secret` (specifically the `clientId` and `client-secret` keys).

### 2. Garage Init Job (`garage-init-job`)
This job provisions S3 object storage buckets and access keys for a tenant within the cluster's Garage cluster.

**Key Configuration Highlights:**
- **Argocd Hooks**: Runs as a `Sync` hook with `sync-wave: "-2"`. It must run before databases (CNPG) or the Keycloak init jobs.
- **Tenant Bucket**: It generates an access key and creates a bucket named after the tenant namespace. It saves the credentials in a `garage-secret` secret.
- **Dedicated CNPG Bucket**: It explicitly creates a `postgres-backups` bucket and a corresponding key specifically for CloudNativePG database backups. It saves these in `garage-secret-cnpg`. 
- **Why Two Secrets?** Splitting them ensures that the tenant application only gets access to its general-purpose bucket, while the postgres operator gets exclusive access to the backup bucket, maintaining separation of concerns and security.

### 3. Backup Job (`backup-job`)
A generic CronJob template designed to replicate S3 backups (e.g., from Garage) to an external location (like a home server) using `rclone`.

**Key Configuration Highlights:**
- **Schedule**: Defaults to `0 3 * * *` (3 AM daily), though tenants can override this via Kustomize.
- **Environment Variable**: Expects `S3_BUCKET` to be patched in by the tenant.
- **Volume Mounts**: Requires an `rclone-config-secret` containing the Rclone configuration mapping the `source:` (Garage) and `dest:` endpoints.

### 4. Setup RBAC (`setup-rbac`)
Provides the ServiceAccount and RoleBindings required by the initialization jobs.

**Key Configuration Highlights:**
- **Argocd Hooks**: Runs as `PreSync` with `sync-wave: "-2"`. This guarantees the ServiceAccount (`setup-sa`) exists before the jobs in `Sync` wave `-1` or `-2` attempt to use it.
- **Why a separate base?** By extracting the RBAC into a base, we ensure consistent least-privilege execution for all our init jobs across all tenant namespaces.
