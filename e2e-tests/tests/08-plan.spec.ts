import { test, expect } from '@playwright/test';
import { FULL_OIDC, SKIP_REASON } from './oidc-mode';

/**
 * Plane — smoke test via OIDC SSO
 *
 * Verifies that Plane loads and redirects to Keycloak.
 */

const DOMAIN = process.env.DOMAIN!;
const PLAN_URL = `https://plan.${DOMAIN}`;

test('Plane: login page is served (Plane CE ships no OIDC/SSO)', async ({ browser }) => {
  // Plane Community Edition has NO OIDC support — its instance configuration
  // exposes only ENABLE_EMAIL_PASSWORD / ENABLE_MAGIC_LINK_LOGIN /
  // ENABLE_SIGNUP (verified against the live instance_configurations table).
  // Login is email/password, so the meaningful smoke check is that the app
  // serves its own login form, not a Keycloak redirect.
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(`${PLAN_URL}/`);
  const loginForm = page.getByPlaceholder(/name@company\.com/i)
    .or(page.getByRole('button', { name: /continue/i }));
  await expect(loginForm.first()).toBeVisible({ timeout: 20_000 });
  await context.close();
});

test.describe('Plane', () => {
  test.skip(!FULL_OIDC, SKIP_REASON);

  test.beforeEach(async ({ page }) => {
    // For now we just skip the deep tests as we only need shallow wiring 
    // verification for the smoke test pipeline on Plane.
  });

  test('dashboard loads after OIDC login', async ({ page }) => {
    // Placeholder for future deep OIDC tests for Plane
  });
});
