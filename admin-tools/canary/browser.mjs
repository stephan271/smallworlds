/* The browser half of seed.py: what only a member's browser can do.
 *
 *   node browser.mjs join   '<json>'   an invitation, walked like a member: join page,
 *                                      passkey, recovery codes. Prints the passkey
 *                                      (private key included) and the codes.
 *   node browser.mjs signin '<json>'   signs a member in to an application with the
 *                                      stored passkey and prints the application's
 *                                      session cookie and the passkey's new signCount.
 *
 * One JSON argument in, one JSON line out (prefixed "RESULT "), so seed.py never
 * scrapes prose. The passkey is a CDP virtual authenticator's credential; restoring
 * it into a fresh authenticator is what lets a script sign in exactly as a member
 * does, through the smallworlds login theme and nothing else. */
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");

const [command, raw] = process.argv.slice(2);
const args = JSON.parse(raw || "{}");
const browser = await chromium.launch();
let current;  // the page in use, so a failure can say where it stopped

async function memberContext(locale) {
  const context = await browser.newContext({ locale: locale === "en" ? "en-GB" : "de-DE" });
  const page = await context.newPage();
  current = page;
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true,
               hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  return { context, page, cdp, authenticatorId };
}

async function join() {
  const { page, cdp, authenticatorId } = await memberContext(args.locale);
  await page.goto(`${args.join}/#${args.code}`);
  await page.waitForSelector('[data-view="welcome"]:not([hidden])');
  await page.click("#start");
  await page.fill("#username", args.username);
  await page.waitForSelector("#username-status:not(:empty)");
  await page.fill("#firstName", args.firstName);
  await page.fill("#lastName", args.lastName);
  await page.fill("#email", args.email);
  await Promise.all([page.waitForURL(`${args.keycloak}/**`), page.click("#profile-submit")]);
  await page.waitForSelector("#registerWebAuthn");
  await page.click("#registerWebAuthn");
  await page.waitForSelector("#kc-recovery-codes-list");
  const recoveryCodes = (await page.locator("#kc-recovery-codes-list li").allTextContents())
    .map((text) => text.trim());
  const { credentials } = await cdp.send("WebAuthn.getCredentials", { authenticatorId });
  if (credentials.length !== 1) throw new Error(`expected one passkey, found ${credentials.length}`);
  await page.check("#kcRecoveryCodesConfirmationCheck");
  await Promise.all([page.waitForURL(`${args.join}/done/**`), page.click("#saveRecoveryAuthnCodesBtn")]);
  return { credential: credentials[0], recoveryCodes };
}

// Keycloak's sign-in for a member who has a passkey and no password: username,
// then the passkey ceremony, which the virtual authenticator answers. The button
// gets its handler from a module script that loads after the page, so a click
// can land before there is anything to run: click again until the authenticator
// has actually signed.
async function keycloakSignIn(page, cdp, username) {
  let asserted = false;
  cdp.on("WebAuthn.credentialAsserted", () => { asserted = true; });
  await page.waitForSelector("#username");
  await page.fill("#username", username);
  await page.click("#kc-login");
  const button = page.locator("#authenticateWebAuthnButton");
  await button.waitFor({ timeout: 60000 });
  await page.waitForLoadState("networkidle").catch(() => {});
  const deadline = Date.now() + 60000;
  while (!asserted && Date.now() < deadline) {
    if (await button.isVisible().catch(() => false)) await button.click().catch(() => {});
    await page.waitForTimeout(2000);
  }
  if (!asserted) throw new Error("the passkey was never asked for");
}

// Open an application's sign-in, and answer Keycloak with the passkey when it
// asks; it does not ask again while the Keycloak session lasts.
async function through(page, cdp, url, host) {
  await page.goto(url, { waitUntil: "commit" });
  await page.waitForURL((u) => u.hostname === host || u.href.startsWith(args.keycloak), { timeout: 60000 });
  if (page.url().startsWith(args.keycloak)) await keycloakSignIn(page, cdp, args.username);
  await page.waitForURL((u) => u.hostname === host && !u.pathname.startsWith("/login")
                          && !u.pathname.includes("/oauth2/") && !u.pathname.includes("user_oidc"),
                        { timeout: 120000 });
  await page.waitForLoadState("networkidle").catch(() => {});
}

// Gallery launches OAuth by itself (oauthAutoLaunch) and finishes the callback in
// its own page; its session is the immich_access_token cookie. Done when the
// cookie exists: a new account lands on /auth/onboarding, a known one on /photos.
async function immich(context, page, cdp) {
  await page.goto(`${args.origin}/auth/login?autoLaunch=1`);
  await page.waitForURL(`${args.keycloak}/**`);
  await keycloakSignIn(page, cdp, args.username);
  let cookie;
  const deadline = Date.now() + 120000;
  while (!cookie && Date.now() < deadline) {
    cookie = (await context.cookies(args.origin)).find((c) => c.name === "immich_access_token")?.value;
    if (!cookie) await page.waitForTimeout(500);
  }
  if (!cookie) throw new Error("immich: signed in, but no session cookie");
  return { session: cookie };
}

