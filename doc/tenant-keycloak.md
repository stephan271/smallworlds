# Keycloak Tenant (`infrastructure/kubernetes/tenants/keycloak/*.yaml`)

Keycloak serves as the central OIDC Identity Provider for the entire cluster. Its configuration focuses heavily on automating realm setup and integrating with other cluster services (like databases and mail).

## Key Infrastructure Integrations

### 1. Database & Backups (`cnpg-cluster.yaml` & `garage-init-job.yaml`)
Like other stateful tenants, Keycloak uses a dedicated CloudNativePG cluster.
- **Backups**: The `garage-init-job.yaml` base is used here to provision the S3 credentials in Garage. The `cnpg-cluster.yaml` is configured to stream WAL logs and scheduled backups to the `postgres-backups` bucket in Garage.
- **Connection**: In `values.yaml`, Keycloak is configured to connect to this database via `KC_DB_URL_HOST=keycloak-db-rw`.

### 2. Stalwart SMTP Integration (`values.yaml` & `realm-config-job.yaml`)
Keycloak can send email through the cluster's Stalwart mail server. Note that in the default configuration it has very little to send: the realm is passwordless, so there are no password-reset mails (§5), invitations are delivered out of band by `admin-tools/bulk-invite.py`, and `verifyEmail` is only set in the `self-registration` onboarding mode. See `doc/mail.md` — mail is an opt-in capability (`docs/adr/0049`), and this SMTP block is inert in a cluster without a mail server.
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

### 5. Passkey authentication (`smallworlds-realm.json`)

**The realm is passkey-*capable*, not passkey-only.** This is worth stating plainly because three descriptions of it disagree, and only the first is what a fresh cluster runs:

- **Bound and active** (`"browserFlow": "browser"`): the `forms` subflow holds `webauthn-authenticator-passwordless` and `auth-username-password-form`, both **ALTERNATIVE**. Either credential is accepted.
- **Defined but unbound**: a `browser-with-passkey` top-level flow whose `passkey-or-password` subflow does username-first, then passkey / password / **recovery code**. Its `browser-with-passkey forms` child is `DISABLED`. Nothing binds it.
- **Documented but nonexistent**: `admin-tools/keycloak-config-instructions.md` §3 tells the Operator to hand-build a `passkey-only-browser` flow by duplicating `browser` and deleting the password form. That flow is not in the realm JSON and matches neither of the above.

Members are nonetheless passwordless **in practice**, for two reasons that are worth keeping true deliberately rather than by accident: `admin-tools/bulk-invite.py` sets no password credential when it creates a member, so the password form has nothing to match against; and `resetPasswordAllowed` is `false`, so there is no "Forgot password?" link and Keycloak never sends a reset mail. That — not the absence of a password form — is why account recovery needs no mail (`doc/mail.md`, `docs/adr/0049`). The password path is latent, not removed: give a user a password and it will work.

**Recovery codes**, the mail-free and Operator-free way back into an account. The `forms` subflow now offers the passkey first and `passkey-or-password` as its second alternative, which Keycloak surfaces as *Try Another Way*:

```
forms                                  ALTERNATIVE
  webauthn-authenticator-passwordless    ALTERNATIVE   usernameless, resident key
  passkey-or-password                    ALTERNATIVE
    auth-username-form                     REQUIRED    identify the user first
    must-authenticate                      REQUIRED
      webauthn-authenticator-passwordless    ALTERNATIVE
      auth-password-form                     ALTERNATIVE
      recovery-auth-code-form                ALTERNATIVE
```

The default login is unchanged — members still get the usernameless passkey prompt. `must-authenticate` and `passkey-or-password` already existed in the realm and were simply never reachable; the only edit was replacing `auth-username-password-form` in `forms` with the `passkey-or-password` subflow. `admin-tools/bulk-invite.py` adds `recovery-auth-code-register` to the invitation's required actions, so members take their codes down during onboarding, after the passkey (Keycloak orders required actions by realm priority — passkey 60, recovery codes 110 — not by the order the script sends them).

Alternatives that a member has not configured are not offered, so this degrades correctly: an invited member with no password sees passkey and recovery-code options only.

Three things to know:
- **It is untested against a running Keycloak.** The flow structure is validated and both invitation paths send the required action, but no login has been performed. `recovery-auth-code-form` is documented by Keycloak as a second-factor authenticator, and here it acts as a sole factor after `auth-username-form`. Verify on staging (`admin-tools/test-pr-locally.sh`) before relying on it.
- **Only fresh realms get it.** `realm-config-job.yaml` runs `kcadm create realms` and tolerates "Realm may already exist", so it never updates an imported realm. An existing cluster keeps its old flow until the realm is recreated or the binding is applied by hand.
- **Existing members have no codes.** `defaultAction` stays `false`, so the action applies only where it is requested. Members enrolled before this change need `recovery-auth-code-register` added to their account, or a re-invitation.

After logging in with a recovery code, a member should register a fresh passkey; nothing enforces that automatically.

The passwordless WebAuthn policy — the `webAuthnPolicyPasswordless*` keys, *not* the `webAuthnPolicy*` ones, which govern the two-factor `webauthn-authenticator` that no flow here uses:

