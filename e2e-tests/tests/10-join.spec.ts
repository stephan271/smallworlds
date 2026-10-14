import { test, expect } from '@playwright/test';
import { execFileSync } from 'child_process';
import { randomBytes } from 'crypto';

/**
 * Member Invitations (docs/adr/0058, doc/tenant-join.md).
 *
 * Issues a real invitation with the shell route (manage.py, inside the join
 * pod), walks the join page in German, and follows the minted link into
 * Keycloak. It stops at Keycloak's passkey step: registering a passkey needs an
 * authenticator, and on staging also a certificate the browser trusts. What it
 * proves is everything before that — the code, the account, join's own token
 * and its handler, the theme, and that Keycloak speaks the member's language.
 * The rest of the path is covered against a local Keycloak by
 * infrastructure/kubernetes/tenants/join/tests/live-keycloak.sh.
 *
 * The started account is deleted again afterwards.
 *
 * The Desk needs the operator's TOTP, so only its gate is checked here: it
 * must never answer 200 to a request without a session.
 *
 * Requires KUBECONFIG, which test-pr-locally.sh exports.
 */

const DOMAIN = process.env.DOMAIN!;
const JOIN_URL = `https://join.${DOMAIN}`;
const IDENTITY = `https://identity.${DOMAIN}`;

const suffix = randomBytes(3).toString('hex');
const USERNAME = `e2e-join-${suffix}`;

function kubectl(args: string[]): string {
  return execFileSync('kubectl', args, {
    encoding: 'utf-8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function manage(args: string[]): string {
  return kubectl(['-n', 'join', 'exec', 'deploy/join', '--', 'python', '/app/manage.py', ...args]);
}

let invitationId = '';
let code = '';

test.describe('Member Invitations', () => {
  test.beforeAll(() => {
    test.skip(!kubectl(['get', 'ns', 'join', '--ignore-not-found']),
      'join is not deployed on this cluster');
    const issued = manage(['create', '--locale', 'de', '--label', `e2e ${suffix}`, '--days', '1']);
    invitationId = issued.match(/Invitation (\d+)/)![1];
    code = issued.match(/Code: (\S+)/)![1];
  });

  test.afterAll(() => {
    if (invitationId) {
      try { manage(['revoke', invitationId, '--delete-account']); } catch { /* already gone */ }
    }
  });

  test('the Desk never answers without a session', async ({ request }) => {
    const response = await request.get(`${JOIN_URL}/desk/`, { maxRedirects: 0 });
    // 403 from outside the Private Network; a redirect to the master realm's
    // sign-in from inside it. Never the Desk itself.
    expect([302, 403]).toContain(response.status());
    if (response.status() === 302) {
      expect(response.headers()['location']).toMatch(/^\/desk\/login$|\/realms\/master\//);
    }
  });

  test('an invitation leads into Keycloak in the member\'s language', async ({ page }) => {
    await page.goto(`${JOIN_URL}/#${code}`);
    await expect(page.locator('[data-view="welcome"] h1')).toContainText('Willkommen');

    await page.click('#start');
    await page.fill('#username', USERNAME);
    await expect(page.locator('#username-status')).toContainText('ist frei');
    await page.fill('#firstName', 'E2E');
    await page.fill('#lastName', 'Join');
    await page.fill('#email', `${USERNAME}@example.org`);

    await Promise.all([
      page.waitForURL(`${IDENTITY}/realms/smallworlds/**`),
      page.click('#profile-submit'),
    ]);
    const landed = new URL(page.url());
    expect(landed.searchParams.get('kc_locale')).toBe('de');

    // Keycloak has accepted join's own token and goes straight to the passkey
    // step — no confirmation page (docs/adr/0059) — in German, in the
    // smallworlds theme, naming the account the passkey is for.
    await expect(page.locator('html')).toHaveAttribute('lang', 'de');
    await expect(page.locator('#registerWebAuthn')).toBeVisible();
    await expect(page.locator('.sw-account-name')).toHaveText(USERNAME);
    await expect(page.locator('body')).not.toContainText(/ungültig|abgelaufen|invalid|expired/i);
  });

  test('opening the invitation again resumes the same account', async ({ page }) => {
    await page.goto(`${JOIN_URL}/#${code}`);
    await expect(page.locator('[data-view="resume"] h1')).toContainText(USERNAME);
  });
});
