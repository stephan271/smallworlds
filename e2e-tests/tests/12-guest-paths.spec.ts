import { test, expect, chromium, type Browser, type Page } from '@playwright/test';
import https from 'node:https';
import dns from 'node:dns/promises';
import { FULL_OIDC, SKIP_REASON } from './oidc-mode';

/**
 * Guest Paths on the public entrypoint (docs/adr/0066).
 *
 * With the Member Network on, a member application's full UI answers only on
 * the `members` entrypoint, and the public one keeps the paths a signed-out
 * guest needs for a share link. An application update can add such a path,
 * and then only guests notice. So on every commit this:
 *   - makes a public link as a member, through `members`;
 *   - opens it in a signed-out browser on the public entrypoint and fails on
 *     any request the entrypoint refuses;
 *   - checks that sign-in, the apps and the member APIs answer 404 there;
 *   - checks that members still get the full application.
 *
 * Inside the cluster the member applications' hostnames resolve to
 * `traefik-members`, so the public entrypoint is reached through the `traefik`
 * Service, which is what the node's public address forwards to. Guest Paths
 * have no source allow-list, so the cluster source address changes nothing.
 *
 * Runs where MEMBER_NETWORK_APPS names the application (Ring 0's checks Job)
 * and with FULL_OIDC=1, which making the link as a member needs.
 */

const DOMAIN = process.env.DOMAIN!;
const APPS = (process.env.MEMBER_NETWORK_APPS ?? '').split(/[\s,]+/).filter(Boolean);
const PUBLIC_ENTRYPOINT = 'traefik.traefik-system.svc.cluster.local';
const FILES = `files.${DOMAIN}`;

/** GET with SNI and Host set to `host`, connecting to `connect` if given. */
function get(host: string, path: string, connect?: string): Promise<{ status: number; body: string; location?: string }> {
  return new Promise((resolve, reject) => {
    const request = https.request(
      { host: connect ?? host, port: 443, path, servername: host, headers: { host }, timeout: 15_000 },
      (response) => {
        let body = '';
        response.setEncoding('utf8');
        response.on('data', (chunk) => (body += chunk));
        response.on('end', () => resolve({ status: response.statusCode ?? 0, body, location: response.headers.location }));
      },
    );
    request.on('timeout', () => request.destroy(new Error(`timeout: ${host}${path}`)));
    request.on('error', reject);
    request.end();
  });
}

/** A signed-out browser whose `host` goes to the public entrypoint. */
async function guestBrowser(host: string): Promise<Browser> {
  const { address } = await dns.lookup(PUBLIC_ENTRYPOINT);
  return chromium.launch({ args: [`--host-resolver-rules=MAP ${host} ${address}`] });
}

