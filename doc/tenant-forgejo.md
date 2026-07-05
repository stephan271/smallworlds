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
