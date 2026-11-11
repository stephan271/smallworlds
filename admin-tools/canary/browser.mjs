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

async function signin() {
  const { context, page, cdp, authenticatorId } = await memberContext(args.locale);
  // Keycloak refuses an assertion whose signature counter does not exceed the last
  // one it saw. The stored count can lag (a run that died, a sign-in by hand), so
  // start from the clock, which only moves forward.
  const signCount = Math.max(args.credential.signCount, Math.floor(Date.now() / 1000));
  await cdp.send("WebAuthn.addCredential", { authenticatorId, credential: { ...args.credential, signCount } });
  let cookie;
  if (args.app === "immich") {
    // Gallery launches OAuth by itself (oauthAutoLaunch) and finishes the callback
    // in its own page; its session is the immich_access_token cookie.
    await page.goto(`${args.origin}/auth/login?autoLaunch=1`);
    await page.waitForURL(`${args.keycloak}/**`);
    await keycloakSignIn(page, cdp, args.username);
    // Done when the session cookie exists. Where Gallery lands differs: a new
    // account goes to /auth/onboarding, a known one to /photos.
    const deadline = Date.now() + 120000;
    while (!cookie && Date.now() < deadline) {
      cookie = (await context.cookies(args.origin)).find((c) => c.name === "immich_access_token")?.value;
      if (!cookie) await page.waitForTimeout(500);
    }
  } else {
    throw new Error(`no sign-in for app ${args.app}`);
  }
  if (!cookie) throw new Error(`${args.app}: signed in, but no session cookie`);
  const { credentials } = await cdp.send("WebAuthn.getCredentials", { authenticatorId });
  return { session: cookie, signCount: credentials[0].signCount };
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
