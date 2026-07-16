import { test, expect } from '@playwright/test';
import { FULL_OIDC, SKIP_REASON, expectRedirectIntoKeycloak } from './oidc-mode';

/**
 * Bulwark Webmail — smoke test via OIDC SSO
 *
 * Verifies that Bulwark loads after the OIDC redirect through Keycloak.
 * Bulwark is a JMAP-native webmail client connecting to Stalwart.
 */

const DOMAIN = process.env.DOMAIN!;
const BULWARK_URL = `https://webmail.${DOMAIN}`;

test('Bulwark Webmail: OIDC wiring redirects into Keycloak', async ({ browser }) => {
  await expectRedirectIntoKeycloak(browser, BULWARK_URL);
});

test.describe('Bulwark Webmail', () => {
  test.skip(!FULL_OIDC, SKIP_REASON);

  test.beforeEach(async ({ page }) => {
    // Navigate to Bulwark — with OAUTH_ONLY=true it should auto-redirect to Keycloak
    await page.goto(BULWARK_URL);

    // Wait for the OIDC redirect chain to finish and Bulwark to load.
    await page.waitForURL(url => {
      const href = url.toString();
      return (href.includes(BULWARK_URL) || href.includes('webmail.')) &&
             !href.includes('identity.') &&
             !href.includes('/login-actions/');
    }, { timeout: 60_000 });
  });

  test('loads inbox after OIDC auto-login', async ({ page }) => {
    // Verify the Bulwark mailbox UI loaded.
    // Look for common email UI indicators: inbox heading, message list, or mail navigation
    const mailboxUI = page.locator('[data-testid="inbox"], [data-testid="message-list"], [role="main"], .inbox, .message-list, nav')
      .or(page.getByRole('heading', { name: /inbox/i }))
      .or(page.getByRole('navigation'));
    await expect(mailboxUI.first()).toBeVisible({ timeout: 30_000 });
  });

  test('compose button is available', async ({ page }) => {
    // The compose/new message button should be accessible
    const composeBtn = page.getByRole('button', { name: /compose|new|write|verfassen/i })
      .or(page.getByRole('link', { name: /compose|new|write|verfassen/i }))
      .or(page.locator('[data-testid="compose"], [aria-label*="compose" i], [aria-label*="new" i]'));

    await expect(composeBtn.first()).toBeVisible({ timeout: 20_000 });
  });
});
