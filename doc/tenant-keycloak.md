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
- **Host rename** (`7bfb924`): The public host was renamed from `auth.` to `identity.smallworlds.network`; the redirect regex tracks that hostname.

## Notable changes per file (from git history)

### `garage-init-job.yaml` — Keycloak's own S3 provisioning
- **Runs as a Sync hook with an idempotency guard** (`7cfac4d`): As a plain `Job` it went permanently `OutOfSync` — completed Jobs are immutable, so once its spec changed in git ArgoCD's patch failed forever. It is now a Sync hook (recreated each sync) with a `garage-secret`-existence guard so re-runs are a no-op instead of rotating the backup key on every sync.
- **Forms the Garage layout itself at wave 0** (`45afad8`): fixed a fresh-install deadlock. The Garage cluster layout is normally formed by tenant init jobs at wave 5, but Keycloak's own garage-init runs at wave 0 and needs a ready layout. It used to fail with "Layout not ready", exhaust its backoff, and permanently block the root app from ever reaching wave 5. It now forms the layout itself (idempotent) with a raised `backoffLimit`.
- **Double-secret guard on reinstall** (`dbd7b46`): avoids creating duplicate secrets when the tenant is reinstalled.

### `realm-config-job.yaml` — realm bootstrap
- **`sed`-based offline realm init instead of curl/jq** (`e7b4aec`): the realm JSON is templated with `sed` rather than fetched/patched via the Keycloak API, so bootstrap works offline and doesn't depend on the admin REST endpoint being reachable mid-sync. (This is also where `${env.STALWART_PASSWORD}` is injected — see §2 above.)
- **`keycloak-stalwart-secret` made optional** (`a53b6b7`): the job no longer hard-fails when the mail secret is absent, so Keycloak can bootstrap before/without Stalwart.
- **Micro sync-waves** (`f1d300a`): fine-grained wave ordering was introduced to eliminate race conditions between the realm import and the pods/services it depends on.
- **`invalid_grant` bootstrap fix** (`2133553`): the import logic was corrected so the temporary bootstrap admin user doesn't produce `invalid_grant` failures during first login.
- **`bulk-invite` service account** (`5b3be14`): added the client + `realm-admin` role assignment used by external bulk-invite scripts.
- **WebAuthn passwordless RP ID templated as `${env.IDENTITY_HOST}`** (`568c554`): the relying-party ID in `smallworlds-realm.json` was hardcoded to the base identity host, so passkeys registered on a dev cluster were bound to the wrong origin. It is now a placeholder substituted at realm-import time (same `sed` mechanism as `STALWART_PASSWORD`), with `IDENTITY_HOST` patched per environment so e.g. dev passkeys use `identity.dev.<domain>`.

### `values.yaml` & `kustomization.yaml` — Keycloak 26 upgrade and the `KC_HOSTNAME` saga
- **keycloakx chart 2.6.0 → 7.2.0, Keycloak 26.6.4** (`cf880ab`): the chart and image were upgraded together (image digest-pinned in `values.yaml`, `realm-config-job` image bumped in lockstep). Keycloak 26 **removed `KC_PROXY`**, so the old `KC_PROXY=edge` / `KC_HOSTNAME_STRICT=false` env combo was replaced by the v2 hostname/proxy options: `proxy.enabled: false` in the chart plus `KC_PROXY_HEADERS=xforwarded` + `KC_HTTP_ENABLED=true`, and the hostname is now a full URL (`https://identity.<domain>`) instead of a bare host. The custom action-token SPI jar was rebuilt against the new Keycloak APIs in the same commit (see `infrastructure/keycloak-spi/`).
- **Hostname moved from a `--hostname` CLI flag to a `KC_HOSTNAME` env var** (`33b623a`): as a `command:` argument the hostname was invisible to the overlay's domain-patch generator; as an `extraEnv` entry it can be patched per environment (dev/staging/local domains).
- **…and that env var must be patched *by name*, not by index** (`568c554`): the overlay generator originally targeted the StatefulSet's env positionally (index 0), but the keycloakx chart injects six of its own env vars *before* the `extraEnv` block, so `KC_HOSTNAME` actually renders at index 6. On non-base domains the patch silently overwrote `KC_HTTP_RELATIVE_PATH` instead, Keycloak kept advertising the base `identity.<domain>` in its discovery document, and **every downstream app's OIDC broke** (Immich issuer mismatch, Bulwark/Forgejo redirecting to a non-existent host). The generator now uses a name-keyed strategic-merge patch. Lesson encoded here: never patch chart-rendered env arrays positionally.
- **`KEYCLOAK_ADMIN` over `KC_BOOTSTRAP_ADMIN_USERNAME`** (`f5b23c1`): switched to the admin env var the running image actually honors.
- **Hook annotations to fix StatefulSet drift** (`2776940`): added hook annotations and reconciled the Keycloak StatefulSet so ArgoCD stops reporting a permanent `OutOfSync`.
- **Passkey onboarding + Keycloak SPI** (`f648453`): a custom SPI and passkey onboarding flow were wired in.

### `kustomization.yaml`
- **Per-tenant unique `setup-binding` name** (`7418e81`): the RBAC `ClusterRoleBinding` is named uniquely per tenant to avoid cross-tenant `ClusterRoleBinding` collisions (bindings are cluster-scoped and would otherwise clash).

### `cnpg-cluster.yaml`
- **Secret/S3 isolation** (`68bc5c9`, "Isolate secret and S3 integration of all apps from each other"): part of the cluster-wide refactor giving each app its own isolated secrets and S3 credentials rather than sharing.
