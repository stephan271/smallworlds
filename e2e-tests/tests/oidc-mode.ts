import { request as pwRequest, expect, type Browser } from '@playwright/test';

/**
 * OIDC test depth control.
 *
 * Full OIDC login roundtrips require the APPS to trust the TLS certificate
 * of identity.<domain> for their server-side discovery/token calls. That
 * holds in production (Let's Encrypt) but is structurally impossible on
 * ephemeral staging clusters (self-signed issuer; real certs would exhaust
 * Let's Encrypt duplicate-certificate rate limits).
 *
 * - default (staging): shallow checks assert each app redirects into the
 *   Keycloak authorize endpoint — proving client config, secrets, issuer
 *   URL, in-cluster DNS and the app's OIDC wiring.
 * - FULL_OIDC=1 (production verification): the complete login roundtrips run.
 */
export const FULL_OIDC = process.env.FULL_OIDC === '1';

export const SKIP_REASON =
  'Full OIDC roundtrip needs app-trusted certificates — run with FULL_OIDC=1 against production';

/**
 * Follow an app's login redirect chain WITHOUT any Keycloak SSO cookies
 * (fresh request context) and assert it ends on the Keycloak authorize page.
 */
export async function expectRedirectIntoKeycloak(browser: Browser, startUrl: string) {
  // Fresh context: no Keycloak SSO cookies, so the chain stops AT Keycloak's
  // login page instead of bouncing back into the app. A real browser handles
  // session-cookie priming that raw HTTP requests trip over (Nextcloud 429s
  // repeated cookie-less requests via its brute-force protection).
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  try {
    const page = await ctx.newPage();
    // Any request against the identity host proves the app initiated the
    // OIDC flow. A request listener is the only reliable capture: redirect
    // hops fire no navigation events, and Keycloak's passkey page bounces
    // straight back in a headless browser (no WebAuthn authenticator), so
    // the identity URL is often only transient.
    let reachedIdentity = false;
    page.on('request', (req) => {
      if (new URL(req.url()).hostname.startsWith('identity.')) reachedIdentity = true;
    });
    await page.goto(startUrl, { waitUntil: 'domcontentloaded' }).catch(() => {});
    for (let i = 0; i < 20 && !reachedIdentity; i++) {
      await page.waitForTimeout(500);
    }
    expect(reachedIdentity, `no request to identity.<domain> after loading ${startUrl}`).toBe(true);
  } finally {
    await ctx.close();
  }
}

/**
 * Nextcloud variant: user_oidc performs server-side DISCOVERY before it
 * even issues the redirect, so in staging the redirect itself can never
 * happen. Assert one level lower: the login page (?direct=1 bypasses the
 * auto-redirect) renders the registered OIDC provider link — proving the
 * provider is configured, without any server-side provider call.
 */
export async function expectNextcloudOidcProvider(browser: Browser, baseUrl: string) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  try {
    const page = await ctx.newPage();
    await page.goto(`${baseUrl}/login?direct=1`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('a[href*="user_oidc"]').first()).toBeVisible({ timeout: 15_000 });
  } finally {
    await ctx.close();
  }
}

/**
 * SPA variant (Immich): assert OAuth is enabled in the server config.
 * (The authorize API would trigger server-side discovery, which needs
 * app-trusted certificates — exactly what staging cannot provide.)
 */
export async function expectOauthEnabled(apiBase: string) {
  const ctx = await pwRequest.newContext({ ignoreHTTPSErrors: true });
  try {
    const resp = await ctx.get(`${apiBase}/api/server/features`);
    expect(resp.ok()).toBeTruthy();
    const features = await resp.json();
    expect(features.oauth).toBe(true);
  } finally {
    await ctx.dispose();
  }
}
