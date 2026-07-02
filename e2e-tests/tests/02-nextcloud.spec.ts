import { test, expect } from '@playwright/test';

/**
 * Nextcloud — smoke test via OIDC SSO
 *
 * Verifies that Nextcloud loads, the OIDC redirect completes automatically
 * (Keycloak SSO session from storage state), and the Files app is usable.
 */

const DOMAIN = process.env.DOMAIN!;
const NEXTCLOUD_URL = `https://files.${DOMAIN}`;

test.describe('Nextcloud', () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to Nextcloud
    await page.goto(NEXTCLOUD_URL);

    // If Nextcloud doesn't auto-redirect, click the Keycloak/OIDC login button
    try {
      const keycloakBtn = page.getByRole('link', { name: /keycloak|smallworlds|oidc|log in with/i })
        .or(page.getByRole('button', { name: /keycloak|smallworlds|oidc|log in with/i }))
        .or(page.locator('a[href*="oauth"], a[href*="keycloak"]').filter({ hasText: /keycloak|smallworlds|oidc/i }))
        .or(page.locator('.button.keycloak, .button.oauth, #oidc-login-button'));

      await keycloakBtn.first().click({ timeout: 5_000 });
    } catch (e) {
      // Ignored
    }

    // Wait for the OIDC redirect chain to complete and Nextcloud to load.
    await page.waitForURL(url => {
      const href = url.toString();
      return href.includes('/apps/dashboard') ||
             href.includes('/apps/files') ||
             href.includes('/index.php/apps/');
    }, { timeout: 60_000 });

    // Handle Nextcloud's First Run Wizard (Hub onboarding popup)
    await page.keyboard.press('Escape');
    await page.waitForTimeout(1000); // Wait for modal animation to close
  });

  test('loads Files app after OIDC auto-login', async ({ page }) => {
    // Verify the main Nextcloud app loaded — look for the header or nav
    const mainContent = page.locator('#content');
    await expect(mainContent.first()).toBeVisible({ timeout: 15_000 });
  });

  test('Files view shows file listing', async ({ page }) => {
    await page.goto(`${NEXTCLOUD_URL}/apps/files/`);

    // Wait for files view to load
    await page.waitForURL(url => url.toString().includes('/apps/files'), {
      timeout: 60_000,
    });

    // The files list should be visible
    const filesList = page.locator('.files-filestable, #app-content-files');
    await expect(filesList.first()).toBeVisible({ timeout: 15_000 });
  });

  test('user menu shows alice', async ({ page }) => {
    // Open the user menu
    const userMenu = page.locator('header .avatardiv, header .user-menu__avatar');
    await expect(userMenu.first()).toBeVisible({ timeout: 15_000 });

    // Click to expand user menu
    await userMenu.first().click();

    // Verify username appears
    const userLabel = page.getByText(/sw-test-alice/i)
      .or(page.getByText(/alice/i));
    await expect(userLabel.first()).toBeVisible({ timeout: 10_000 });
  });
});
