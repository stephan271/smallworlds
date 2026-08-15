# Keycloak Configuration for Passkey Onboarding

To enable the onboarding flow created by `bulk-invite.py`, you must configure Keycloak as follows:

## 1. Enable "Edit Username"
When users click the Action Token link, they will be forced to `UPDATE_PROFILE`. By default, they cannot edit their username. Since we provisioned them with a temporary username (their email), they need to be able to change it.

1. Log in to the Keycloak Admin Console.
2. Go to **Realm Settings** > **Login**.
3. Toggle **Edit username** to **ON**.
4. Save.

## 2. The Action Token Link Generator SPI

This SPI exposes `POST /realms/{realm}/action-token-link/generate-link`, which mints
the same onboarding action token that Keycloak's `execute-actions-email` endpoint
would — with the same required actions and the same lifespan — but **returns the URL
instead of mailing it**. That is what lets a cluster onboard members without any mail
capability at all (see `doc/mail.md`).

**On a SmallWorlds cluster it is already deployed.** The jar ships with the Keycloak
tenant as a `configMapGenerator` entry
(`infrastructure/kubernetes/tenants/keycloak/kustomization.yaml`) and is mounted at
`/opt/keycloak/providers/action-token-generator.jar` (`values.yaml`). Nothing to do.

Rebuild and re-commit the jar only after changing the SPI source, or when pointing
the script at a Keycloak that this repo did not deploy:

```bash
cd ../infrastructure/keycloak-spi/action-token-generator
./build.sh
# then copy target/action-token-generator-1.0.0-SNAPSHOT.jar into the tenant
# directory as action-token-generator.jar, or into that Keycloak's providers/
```

Authorization: the endpoint requires the realm role `admin` or `manage-users` on
`realm-management`. The `bulk-invite` service-account client created by
`realm-config-job.yaml` holds `realm-admin`, a composite that includes it, so the
script's existing credential already works.

## 3. Configure Passkey-Only Login (Passwordless)
We need to remove passwords from the login flow and strictly require WebAuthn (Passkeys).

1. Go to **Authentication** > **Flows**.
2. Duplicate the default `browser` flow. Name the copy `passkey-only-browser`.
3. In the new `passkey-only-browser` flow:
   - Find the `Username Password Form` execution. **Delete it**.
   - Ensure the flow contains `Cookie` (Alternative) and `Identity Provider Redirector` (Alternative).
   - Add an execution: **WebAuthn Passwordless Authenticator**.
   - Set **WebAuthn Passwordless Authenticator** to **REQUIRED**.
4. Bind this flow as the default:
   - Click the action menu next to `passkey-only-browser` and select **Bind flow**.
   - Choose **Browser flow**.

## 4. Invite members

1. Create a `users.csv`:
   ```csv
   email,phone
   testuser1@example.com,+1234567890
   ```
2. Run the invite script. It authenticates as the `bulk-invite` service-account
   client, so it needs that client's secret — the `bulk-invite-secret` key of the
   `keycloak-admin-creds` Secret, set by `smallworlds-init.sh`:
   ```bash
   export KEYCLOAK_URL="https://identity.yourdomain.com"
   export KEYCLOAK_REALM="smallworlds"
   export KEYCLOAK_CLIENT_SECRET="<bulk-invite-secret>"

   ./bulk-invite.py users.csv
   ```
3. By default no mail is sent. The links are written to `invite-links.csv`
   (mode `0600`, override with `-o`) as `email,phone,link`.

   **Each link is a bearer credential** that grants access to that account until it
   expires — the validity window is printed at the end of the run. Distribute the
   links over a channel you trust (Signal, a QR code, in person). Not SMS, and not
   e-mail, which is the channel this flow exists to avoid depending on. Delete the
   file once the invitations are out.
4. To have Keycloak mail the links instead, pass `--email`. This requires a working
   SMTP relay; without one, Keycloak accepts the request and the mail is silently
   lost.
5. The member opens the link, is prompted to update their profile (including
   choosing a username), then to register a passkey.
6. After completion they log in with the passkey — the realm has no password form.

If step 3 reports that the SPI is not deployed, see section 2.
