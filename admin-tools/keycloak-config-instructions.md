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

1. Create a `users.csv`. Assign each member a **username**; `phone` and `email` are
   both optional:
   ```csv
   username,phone,email
   ana,+41790000001,ana@example.test
   bo,+41790000002,
   ```
   A legacy `email,phone` file still works — the username is then the local part of
   the address. Prefer explicit usernames: `ana@gmail.test` and `ana@bluewin.test`
   both derive `ana`, and the script refuses the second rather than guessing (see
   step 5). Forgejo rejects `@` in usernames, which is why the address itself cannot
   be one.

2. Run the invite script. It authenticates as the `bulk-invite` service-account
   client, so it needs that client's secret — the `bulk-invite-secret` key of the
   `keycloak-admin-creds` Secret, set by `smallworlds-init.sh`:
   ```bash
   export KEYCLOAK_URL="https://identity.yourdomain.com"
   export KEYCLOAK_REALM="smallworlds"
   export KEYCLOAK_CLIENT_SECRET="<bulk-invite-secret>"

   ./bulk-invite.py users.csv            # links only
   ./bulk-invite.py users.csv --qr       # links + printable QR codes
   ```

3. By default no mail is sent. Links are written to `invite-links.csv` (mode `0600`,
   override with `-o`) as `username,phone,email,link`. With `--qr`, one SVG per
   member also lands in `invite-qr/` (override by passing a directory). SVG is
   vector — print at **4 cm or larger**, since an onboarding link is a dense symbol.
   `--qr` needs either the `qrcode` Python package or the `qrencode` binary.

4. **Each link logs its holder into that account** until it expires; the validity
   window is printed at the end of the run. Deliver it:
   - **In person** — hand over the printed QR. No network involved at all.
   - **Remotely** — open a Jitsi room (`meet.<domain>/<room>`), have the member join
     as a guest in the browser with no account, admit them from the lobby, and send
     the link as a **private** chat message. The room URL is short enough to dictate
     over the phone, which the onboarding link is not; and you see the person's face,
     which is the identity check an invitation is supposed to make.

   Never post a link or QR to a group — everyone present can use it. Not by e-mail or
   SMS. Delete `invite-links.csv` and `invite-qr/` once the invitations are out.

5. Two refusals are deliberate, and both exit non-zero: a username repeated **within
   the CSV**, and a username that already exists in Keycloak **under a different
   email**. In the second case the account may belong to somebody else, and issuing a
   link would log the wrong person into it. Re-running for the *same* member (matching
   email, or no email on either side) is a normal re-issue and mints a fresh link —
   that is the recovery path for an expired link or a lost passkey.

6. The member opens the link on the device they intend to use, updates their profile
   (including choosing their final username), registers a passkey, and is then shown
   a set of **recovery codes**. The passkey is created on **that** device, so a
   phone-only member should open it on their phone. Tell members to keep the recovery
   codes somewhere off that device — they are the only way back into the account
   without coming to you, and they are shown once.

7. From then on they log in with the passkey. Invited members never get a password
   credential and `resetPasswordAllowed` is `false`, so the passkey — or a recovery
   code via *Try Another Way* — is their only way in. See `doc/tenant-keycloak.md`
   §5, and note that section 3 above is a manual step the realm JSON does not
   perform. Encourage a second passkey via the account console as well; recovery
   codes get a member back in, but a second passkey means never needing them.

   The recovery-code login path has **not** been tested against a running Keycloak,
   and the realm JSON only takes effect on a fresh realm import — an existing
   cluster keeps its previous login flow. Both caveats are detailed in
   `doc/tenant-keycloak.md` §5.

To have Keycloak mail the links instead of writing them out, pass `--email`. That
requires a working SMTP relay (`doc/mail.md`); without one Keycloak accepts the
request and the message is silently lost.

If step 3 reports that the SPI is not deployed, see section 2.
