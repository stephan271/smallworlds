# Keycloak Tenant (`infrastructure/kubernetes/tenants/keycloak/*.yaml`)

Keycloak serves as the central OIDC Identity Provider for the entire cluster. Its configuration focuses heavily on automating realm setup and integrating with other cluster services (like databases and mail).

## Key Infrastructure Integrations

### 1. Database & Backups (`cnpg-cluster.yaml` & `garage-init-job.yaml`)
Like other stateful tenants, Keycloak uses a dedicated CloudNativePG cluster.
- **Backups**: The `garage-init-job.yaml` base is used here to provision the S3 credentials in Garage. The `cnpg-cluster.yaml` is configured to stream WAL logs and scheduled backups to the `postgres-backups` bucket in Garage.
- **Connection**: In `values.yaml`, Keycloak is configured to connect to this database via `KC_DB_URL_HOST=keycloak-db-rw`.

### 2. Stalwart SMTP Integration (`values.yaml` & `realm-config-job.yaml`)
Keycloak sends emails (for password resets, email verification, etc.) using the cluster's Stalwart mail server.
- **Environment Variables**: In `values.yaml`, SMTP settings are passed (e.g. `KC_SMTP_HOST="stalwart-mail.stalwart.svc.cluster.local"`). 
- **Realm Injection**: Interestingly, the actual SMTP password is dynamically injected into the realm JSON during the `realm-config-job`. The job uses `sed` to replace `${env.STALWART_PASSWORD}` with the real password fetched from the `keycloak-stalwart-secret` before using `kcadm.sh` to create the realm. This avoids committing the plaintext mail password in the realm JSON file.

### 3. Realm Initialization (`realm-config-job.yaml`)
Instead of manual configuration, Keycloak's state is declarative.
- **Sync Hook**: Runs as an ArgoCD Sync hook (`sync-wave: "1"`), meaning it waits for the Keycloak pods (wave `0`) to spin up.
- **kcadm.sh Scripting**: It loops until the Keycloak HTTP endpoint is ready, logs in as the admin, and imports the `smallworlds` realm. 
- **Service Account Creation**: It also provisions a `bulk-invite` service account client and assigns it the `realm-admin` role, which allows external scripts to bulk invite users to the cluster.

### 4. Root Redirect (`keycloak-redirect.yaml`)
Keycloak's default root URL (`/`) goes to an admin welcome page.
- **Traefik Middleware**: This file defines a Traefik `RedirectRegex` middleware that catches root hits to `identity.smallworlds.network` and redirects users immediately to the `smallworlds` account console (`/realms/smallworlds/account/`). This creates a much better user experience since users don't see the Keycloak admin landing page.
