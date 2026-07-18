# Forgejo Tenant (`infrastructure/kubernetes/tenants/forgejo/*.yaml`)

Forgejo (a Gitea fork) provides Git hosting and code collaboration. Its Kubernetes setup focuses on disabling embedded sub-components (PostgreSQL, Redis) in favor of the cluster's high-availability operators, and automating OIDC.

## Key Infrastructure Integrations

### 1. Externalizing State (`values.yaml`)
By default, the Forgejo helm chart tries to spin up its own PostgreSQL and Redis instances. This is disabled in `values.yaml` (`postgresql.enabled: false`, etc.).
- **Database**: Configured to point to the CloudNativePG instance (`database-rw:5432`). The credentials are dynamically injected via the `additionalConfigFromEnvs` block mapping `FORGEJO__DATABASE__PASSWD` to the `database-app` secret.
- **Cache & Sessions**: Both session state and general caching are routed to the dedicated tenant Redis pod (`redis://redis:6379/0?pool_size=100`) to ensure fast, stateless Forgejo web pods.

### 2. S3 Storage via Garage (`values.yaml`)
Git LFS objects, user avatars, and issue attachments can consume massive amounts of disk space.
- **Storage Redirection**: The `storage` section in `values.yaml` configures Forgejo to use `MINIO` pointing to `garage.garage-system.svc.cluster.local:3900`. 
- **Credentials**: Like the database, S3 credentials (`MINIO_ACCESS_KEY_ID`, etc.) are pulled directly from the `garage-secret` using `additionalConfigFromEnvs`.

### 3. OIDC Automation (`oidc-config-job.yaml`)
Instead of manual admin configuration, SSO is wired automatically.
- **CLI Automation**: The Sync Hook waits for Forgejo to be active, then runs `kubectl exec` to invoke the `forgejo admin auth add-oauth` CLI command inside the pod.
- **Keycloak Integration**: It registers the OIDC provider pointing to Keycloak, using the `CLIENT_SECRET` extracted from the `keycloak-secret` (provisioned by the base job). It also enables `ENABLE_AUTO_REGISTRATION` in `values.yaml` so SSO users instantly get accounts without admin approval.
- **Auto-registration rationale** (`40bd20d`): auto-registration was deliberately enabled so first-time SSO users don't need a pre-created local account, and the OIDC callback host was updated when `auth.` was renamed to `identity.smallworlds.network` (`7bfb924`).
- **In-cluster discovery URL + idempotency guard** (`cf880ab`): the `--auto-discover-url` now points at Keycloak's in-cluster service (`http://keycloak-keycloakx-http.keycloak.svc...`) instead of the public `https://identity.<domain>` endpoint. Keycloak still advertises its public URL in the discovery *document*, but the fetch itself no longer requires the Forgejo pod to trust the public TLS certificate during bootstrap — which is impossible on ephemeral staging and LAN-only installs (self-signed issuer). The job also gained a `forgejo admin auth list | grep` guard so re-runs skip registration instead of relying on `|| true` to swallow duplicate errors.

## Notable changes per file (from git history)

### `kustomization.yaml` — Helm chart source churn
- **Chart repo corrections** (`2641262`, `20a0c06`): the Forgejo Helm chart repository URL was corrected to `codeberg.org` (version 1.1.7) after the original repo reference was wrong — the reason the vendored chart lives under `charts/`.
- **Bumped to v17** (`4231f17`): the pinned Forgejo image was updated to v17 via the automated dependency-update flow.
- **Per-tenant unique `setup-binding`** (`7418e81`): same fix as other tenants — the cluster-scoped RBAC binding is named uniquely to avoid collisions.

### `forgejo-secret-init-job.yaml`
- **Admin username added to creds** (`cb5e62d`): the secret-init job was generating admin credentials without a username, so the bootstrapped admin account was incomplete; the username is now included.
- **Correct `setup-sa` ServiceAccount** (`9cc8d6d`): points the job at the shared init ServiceAccount so its `kubectl` calls carry the right RBAC.

### `cnpg-cluster.yaml`, `redis.yaml`, `values.yaml`
- **Decoupled data services + DRY** (`c100cea`, `697067a`, `b9a864f`): the externalized Postgres (CNPG) and Redis, plus the S3-via-Garage config, were normalized under the modularize/DRY refactors described in §1–§2.