| Setting | Value | Consequence |
|---|---|---|
| `RequireResidentKey` | `Yes` | Discoverable credentials, so login needs no username first. Hardware keys have a finite number of resident slots (a YubiKey 5 holds ~25); platform authenticators are effectively unlimited. |
| `UserVerificationRequirement` | `required` | Biometric or PIN at every login; a tapped-but-unverified key is rejected. |
| `AuthenticatorAttachment` | `not specified` | Both platform authenticators (phone, laptop) and roaming ones (security keys) are accepted. |
| `AttestationConveyancePreference` | `none`, `AcceptableAaguids` empty | **Any** authenticator is accepted, including third-party password managers. This is deliberate: an AAGUID allowlist would silently lock out members using Bitwarden, Proton Pass or 1Password. |

**Where members' credentials actually live.** Registering on a phone produces a *synced* passkey by default — iCloud Keychain on iOS, Google Password Manager on Android — so the private key is escrowed end-to-end-encrypted with Apple or Google and follows the member to their other devices in that ecosystem. That sync is free and automatic, and it is also a dependency on a US provider that this project otherwise avoids. The alternatives are device-bound credentials (hardware keys, which never sync by design) or a third-party manager; the latter requires **Android 14+ or iOS 17+** to act as a credential provider, so a community with older phones cannot rely on it uniformly. Vaultwarden is the self-hostable option that fits this stack, but it stores passkeys rather than fully replacing the platform provider, and compatibility with Bitwarden clients has had recent gaps — verify before recommending it to non-technical members.

**Passkeys are bound to the RP ID, which is the identity hostname.** `webAuthnPolicyPasswordlessRpId` is templated as `${env.IDENTITY_HOST}` and substituted per environment at realm-import time (`568c554`, below). Two consequences that are easy to discover the hard way:
- **Renaming or moving the identity host invalidates every passkey in the community.** They are cryptographically scoped to the RP ID; a credential registered against `identity.<domain>` is unusable at any other host. Recovery is re-inviting every member. Treat the identity hostname as permanent once the first member has enrolled.
- Dev and production passkeys are separate by construction, since `identity.dev.<domain>` is a different RP ID. This is intended, not a bug.

**Recommend two passkeys per member.** WebAuthn is multi-credential by design and Keycloak allows several per account. One on the phone plus one on a laptop or a security key survives a lost device, a locked platform account, a vault outage and an ecosystem migration — none of which a single synced credential survives. It is also the only thing that keeps the Operator from being an availability dependency for account recovery, a cost `docs/adr/0049` accepts explicitly. Where a member is locked out anyway, the recovery path is the Operator re-running `bulk-invite.py` for them, which mints a fresh onboarding link (the script treats a matching-email re-invite as a normal re-issue).

### 6. How administrators log in (`keycloak-admin-ingress.yaml`)
Everything in §5 applies to the `smallworlds` realm. The Keycloak superadmin lives in the **`master` realm**, which keeps Keycloak's stock browser flow — username and password, no passkey — using `KEYCLOAK_ADMIN_PASSWORD` from the `keycloak-admin-creds` Secret. There is deliberately **no interactive admin account in `smallworlds`**; the only privileged identity there is the `bulk-invite` service account, which uses client credentials and cannot log in through a browser.

That separation is the point. A break-glass door must not depend on the mechanism it exists to rescue: if passkey login broke — a botched flow change, an RP ID rename, a WebAuthn regression — an admin who could only authenticate by passkey would have locked out the whole community with no way back. Note also that the password is readable with `kubectl get secret keycloak-admin-creds`, so **the true root credential for the cluster is the kubeconfig**, not any password.

The consequence is that the master admin is a single-factor credential that can mint tokens for any member, set a password on any account, or unbind the passkey flow — the one identity the passkey design does not protect. The chart's Ingress publishes `/` for the whole host, which put that login box on the public internet. `keycloak-admin-ingress.yaml` adds a second, more specific route for the `/admin` prefix carrying the same `ipAllowList` the Operator Console uses (tailnet, private LAN ranges, loopback), with an explicit `router.priority` so it outranks the chart's `/` route by declaration rather than by Traefik's rule-length heuristic. Members still reach `/realms/...` from anywhere, so the restriction cannot live on the chart's Ingress.

Three things worth knowing:
- **`admin-tools/bulk-invite.py` now needs the Private Network or the Cluster Node's LAN**, because it calls `/admin/realms/...` through this host. In-cluster callers are unaffected — `realm-config-job` and the init jobs address the Service directly and never traverse Traefik. The action-token SPI sits under `/realms/`, not `/admin`, so link generation itself is not address-restricted (it still requires an admin bearer token).
- **The new Ingress must be patched per environment.** `admin-tools/generate_domain_patches.py` targets ingresses by name, so `keycloak-admin` has its own entry there. Without it the route would keep the base domain, never match, and the restriction would silently not apply — the same failure shape as the positional-env-patch bug recorded below.
- **`tls` here names `keycloak-tls` but carries no `cluster-issuer` annotation**, because cert-manager already manages that certificate from the chart's Ingress. Annotating both would have two Ingresses racing for one secret.

Still open: the master realm has no second factor. Password plus WebAuthn or TOTP would be appropriate for an account of this power, and there is exactly one of them, so the "members would need an authenticator app" objection does not apply. That is a bound-flow change in `master` and has not been made.

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
