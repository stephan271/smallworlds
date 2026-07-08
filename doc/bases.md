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

**Notable changes (from git history):**
- **`client-secret` key naming** (`3fa4b1d`): The generated secret exposes the credential under the `client-secret` key. This was a deliberate correction — tenants standardize on consuming `keycloak-secret`'s `client-secret`, so all consumers can share one contract instead of each guessing the key name.
- **Post-logout redirect URIs moved into client *attributes*** (`901d1f6`): Originally a top-level `postLogoutRedirectUris` field was used. Keycloak 24 silently ignored it, but Keycloak 25 (pulled in by the keycloakx 2.6.0 chart bump) *rejects* it as invalid. The setting now lives in the client's `attributes` map, which both versions accept. This unblocked the weekly automated dependency-update runs.
- **Audience mapper hardening** (`692cf17`): The optional `OIDC_AUDIENCE_MAPPER` path was made safer so mail-related audience checks don't fail when the mapper is absent.
- **Image pinning** (`62aa15b`, `4bb426f`): The job image was moved off `bitnami/kubectl:latest` to a pinned `alpine/k8s:1.36.2`. Bitnami purged versioned tags and left the image effectively unmaintained; `alpine/k8s` is maintained, bundles `bash` + `kubectl` + the tools the wait-loop scripts need, and its tag tracks the cluster's Kubernetes version.

### 2. Garage Init Job (`garage-init-job`)
This job provisions S3 object storage buckets and access keys for a tenant within the cluster's Garage cluster.

**Key Configuration Highlights:**
- **Argocd Hooks**: Runs as a `Sync` hook with `sync-wave: "-2"`. It must run before databases (CNPG) or the Keycloak init jobs.
- **Tenant Bucket**: It generates an access key and creates a bucket named after the tenant namespace. It saves the credentials in a `garage-secret` secret.
- **Dedicated CNPG Bucket**: It explicitly creates a `postgres-backups` bucket and a corresponding key specifically for CloudNativePG database backups. It saves these in `garage-secret-cnpg`. 
- **Why Two Secrets?** Splitting them ensures that the tenant application only gets access to its general-purpose bucket, while the postgres operator gets exclusive access to the backup bucket, maintaining separation of concerns and security.

**Notable changes (from git history):**
- **DRY refactor** (`b9a864f`): This job (and the other init jobs) was extracted/normalized under "Apply DRY rules on yaml files" so the same job definition is reused across tenants via Kustomize rather than copy-pasted per namespace.
- **Image pinning** (`62aa15b`, `bf7535f`, `4bb426f`): Same migration as the Keycloak job — off floating `bitnami/kubectl:latest` (unmaintained after Bitnami purged tags) onto a pinned, maintained `alpine/k8s` image for reproducible init runs.

### 3. Backup Job (`backup-job`)
A generic CronJob template designed to replicate S3 backups (e.g., from Garage) to an external location (like a home server) using `rclone`.

**Key Configuration Highlights:**
- **Schedule**: Defaults to `0 3 * * *` (3 AM daily), though tenants can override this via Kustomize.
- **Environment Variable**: Expects `S3_BUCKET` to be patched in by the tenant.
- **Volume Mounts**: Requires an `rclone-config-secret` containing the Rclone configuration mapping the `source:` (Garage) and `dest:` endpoints.

### 3b. Velero Garage Init Job (`velero-garage-init-job`)
A specialized variant of the Garage init job dedicated to Velero.

**Key Configuration Highlights:**
- **Why a separate job?** (`cb82b4e`, "Automate velero s3 credentials using garage-init-job"): Velero's backup target lives in a `velero-backups` bucket that is *cluster-scoped* rather than tenant-scoped, so it needs its own provisioning step rather than reusing the per-tenant `garage-init-job`. This job creates that bucket + access key and writes the credentials Velero's `BackupStorageLocation` consumes, so no S3 keys are hand-managed.
- **Runs as a `Sync` hook** ahead of Velero itself, and shares the same `alpine/k8s` image pinning history (`62aa15b`, `bf7535f`, `4bb426f`) as the other init jobs.

### 4. Setup RBAC (`setup-rbac`)
Provides the ServiceAccount and RoleBindings required by the initialization jobs.

**Key Configuration Highlights:**
- **Ordering**: The ServiceAccount (`setup-sa`) and its binding must exist before any init job in wave `-2`/`-1` attempts to use it. It is currently ordered by **sync-wave alone at wave `-3`** — strictly ahead of the wave `-2` hook jobs.
- **Why a separate base?** By extracting the RBAC into a base, we ensure consistent least-privilege execution for all our init jobs across all tenant namespaces.

**Notable changes (from git history) — a tricky ordering saga:**
- It first gained a `sync-wave` (`eba36b0`), then a `PreSync` hook (`1b7457a`) to force it ahead of the init jobs.
- **The PreSync hook was then *removed*** (`350f46a`): as a `PreSync` hook with the default `BeforeHookCreation` deletion policy, **every ArgoCD sync retry DELETED the ServiceAccount** while the previous attempt's init jobs were still running. This produced `serviceaccount setup-sa not found` and silently broke `kubectl` inside the keycloak-client wait loop. The SA/binding are now plain resources ordered purely by sync-wave, so retries never yank them out from under running jobs.
- **Moved to wave `-3`** (`a71150a`): bumped one wave earlier because Velero applied its wave `-2` init hook before the *same-wave* ServiceAccount existed. Wave `-3` guarantees the SA strictly precedes all wave `-2` consumers.

### 5. Staging Smoke Test (`staging-test/smoke-test-job`)
A generic post-deploy smoke test template used in the staging pipeline (`db13931`, "Add Phase 2 and 3: backup and version management").

**Key Configuration Highlights:**
- **Argocd Hooks**: Runs as a `Sync` hook with `hook-delete-policy: HookSucceeded`, so a passing run cleans itself up and only a *failing* run lingers for inspection.
- **What it does**: Polls a tenant's `/healthz` endpoint (via the in-cluster service DNS `${SERVICE_NAME}.${NAMESPACE}.svc.cluster.local`) up to 30 times at 10s intervals, treating `200/301/302` as success. `SERVICE_NAME` defaults to the placeholder `REPLACE_ME_IN_PATCH` and is overridden per tenant via a Kustomize patch.
- **Why?** Gives the automated weekly-update / staging runs a deterministic "did the app actually come up?" gate rather than trusting ArgoCD's resource health alone. The `curlimages/curl` image is pinned by digest for reproducible test runs.
