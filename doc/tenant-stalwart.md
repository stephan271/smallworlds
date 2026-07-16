# Stalwart Tenant (`infrastructure/kubernetes/tenants/stalwart/*.yaml`)

Stalwart serves as the mail server for the cluster. Its configuration includes several unique integrations with other cluster components, specifically the database (CloudNativePG), storage (Garage), and identity (Keycloak).

## Key Infrastructure Integrations

### 1. Database & Backups (`cnpg-cluster.yaml`)
Stalwart uses a CloudNativePG (CNPG) cluster for its storage backend.
- **S3 Backups**: The `backup` configuration points to `http://garage.garage-system.svc.cluster.local:3900`. 
- **Credentials**: It consumes the `garage-secret-cnpg` secret. This secret is populated automatically by the `garage-init-job` base, which ensures that CNPG has exclusive access to the `postgres-backups` S3 bucket.

### 2. Stalwart Core Configuration (`stalwart-deployment.yaml`)
- **Database Connection**: The `stalwart-config` ConfigMap points to `database-rw.stalwart.svc.cluster.local`, which is the read-write service automatically created by the CNPG operator.
- **Relay Configuration**: The `relay.json` allows relaying from Kubernetes internal subnets (`10.42.0.0/16`, `10.43.0.0/16`). This is crucial because other applications in the cluster (like Keycloak and Nextcloud) use Stalwart as an SMTP relay to send emails without needing full authentication for internal traffic.
- **Certificates**: It mounts TLS certificates from `stalwart-tls`. This secret is expected to be populated by `cert-manager` via the Ingress resource, allowing secure STARTTLS/IMAPS.

### 3. Mail Provisioner (`mail-provisioner-deployment.yaml` & `mail-provisioner-rbac.yaml`)
Because Keycloak doesn't natively push user creation events to external mail servers in a simple way, a custom sidecar/provisioner deployment is used to synchronize users.
- **Direct Database Sync**: The Python script inside `mail-provisioner-code` connects *directly* to the Keycloak PostgreSQL database (`keycloak-db-rw.keycloak.svc.cluster.local`). 
- **Cross-Namespace Secrets**: To authenticate to the Keycloak DB, the script queries the Kubernetes API to read the `keycloak-db-app` secret from the `keycloak` namespace. This is why `mail-provisioner-rbac.yaml` grants cross-namespace secret reading privileges to the `mail-provisioner` ServiceAccount.
- **Stalwart API Integration**: Once it detects a new user in the Keycloak database, it uses the `stalwart-cli` (authenticating via `STALWART_API_KEY`) to provision the mailbox in Stalwart automatically.

## Environment-scoped mail domains (production vs `.dev`)

Only one mail system can be authoritative for a DNS domain, so the dev cluster must not share production's mail domain. The environment extension (`ENV_EXT` key in the `stalwart-dns-secrets` secret, written by `smallworlds-init.sh`, matching the terraform `env_ext` variable) drives an asymmetric split:

- **Production** (`ENV_EXT=""`): mail domain is the apex (`smallworlds.network`), MX/SPF/DKIM/DMARC live at the zone apex, hostname `mail.smallworlds.network` — unchanged historical behavior.
- **Dev cluster** (`ENV_EXT=".dev"`, subdomain syntax; legacy `"-dev"` still accepted): mail domain is `dev.smallworlds.network` (addresses `user@dev.smallworlds.network`), hostname `mail.dev.smallworlds.network` (matching the env-aware PTR terraform sets), and all records are grafted into the shared zone under the `dev` label (`dev` MX, `_dmarc.dev`, `<selector>._domainkey.dev`, …).

Three components must agree on this and all derive it from the same secret: `stalwart-init-job.yaml` (creates the Stalwart domain, sets `defaultHostname`, pushes DNS records), `stalwart-deployment.yaml` (`STALWART_SERVER_HOSTNAME` / EHLO — must match the PTR for deliverability), and `mail-provisioner-deployment.yaml` (waits for the domain by name before syncing accounts).

**Safety guards** in the init job's DNS push (`push_dns.py`): a cluster with non-empty `ENV_EXT` refuses to run against an apex mail domain, refuses a mail domain outside the zone, and skips any record whose name would land outside its own subdomain scope. This exists because a dev cluster previously pushed apex records — overwriting production's DKIM keys with its own and breaking production's outbound mail signing. Ephemeral staging (`test-pr-locally.sh`) is additionally protected by a dummy `HCLOUD_TOKEN`.

The Keycloak OIDC issuer URL in the init job is also env-aware (`identity${ENV_EXT}.<domain>`), so a dev Stalwart validates tokens against the dev Keycloak, not production's.

## Notable changes per file (from git history)

### `stalwart-deployment.yaml` — relay config was hard-won
The relay/allow-relaying expression went through several syntax iterations before landing:
- **Indexed → single-quoted array syntax** (`3541253`, `ef52b00`): experiments finding a form of the relay-networks list that Stalwart's config parser accepts.
- **`MtaStageRcpt allowRelaying` expression** (`66190a9`): settled on expressing "allow relaying from internal subnets" via the recipient-stage `allowRelaying` expression rather than a static network list.
- **Relay ENV var fixed for Stalwart v0.9+** (`f47d9f6`): the relay environment variable name changed in v0.9; this also reverted Keycloak's SMTP back to the *internal* relay (talking to Stalwart in-cluster) rather than routing out and back through the public MX.
- **Port 465 (submissions) exposed** (`b7a8317`): added to both the service and deployment so mail clients can use implicit-TLS submission.

### `stalwart-ingress.yaml` — TLS issuer flip-flop
- **Staging → prod Let's Encrypt** (`20fca09` → `10a65a0`): temporarily switched to `letsencrypt-staging` to dodge rate limits during heavy iteration, then back to `letsencrypt-prod` once stable — a reminder that repeated re-issuance can burn the prod ACME quota.
- **HTTPS admin interface** (`be79736`, later partly reverted by `a00a215`): exposing the Stalwart admin UI over HTTPS.

### `stalwart-init-job.yaml` — OIDC & mail policy
- **`preferred_username` for OIDC** (`8b96a7c`): use the `preferred_username` claim instead of email to avoid a domain mismatch between the OIDC identity and the mail domain.
- **Audience checks relaxed then re-hardened** (`c1dcd9d` "relax audience requirement to make tests pass" → `692cf17` "make mail audience checks safer"): the OIDC audience requirement was loosened to unblock E2E tests, then tightened again in a safe way.
- **DMARC set to `p=quarantine`** (`1d130db`): stricter DMARC policy for outbound mail deliverability/anti-spoofing.

### `stalwart-secret-init-job.yaml`
- **Correct `serviceAccountName: setup-sa`** (`9cc8d6d`): the secret-init jobs were pointed at the shared `setup-sa` ServiceAccount so their `kubectl` calls have the right RBAC (ties into the setup-rbac base).

### `mail-provisioner-*.yaml`
- **Dynamic Keycloak DB sync** (`147a449`): switched from a static approach to pulling users *dynamically* from the Keycloak database.
- **`emailAddress` parsing fix** (`9d1dd8b`): corrected the account-parsing logic for the `emailAddress` field.
- **RBAC & authentication fix** (`2fc733d`): fixed the cross-namespace RBAC and the provisioner's authentication (the origin of the `mail-provisioner-rbac.yaml` cross-namespace secret-read grant described above).
