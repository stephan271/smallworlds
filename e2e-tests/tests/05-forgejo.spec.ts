import { test, expect } from '@playwright/test';

/**
 * Forgejo — smoke test via OIDC SSO
 *
 * Verifies that Forgejo loads and we can access user settings.
 */

const DOMAIN = process.env.DOMAIN!;
const FORGEJO_URL = `https://git.${DOMAIN}`;

test.describe('Forgejo', () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to Forgejo
    await page.goto(FORGEJO_URL);

    // Forgejo lands on the Explore page if unauthenticated.
    // If we see the "Sign In" link in the top nav, we aren't logged in.
    try {
      const topSignInLink = page.getByRole('link', { name: /sign in/i }).filter({ hasText: 'Sign In' })
          .or(page.locator('a[href*="/user/login"]'));
      await topSignInLink.first().click({ timeout: 5_000 });
      
      // Now we are on the login page. Click the Keycloak button.
      const keycloakBtn = page.getByRole('link', { name: /keycloak|smallworlds|oidc/i })
        .or(page.getByRole('button', { name: /keycloak|smallworlds|oidc/i }))
        .or(page.getByText(/sign in with keycloak/i))
        .or(page.locator('a[href*="oauth"], a[href*="keycloak"]').filter({ hasText: /keycloak|smallworlds/i }))
        .or(page.locator('.button.keycloak, .button.oauth'));
        
      await keycloakBtn.first().click({ timeout: 5_000 });
    } catch (e) {
      // Ignore if elements not found, might already be logged in
    }

    // Wait for the OIDC redirect chain to finish.
    await page.waitForURL(url => {
      const href = url.toString();
      // Wait until we are no longer on identity/login-actions
      return !href.includes('identity.') && !href.includes('/login-actions/');
    }, { timeout: 60_000 });
  });

  test('dashboard loads after OIDC login', async ({ page }) => {
    // Navigate to the root dashboard (if we aren't there already)
    await page.goto(FORGEJO_URL);

    // Verify the Forgejo dashboard loaded by looking for common elements
    const dashboardUI = page.locator('.dashboard, .repository, .repo-title')
      .or(page.getByRole('heading', { name: /repositories/i }))
      .or(page.getByText(/my repositories/i))
      .or(page.getByText(/explore/i));

    await expect(dashboardUI.first()).toBeVisible({ timeout: 20_000 });
  });

  test('shows user information', async ({ page }) => {
    await page.goto(FORGEJO_URL);

    // Look for the user avatar in the top right
    const userAvatar = page.locator('.item.ui.dropdown img.avatar, nav img[alt*="avatar"]')
        .or(page.getByAltText(/avatar/i));

    await expect(userAvatar.first()).toBeVisible({ timeout: 15_000 });

    // Click it to open the dropdown
    await userAvatar.first().click();

    // Verify the username "sw-test-alice" is in the dropdown
    const usernameElem = page.getByText(/sw-test-alice|alice/i);
    await expect(usernameElem.first()).toBeVisible({ timeout: 10_000 });
  });

  test('can navigate to user settings', async ({ page }) => {
    // Navigate directly to the settings page
    await page.goto(`${FORGEJO_URL}/user/settings`);

    // Verify we see the settings page title
    const settingsHeader = page.getByRole('heading', { name: /settings/i })
      .or(page.getByText(/profile settings/i))
      .or(page.getByText(/account settings/i));

    await expect(settingsHeader.first()).toBeVisible({ timeout: 15_000 });
  });
});