// Nextcloud: an app password, made the way Settings → Security makes one. Members
// have no Nextcloud password, so this is how any client of theirs gets in.
async function nextcloud(page, cdp) {
  await through(page, cdp, `${args.origin}/`, new URL(args.origin).hostname);
  return await page.evaluate(async (name) => {
    const response = await fetch("/index.php/settings/personal/authtokens", {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json", requesttoken: document.head.dataset.requesttoken },
      body: JSON.stringify({ name }),
    });
    if (!response.ok) throw new Error(`nextcloud app password: HTTP ${response.status}`);
    const made = await response.json();
    return { uid: document.head.dataset.user, loginName: made.loginName, token: made.token };
  }, args.tokenName);
}

// Forgejo: an access token from Settings → Applications → New access token.
async function forgejo(page, cdp) {
  await through(page, cdp, `${args.origin}/user/oauth2/smallworlds`, new URL(args.origin).hostname);
  await page.goto(`${args.origin}/user/settings/applications/tokens/new`);
  await page.fill("#name", args.tokenName);
  await page.check("#resource-all");
  for (const scope of ["repository", "issue", "user"]) {
    await page.selectOption(`#access-token-scope-${scope}`, `write:${scope}`);
  }
  await Promise.all([page.waitForNavigation(), page.locator("#scoped-access-form button.primary").last().click()]);
  const text = await page.locator(".flash-message, .ui.message, .flash-info").allTextContents();
  const token = text.join(" ").match(/\b[0-9a-f]{40}\b/)?.[0];
  if (!token) throw new Error(`forgejo: no token on ${page.url()}: ${text.join(" ").slice(0, 200)}`);
  return { token };
}

// Mail: members sign in to Bulwark (webmail), whose page then talks JMAP to
// Stalwart with a Keycloak bearer token for the audience "stalwart". That token
// is what any JMAP client of the member holds; it lives minutes, so the seeder
// uses it at once and signs in again for the next run.
async function stalwart(page, cdp) {
  let token;
  page.on("request", (request) => {
    const auth = request.headers()["authorization"] || "";
    if (!token && auth.startsWith("Bearer ") && new URL(request.url()).pathname.startsWith("/jmap")) {
      token = auth.slice(7);
    }
  });
  await page.goto(`${args.origin}/`, { waitUntil: "commit" });
  await page.waitForURL((u) => u.pathname.includes("/login") || u.href.startsWith(args.keycloak), { timeout: 60000 });
  if (!page.url().startsWith(args.keycloak)) {
    await page.getByRole("button", { name: /SSO|Single|Anmelden|Sign in|Login|SmallWorlds/i }).first().click();
    await page.waitForURL(`${args.keycloak}/**`, { timeout: 60000 });
  }
  await keycloakSignIn(page, cdp, args.username);
  const deadline = Date.now() + 120000;
  while (!token && Date.now() < deadline) await page.waitForTimeout(500);
  if (!token) throw new Error("stalwart: signed in to webmail, but it never called JMAP");
  return { token };
}

async function signin() {
  const { context, page, cdp, authenticatorId } = await memberContext(args.locale);
  // Keycloak refuses an assertion whose signature counter does not exceed the last
  // one it saw. The stored count can lag (a run that died, a sign-in by hand), so
  // start from the clock, which only moves forward.
  const signCount = Math.max(args.credential.signCount, Math.floor(Date.now() / 1000));
  await cdp.send("WebAuthn.addCredential", { authenticatorId, credential: { ...args.credential, signCount } });
  const apps = { immich: () => immich(context, page, cdp), nextcloud: () => nextcloud(page, cdp),
                 forgejo: () => forgejo(page, cdp), stalwart: () => stalwart(page, cdp) };
  if (!apps[args.app]) throw new Error(`no sign-in for app ${args.app}`);
  const result = await apps[args.app]();
  const { credentials } = await cdp.send("WebAuthn.getCredentials", { authenticatorId });
  return { ...result, signCount: credentials[0].signCount };
}

try {
  const result = command === "join" ? await join()
               : command === "signin" ? await signin()
               : (() => { throw new Error(`unknown command ${command}`); })();
  console.log("RESULT " + JSON.stringify(result));
} catch (error) {
  console.error(error.stack || String(error));
  if (current && args.failureScreenshot) {
    console.error(`stopped at ${current.url()}`);
    await current.screenshot({ path: args.failureScreenshot, fullPage: true }).catch(() => {});
    console.error(`screenshot: ${args.failureScreenshot}`);
  }
  process.exitCode = 1;
} finally {
  await browser.close();
}
