import { test, expect } from '@playwright/test';
import { FULL_OIDC, SKIP_REASON, expectRedirectIntoKeycloak } from './oidc-mode';

/**
 * Plane — smoke test via OIDC SSO
 *
 * Verifies that Plane loads and redirects to Keycloak.
 */

const DOMAIN = process.env.DOMAIN!;
const PLAN_URL = `https://plan.${DOMAIN}`;

test('Plane: OIDC wiring redirects into Keycloak', async ({ browser }) => {
  // Plane usually triggers OIDC from a specific endpoint or the root login page.
  // We'll hit a route that forces OIDC authentication.
  await expectRedirectIntoKeycloak(browser, `${PLAN_URL}/`);
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
