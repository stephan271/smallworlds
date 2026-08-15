import { test as setup, expect } from '@playwright/test';
import fs from 'fs';
import path from 'path';

/**
 * Auth setup — onboards test users with a passkey and saves browser state.
 *
 * The realm has no password credential (doc/tenant-keycloak.md §5), so this
 * walks the real onboarding path: open the action-token link minted by
 * setup/provision-test-users.ts, register a passkey against a CDP virtual
 * authenticator, and save the resulting SSO session. Later specs reuse the
 * storage state, so they never need the authenticator again — which is just as
 * well, since a virtual authenticator does not outlive its browser context.
 *
 * Chromium only: virtual authenticators are a CDP feature. The suite already
 * provisions chromium exclusively.
 */

const LINKS_FILE = path.join(__dirname, '../setup/.auth/invite-links.json');

const ALICE_AUTH_FILE = path.join(__dirname, '../setup/.auth/alice.json');
const BOB_AUTH_FILE = path.join(__dirname, '../setup/.auth/bob.json');

function onboardingLink(username: string): string {
  if (!fs.existsSync(LINKS_FILE)) {
    throw new Error(
      `${LINKS_FILE} is missing — run setup/provision-test-users.ts first ` +
        `(run-smoke-tests.sh does this unless SKIP_PROVISION=1).`,
    );
  }
  const links = JSON.parse(fs.readFileSync(LINKS_FILE, 'utf-8'));
  const link = links[username];
  if (!link) throw new Error(`No onboarding link for ${username} in ${LINKS_FILE}`);
  return link;
}

setup('onboard alice with a passkey', async ({ page }) => {
  await registerPasskey(page, 'sw-test-alice');
  await page.context().storageState({ path: ALICE_AUTH_FILE });
});

setup('onboard bob with a passkey', async ({ page }) => {
  await registerPasskey(page, 'sw-test-bob');
  await page.context().storageState({ path: BOB_AUTH_FILE });
});

/**
 * Open the user's onboarding link and complete WebAuthn registration.
 *
 * The action token authenticates the user by itself, so there is no credential
 * to present — the link lands directly on the passkey registration screen.
 */
async function registerPasskey(
  page: import('@playwright/test').Page,
  username: string,
) {
  // A resident (discoverable) credential with user verification already
  // satisfied, matching the realm's passwordless policy: RequireResidentKey
  // Yes, UserVerificationRequirement required.
  const client = await page.context().newCDPSession(page);
  await client.send('WebAuthn.enable');
  await client.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });

  // Some Keycloak versions ask for the authenticator label with a JS prompt.
  page.on('dialog', (dialog) => dialog.accept('e2e-virtual-authenticator').catch(() => {}));

  await page.goto(onboardingLink(username));

  // Keycloak may interpose a "Register passkey" confirmation before invoking
  // navigator.credentials.create(); click it when present.
  const registerButton = page
    .locator('#registerWebAuthn, input[name="registerWebAuthn"]')
    .or(page.getByRole('button', { name: /register|continue|weiter/i }));
  if (await registerButton.first().isVisible({ timeout: 10_000 }).catch(() => false)) {
    await registerButton.first().click();
  }

  // Other versions render a label field on a form instead of a prompt.
  const labelInput = page.locator('#authenticatorLabel, input[name="authenticatorLabel"]');
  if (await labelInput.isVisible({ timeout: 5_000 }).catch(() => false)) {
    await labelInput.fill('e2e-virtual-authenticator');
    await page.click('#kc-login, input[type="submit"], button[type="submit"]');
  }

  // Registration is done once Keycloak stops driving required actions.
  await page.waitForURL((url) => !url.toString().includes('/login-actions/'), {
    timeout: 30_000,
  });

  expect(page.url()).not.toContain('/login-actions/');
  console.log(`  ✅ Registered passkey and signed in as ${username}`);
}
