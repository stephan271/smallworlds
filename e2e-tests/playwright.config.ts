import { defineConfig, devices } from '@playwright/test';

/**
 * SmallWorlds E2E Smoke Test Configuration
 *
 * Required environment variables:
 *   DOMAIN          - The community domain (e.g. "smallworlds.network")
 *   KC_ADMIN_PASS   - Keycloak admin password (for test user provisioning)
 *
 * Optional:
 *   HEADED          - Set to "1" to run in headed mode
 *   SLOWMO          - Milliseconds to slow down operations (e.g. "500")
 *   RESOLVE_IP      - Resolve all *.DOMAIN hostnames to this IP inside the
 *                     browser (Chromium host-resolver-rules). Use when
 *                     testing a LAN deployment from inside the same LAN:
 *                     public DNS points at the router's WAN address and the
 *                     hairpin-NAT path can be slow enough to flake
 *                     first-load assertions — this takes the direct LAN
 *                     path, which is also what real clients use via router
 *                     DNS / hosts entries.
 */

const domain = process.env.DOMAIN;
if (!domain) {
  // Allow config to load without DOMAIN for IDE support, but tests will fail
  console.warn('⚠ DOMAIN environment variable not set. Tests will fail.');
}

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,       // Sequential execution — don't overload a small server
  forbidOnly: true,           // Fail CI if .only is left in
  retries: 1,                 // One retry for transient startup issues
  workers: 1,                 // Single worker — sequential
  timeout: 90_000,            // 90s per test (services on small VMs can be slow)
  expect: {
    timeout: 15_000,          // 15s for assertions
  },

  reporter: [
    ['list'],                                    // Terminal output
    ['html', { outputFolder: 'reports/html', open: 'never' }],  // HTML report
  ],

  use: {
    /* Increase timeouts for navigation — OIDC redirects go through Keycloak */
    navigationTimeout: 60_000,
    actionTimeout: 30_000,

    /* Capture evidence on failure */
    screenshot: 'only-on-failure',
    trace: 'on-first-retry',
    video: 'retain-on-failure',

    /* Ignore self-signed or Let's Encrypt staging certs */
    ignoreHTTPSErrors: true,

    /* Slow down for debugging if requested */
    launchOptions: {
      slowMo: process.env.SLOWMO ? parseInt(process.env.SLOWMO) : 0,
      args: process.env.RESOLVE_IP && domain
        ? [`--host-resolver-rules=MAP *.${domain} ${process.env.RESOLVE_IP}, MAP ${domain} ${process.env.RESOLVE_IP}`]
        : [],
    },
  },

  /* Configure projects: auth setup first, then the actual tests */
  projects: [
    {
      name: 'auth-setup',
      testMatch: /auth\.setup\.ts/,
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'smoke-tests',
      dependencies: ['auth-setup'],
      use: {
        ...devices['Desktop Chrome'],
        /* Reuse authenticated state from the auth setup */
        storageState: 'setup/.auth/alice.json',
      },
    },
  ],
});
