import { test, expect } from '@playwright/test';
import { FULL_OIDC, SKIP_REASON, expectRedirectIntoKeycloak } from './oidc-mode';

/**
 * Roundcube Webmail — smoke test via OIDC SSO
 *
 * Verifies that Roundcube loads after the OIDC redirect through Keycloak.
 */

const DOMAIN = process.env.DOMAIN!;
const ROUNDCUBE_URL = `https://webmail.${DOMAIN}`;

test('Roundcube Webmail: OIDC wiring redirects into Keycloak', async ({ browser }) => {
  await expectRedirectIntoKeycloak(browser, ROUNDCUBE_URL);
});

test.describe('Roundcube Webmail', () => {
  test.skip(!FULL_OIDC, SKIP_REASON);

  test.beforeEach(async ({ page }) => {
    // Navigate to Roundcube
    await page.goto(ROUNDCUBE_URL);

    // If Roundcube doesn't auto-redirect, click the Keycloak login button
    try {
      const loginBtn = page.getByRole('button', { name: /keycloak|smallworlds/i })
        .or(page.getByRole('link', { name: /keycloak|smallworlds/i }))
        .or(page.locator('.button.keycloak, .button.oauth, [value*="Keycloak" i], a:has-text("Keycloak")'));

      // In case there are multiple, click the first one that is visible
      await loginBtn.first().click({ timeout: 5_000 });
    } catch (e) {
      // Ignored - maybe it auto-redirected already or button is not there
    }

    // Wait for the OIDC redirect chain to finish and Roundcube to load.
    // Roundcube lands on the mail view with ?_task=mail or similar.
    await page.waitForURL(url => {
      const href = url.toString();
      // Either we're on the Roundcube mail view or still on the base URL (loaded)
      return (href.includes(ROUNDCUBE_URL) || href.includes('webmail.')) &&
             !href.includes('identity.') &&
             !href.includes('/login-actions/');
    }, { timeout: 60_000 });
  });

  test('loads inbox after OIDC auto-login', async ({ page }) => {
    // Verify the Roundcube mailbox UI loaded.
    const mailboxUI = page.locator('#mailboxlist, #messagelist, .mailbox-list, #layout-list');
    await expect(mailboxUI.first()).toBeVisible({ timeout: 20_000 });
  });

  test('compose button is available', async ({ page }) => {
    // The compose button should be accessible — a core part of any webmail UI
    const composeBtn = page.getByRole('button', { name: /compose/i })
      .or(page.getByRole('link', { name: /compose/i }))
      .or(page.locator('a.compose, .button-compose, [href*="compose"]'));

    await expect(composeBtn.first()).toBeVisible({ timeout: 20_000 });
  });
});