test.describe('Nextcloud Guest Paths', () => {
  test.skip(!APPS.includes('nextcloud'), 'runs where MEMBER_NETWORK_APPS names nextcloud');
  test.skip(!FULL_OIDC, SKIP_REASON);
  test.describe.configure({ mode: 'serial' });

  const marker = `Gastzugang ${Date.now().toString(36)}`;
  const name = `guest-paths-${Date.now().toString(36)}.md`;
  let member: Page;
  let requestToken = '';
  let uid = '';
  let share = { id: '', token: '' };

  const ocsHeaders = () => ({ 'OCS-APIRequest': 'true', requesttoken: requestToken, Accept: 'application/json' });

  test.beforeAll(async ({ browser }) => {
    // The member's session, through `members` (in-cluster DNS).
    const context = await browser.newContext({ storageState: 'setup/.auth/alice.json' });
    member = await context.newPage();
    await member.goto(`https://${FILES}/apps/files/`);
    try {
      await member.locator('a[href*="oidc"], a[href*="oauth"], #oidc-login-button').first().click({ timeout: 5_000 });
    } catch {
      // Already redirected by the auto-login.
    }
    await member.waitForURL((url) => url.pathname.includes('/apps/'), { timeout: 60_000 });

    requestToken = (await (await member.request.get(`https://${FILES}/csrftoken`)).json()).token;
    const user = await member.request.get(`https://${FILES}/ocs/v2.php/cloud/user?format=json`, { headers: ocsHeaders() });
    uid = (await user.json()).ocs.data.id;

    const upload = await member.request.put(`https://${FILES}/remote.php/dav/files/${encodeURIComponent(uid)}/${name}`, {
      headers: { requesttoken: requestToken, 'Content-Type': 'text/markdown' },
      data: `# ${marker}\n\nShared with a guest by e2e spec 12.\n`,
    });
    expect(upload.status(), 'upload the note').toBeLessThan(300);

    const made = await member.request.post(`https://${FILES}/ocs/v2.php/apps/files_sharing/api/v1/shares?format=json`, {
      headers: ocsHeaders(),
      form: { path: `/${name}`, shareType: '3' },
    });
    expect(made.status(), 'make the public link').toBe(200);
    const data = (await made.json()).ocs.data;
    share = { id: String(data.id), token: data.token };
  });

  test.afterAll(async () => {
    if (!member) return;
    if (share.id) {
      await member.request.delete(`https://${FILES}/ocs/v2.php/apps/files_sharing/api/v1/shares/${share.id}`, { headers: ocsHeaders() });
    }
    if (uid) {
      await member.request.delete(`https://${FILES}/remote.php/dav/files/${encodeURIComponent(uid)}/${name}`, {
        headers: { requesttoken: requestToken },
      });
    }
    await member.context().close();
  });

  test('a share link opens for a guest, and the public entrypoint refuses none of its requests', async () => {
    const browser = await guestBrowser(FILES);
    try {
      const page = await browser.newPage();
      const refused: string[] = [];
      page.on('response', (response) => {
        const url = new URL(response.url());
        if (url.hostname === FILES && response.status() === 404) refused.push(url.pathname);
      });
      await page.goto(`https://${FILES}/s/${share.token}`, { waitUntil: 'networkidle' });
      await expect(page.getByText(marker).first()).toBeVisible({ timeout: 30_000 });
      expect(refused, 'paths the share page needs that the public entrypoint answers 404').toEqual([]);
    } finally {
      await browser.close();
    }
  });

  // The share page's download sends the guest on to the share's WebDAV path.
  test('a guest can download the shared file', async () => {
    const start = await get(FILES, `/s/${share.token}/download`, PUBLIC_ENTRYPOINT);
    expect(start.status).toBe(303);
    const target = new URL(start.location ?? '', `https://${FILES}`);
    expect(target.hostname).toBe(FILES);
    const download = await get(FILES, target.pathname + target.search, PUBLIC_ENTRYPOINT);
    expect(download.status).toBe(200);
    expect(download.body).toContain(marker);
  });

  test('sign-in, the apps and the member APIs answer 404 in public', async () => {
    const closed = [
      '/',
      '/login',
      '/index.php/login',
      '/apps/files/',
      '/apps/dashboard/',
      `/remote.php/dav/files/${encodeURIComponent(uid)}/`,
      '/remote.php/webdav/',
      '/ocs/v2.php/cloud/user',
      '/ocs/v2.php/apps/files_sharing/api/v1/shares',
      '/status.php',
      '/csrftoken',
    ];
    const answers: Record<string, number> = {};
    for (const path of closed) answers[path] = (await get(FILES, path, PUBLIC_ENTRYPOINT)).status;
    expect(answers).toEqual(Object.fromEntries(closed.map((path) => [path, 404])));
  });

  // The guest router matches paths only. Euro-Office's config endpoint is
  // public by path and must still hand out nothing without a share token.
  test("Euro-Office's config gives nothing away without the share token", async () => {
    const config = await get(FILES, '/ocs/v2.php/apps/eurooffice/api/v1/config/1', PUBLIC_ENTRYPOINT);
    expect(config.body).toContain('"error"');
    expect(config.body).not.toContain('"document"');
  });

  // status.php answers whatever the session; in public it is closed above.
  test('members still get the full application', async () => {
    expect((await get(FILES, '/status.php')).status).toBe(200);
  });
});
