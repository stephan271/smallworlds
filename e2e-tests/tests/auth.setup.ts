import { test as setup, expect } from '@playwright/test';
import path from 'path';

/**
 * Auth setup — logs into Keycloak as test users and saves browser state.
 *
 * This runs before all other tests. The saved storage state (cookies + localStorage)
 * is reused by all subsequent test specs, providing SSO across all OIDC-connected apps.
 */

const DOMAIN = process.env.DOMAIN!;
const KEYCLOAK_URL = `https://identity.${DOMAIN}`;

const ALICE_AUTH_FILE = path.join(__dirname, '../setup/.auth/alice.json');
const BOB_AUTH_FILE = path.join(__dirname, '../setup/.auth/bob.json');

setup('authenticate as alice', async ({ page }) => {
  await keycloakLogin(page, 'sw-test-alice', 'SmallW0rlds-Test!');
  await page.context().storageState({ path: ALICE_AUTH_FILE });
});

setup('authenticate as bob', async ({ page }) => {
  await keycloakLogin(page, 'sw-test-bob', 'SmallW0rlds-Test!');
  await page.context().storageState({ path: BOB_AUTH_FILE });
});

/**
 * Perform a direct Keycloak login via the account console.
 * This establishes a Keycloak SSO session that will be used by all OIDC apps.
 */
async function keycloakLogin(
  page: import('@playwright/test').Page,
  username: string,
  password: string,
) {
  // Navigate to Keycloak account console — this triggers the login flow
  await page.goto(`${KEYCLOAK_URL}/realms/smallworlds/account/`);

  // The realm is passkey-first: the initial screen offers "Sign in with
  // Passkey". Test users authenticate with passwords, so switch to the
  // password form via Keycloak's "Try another way" link.
  const tryAnotherWay = page.locator('#try-another-way, a:has-text("Try another way")');
  if (await tryAnotherWay.isVisible({ timeout: 10_000 }).catch(() => false)) {
    await tryAnotherWay.click();
    // The authenticator selection screen lists the alternatives
    await page
      .locator('.select-auth-box-paragraph, .select-auth-box-headline')
      .filter({ hasText: /password/i })
      .first()
      .click();
  }

  // Wait for the login form to appear
  await page.waitForSelector('#username, input[name="username"]', { timeout: 30_000 });

  // Fill in the username
  await page.fill('#username, input[name="username"]', username);

  // Keycloak might use a two-step login (username first, then password or passkey)
  const passwordInput = page.locator('#password, input[name="password"]');
  const hasPasswordField = await passwordInput.isVisible({ timeout: 2_000 }).catch(() => false);

  if (!hasPasswordField) {
    // Two-step login: click 'Sign In' or 'Next' to proceed to the password screen
    await page.click('#kc-login, input[type="submit"], button[name="login"]');
    // Wait for the password field to appear on the next screen
    await passwordInput.waitFor({ state: 'visible', timeout: 15_000 });
  }

  // Fill in the password
  await passwordInput.fill(password);

  // Submit the form
  await page.click('#kc-login, input[type="submit"], button[name="login"]');

  // Wait for successful login — we should see the account page or be redirected
  // The Keycloak account console shows the user's name after login
  await page.waitForURL(url => !url.toString().includes('/login-actions/'), {
    timeout: 30_000,
  });

  // Verify we're logged in by checking we're no longer on the login page
  const currentUrl = page.url();
  expect(currentUrl).not.toContain('/login-actions/authenticate');

  console.log(`  ✅ Logged in as ${username}`);
}
