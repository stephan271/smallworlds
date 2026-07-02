import { test, expect } from '@playwright/test';

/**
 * Keycloak Account Console — login & logout smoke test
 *
 * Pre-condition: storage state has a valid SSO session for sw-test-alice
 * (set up by auth.setup.ts).
 */

const DOMAIN = process.env.DOMAIN!;
const KEYCLOAK_ACCOUNT = `https://identity.${DOMAIN}/realms/smallworlds/account/`;

test.describe('Keycloak', () => {
  test('account page loads with authenticated session', async ({ page }) => {
    await page.goto(KEYCLOAK_ACCOUNT);
    
    // The account page should load without hitting the login form.
    // Verify the account page rendered — Keycloak shows the user's name in the top right
    await expect(page.getByText(/Alice Testuser/i)).toBeVisible({ timeout: 15_000 });
  });

});
