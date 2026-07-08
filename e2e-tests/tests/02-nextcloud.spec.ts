import { test, expect } from '@playwright/test';
import { FULL_OIDC, SKIP_REASON, expectNextcloudOidcProvider } from './oidc-mode';

/**
 * Nextcloud — smoke test via OIDC SSO
 *
 * Verifies that Nextcloud loads, the OIDC redirect completes automatically
 * (Keycloak SSO session from storage state), and the Files app is usable.
 */

const DOMAIN = process.env.DOMAIN!;
const NEXTCLOUD_URL = `https://files.${DOMAIN}`;

test('Nextcloud: OIDC provider is registered on the login page', async ({ browser }) => {
  await expectNextcloudOidcProvider(browser, NEXTCLOUD_URL);
});

test.describe('Nextcloud', () => {
  test.skip(!FULL_OIDC, SKIP_REASON);

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
    try {
      const wizardClose = page.locator('#firstrunwizard .modal-header button, #firstrunwizard .icon-close, .first-run-wizard .modal-header button');
      if (await wizardClose.first().isVisible({ timeout: 5000 })) {
        await wizardClose.first().click();
        await page.waitForTimeout(500);
      }
    } catch (e) {
      // Ignored
    }
    
    // Fallback: press Escape a few times just in case
    await page.keyboard.press('Escape');
    await page.waitForTimeout(200);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(500);
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
    const filesList = page.locator('#app-content-vue, #app-content-files, table.files-list, .files-filestable, #app-content .app-content-list, #app-content');
    await expect(filesList.first()).toBeVisible({ timeout: 15_000 });
  });

  test('user menu shows alice', async ({ page }) => {
    // Open the user menu
    const userMenu = page.locator('header .avatardiv, header .user-menu__avatar');
    await expect(userMenu.first()).toBeVisible({ timeout: 15_000 });

    // Click to expand user menu (force: true bypasses the first run wizard overlay if it lingers)
    await userMenu.first().click({ force: true });

    // Verify username appears
    const userLabel = page.getByText(/sw-test-alice/i)
      .or(page.getByText(/alice/i));
    await expect(userLabel.first()).toBeVisible({ timeout: 10_000 });
  });

  test('Collabora (richdocuments) is integrated', async ({ page }) => {
    await page.goto(`${NEXTCLOUD_URL}/apps/files/`);

    // Wait for files view to load
    await page.waitForURL(url => url.toString().includes('/apps/files'), {
      timeout: 60_000,
    });

    // Look for the "New" button (plus icon)
    const newBtn = page.getByRole('button', { name: /new/i })
      .or(page.locator('.button.new, .new-button, .button-vue.new, #new-document-menu'));
    await expect(newBtn.first()).toBeVisible({ timeout: 15_000 });
    
    // Click the New button
    await newBtn.first().click({ force: true });
    
    // Wait for the dropdown menu. richdocuments (Collabora) contributes the
    // "Document" / "Spreadsheet" / "Presentation" entries — they only appear
    // when the integration is active. The menu renders them as menuitems.
    const newDocMenu = page.getByRole('menuitem', { name: /document|spreadsheet|presentation/i });
    await expect(newDocMenu.first()).toBeVisible({ timeout: 10_000 });
  });
});
