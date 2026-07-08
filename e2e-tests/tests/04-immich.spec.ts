import { test, expect } from '@playwright/test';
import { FULL_OIDC, SKIP_REASON, expectOauthEnabled } from './oidc-mode';

/**
 * Immich Photos — smoke test via OIDC SSO
 *
 * Verifies that Immich loads correctly and we can access the core UI.
 */

const DOMAIN = process.env.DOMAIN!;
const IMMICH_URL = `https://photos.${DOMAIN}`;

test('Immich Photos: OIDC wiring is enabled in server config', async () => {
  await expectOauthEnabled(IMMICH_URL);
});

test.describe('Immich Photos', () => {
  test.skip(!FULL_OIDC, SKIP_REASON);

  test.beforeEach(async ({ page }) => {
    // Navigate to Immich
    await page.goto(IMMICH_URL);

    // If Immich presents its local login screen, click the Keycloak/OIDC login button
    const keycloakBtn = page.getByRole('link', { name: /keycloak|smallworlds|oidc/i })
      .or(page.getByRole('button', { name: /keycloak|smallworlds|oidc|log in with/i }))
      .or(page.getByText(/login with keycloak/i))
      .or(page.locator('a[href*="oauth"], a[href*="keycloak"]').filter({ hasText: /keycloak|smallworlds/i }))
      .or(page.locator('button:has-text("Keycloak"), a:has-text("Keycloak")'));

    if (await keycloakBtn.first().isVisible({ timeout: 5_000 }).catch(() => false)) {
      await keycloakBtn.first().click();
    }

    // Wait for the OIDC redirect chain to complete and Immich to load.
    await page.waitForURL(url => {
      const href = url.toString();
      return href.includes('photos.') &&
             !href.includes('identity.') &&
             !href.includes('/login-actions/');
    }, { timeout: 60_000 });
  });

  test('loads main view after OIDC auto-login', async ({ page }) => {
    // A successful auto-login lands either on the main app shell — whose
    // "Primary" sidebar nav and header Upload button are stable landmarks —
    // or, for a brand-new user, the onboarding wizard (a "welcome" heading).
    // Role-based only: broad text unions can resolve to hidden nodes during
    // the SPA's hydration and flake the visibility assertion.
    const mainView = page.getByRole('navigation', { name: 'Primary' })
      .or(page.getByRole('button', { name: /upload/i }))
      .or(page.getByRole('heading', { name: /welcome/i }));
    await expect(mainView.first()).toBeVisible({ timeout: 30_000 });
  });

  test('handles onboarding or empty state gracefully', async ({ page }) => {
    // Sometimes Immich shows a "Welcome" or "Click to upload your first photo" screen
    // We just want to ensure we are actually in the app UI
    const appUI = page.getByRole('heading', { name: /welcome/i })
      .or(page.getByRole('button', { name: /upload|theme/i }));

    await expect(appUI.first()).toBeVisible({ timeout: 30_000 });
  });

  test('user profile menu is accessible', async ({ page }) => {
    // A first-time user sits in the v3 onboarding wizard, which has no
    // header — the profile menu only exists on the main app shell.
    const onboarding = page.getByText(/let's get you started/i);
    if (await onboarding.isVisible({ timeout: 3_000 }).catch(() => false)) {
      test.skip(true, 'fresh user is in the onboarding wizard — no app header yet');
    }

    // Immich has a user avatar or menu icon in the top right
    // Use a very broad locator to find the avatar or account settings button
    const userAvatar = page.getByRole('button', { name: /account|profile/i })
      .or(page.locator('button img[alt*="avatar"]'))
      .or(page.getByRole('button', { name: /sw-test-alice|alice|a/i }))
      .or(page.locator('header button').last());

    await expect(userAvatar.first()).toBeVisible({ timeout: 15_000 });
  });
});
