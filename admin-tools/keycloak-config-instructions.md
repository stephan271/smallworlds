# Keycloak Configuration for Passkey Onboarding

To enable the onboarding flow created by `bulk-invite.py`, you must configure Keycloak as follows:

## 1. Enable "Edit Username"
When users click the Action Token link, they will be forced to `UPDATE_PROFILE`. By default, they cannot edit their username. Since we provisioned them with a temporary username (their email), they need to be able to change it.

1. Log in to the Keycloak Admin Console.
2. Go to **Realm Settings** > **Login**.
3. Toggle **Edit username** to **ON**.
4. Save.

## 2. Deploy the Action Token Link Generator SPI
1. Build the SPI:
   ```bash
   cd ../infrastructure/keycloak-spi/action-token-generator
   ./build.sh
   ```
2. Copy the resulting `target/action-token-generator-1.0.0-SNAPSHOT.jar` to Keycloak's `providers/` directory.
   - If using the official Docker image, volume mount it or build a custom image copying the jar to `/opt/keycloak/providers/`.
3. Restart Keycloak (or run `kc.sh build` if using Keycloak 17+ Quarkus distribution).

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

## 4. Test the Integration
1. Create a `users.csv`:
   ```csv
   email,phone
   testuser1@example.com,+1234567890
   ```
2. Run the invite script:
   ```bash
   export KEYCLOAK_URL="https://auth.yourdomain.com"
   export KEYCLOAK_REALM="your-realm"
   export KEYCLOAK_ADMIN_USER="admin"
   export KEYCLOAK_ADMIN_PASS="admin_password"
   
   ./bulk-invite.py users.csv
   ```
3. Copy the outputted link and open it in an incognito window.
4. You should be prompted to update your profile (including choosing a username).
5. Next, you will be prompted to register a Passkey.
6. After completion, log out and try to log in normally. The login screen will ask for your passkey instead of a password.
