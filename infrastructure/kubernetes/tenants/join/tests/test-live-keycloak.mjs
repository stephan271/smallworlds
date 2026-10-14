/* The invitation path against a real Keycloak, with a virtual passkey.
 * Driven by live-keycloak.sh, which starts both and passes one JSON argument.
 *
 * Asserts what docs/adr/0059 promises: no confirmation page, a passkey page
 * that names the account and asks no questions, recovery codes without the
 * sessions checkbox, and a member who ends on join's done page already signed
 * in — in German, and in English. Takes a screenshot of every page. */
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");

const { keycloak, join, codes, screenshots } = JSON.parse(process.argv[2]);
const failures = [];
const check = (label, condition, detail = "") => {
  if (condition) console.log(`  ok    ${label}`);
  else { failures.push(label); console.log(`  FAIL  ${label}${detail ? " — " + detail : ""}`); }
};

const browser = await chromium.launch();
const problems = [];

async function walk(lang, code, username, colorScheme) {
  console.log(`invitation in ${lang} (${colorScheme})`);
  const context = await browser.newContext({
    locale: lang === "de" ? "de-DE" : "en-GB", colorScheme, viewport: { width: 430, height: 900 },
  });
  const page = await context.newPage();
  page.on("pageerror", (error) => problems.push(`${lang} pageerror: ${error.message}`));
  page.on("dialog", (dialog) => { problems.push(`${lang} dialog: ${dialog.message()}`); dialog.dismiss(); });

  // A platform authenticator that says yes, so the passkey ceremony completes.
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: { protocol: "ctap2", transport: "internal", hasResidentKey: true,
               hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });

  await page.goto(`${join}/#${code}`);
  await page.waitForSelector('[data-view="welcome"]:not([hidden])');
  await page.screenshot({ path: `${screenshots}/${lang}-1-welcome.png`, fullPage: true });
  await page.click("#start");
  await page.fill("#username", username);
  await page.waitForSelector("#username-status:not(:empty)");
  await page.fill("#firstName", "Test");
  await page.fill("#lastName", "Member");
  await page.fill("#email", `${username}@example.org`);
  await page.screenshot({ path: `${screenshots}/${lang}-2-form.png`, fullPage: true });

  await Promise.all([page.waitForURL(`${keycloak}/**`), page.click("#profile-submit")]);
  await page.waitForSelector("#registerWebAuthn");
  check("no confirmation page: straight to the passkey", page.url().includes("webauthn-register-passwordless"),
        page.url());
  check("Keycloak speaks the member's language", (await page.getAttribute("html", "lang")) === lang);
  check("the passkey page names the account", (await page.textContent(".sw-account-name")).trim() === username);
  check("no sessions checkbox on the passkey page", (await page.locator("#logout-sessions").count()) === 0);
  await page.screenshot({ path: `${screenshots}/${lang}-3-passkey.png`, fullPage: true });

  await page.click("#registerWebAuthn");
  await page.waitForSelector("#kc-recovery-codes-list");
  const { credentials } = await cdp.send("WebAuthn.getCredentials", { authenticatorId });
  check("a passkey was registered", credentials.length === 1);
  check("twelve recovery codes", (await page.locator("#kc-recovery-codes-list li").count()) === 12);
  check("no sessions checkbox on the codes page", !(await page.isVisible("#logout-sessions")));
  await page.screenshot({ path: `${screenshots}/${lang}-4-codes.png`, fullPage: true });

  await page.check("#kcRecoveryCodesConfirmationCheck");
  await Promise.all([page.waitForURL(`${join}/done/${lang}**`), page.click("#saveRecoveryAuthnCodesBtn")]);
  await page.waitForFunction(() => document.querySelector("h1").textContent.length > 0);
  check("ends on join's done page", ["Geschafft!", "All done!"].includes(await page.textContent("h1")));
  check("the authorization code is wiped from the address bar", !page.url().includes("code="), page.url());
  await page.screenshot({ path: `${screenshots}/${lang}-5-done.png`, fullPage: true });

  // Signed in already: a silent authorization request succeeds.
  const silent = `${keycloak}/realms/smallworlds/protocol/openid-connect/auth?client_id=join&response_type=code`
    + `&scope=openid&prompt=none&redirect_uri=${encodeURIComponent(`${join}/done/${lang}`)}`;
  const response = await page.request.get(silent, { maxRedirects: 0 });
  const location = response.headers()["location"] || "";
  check("the member arrives signed in", location.includes("code=") && !location.includes("error="), location);

  // The same invitation is used up now.
  await page.goto(`${join}/#${code}`);
  await page.reload();
  await page.waitForSelector('[data-view="problem"]:not([hidden])');
  check("the invitation counts as used", /verwendet|used/.test(await page.textContent("#problem-text")));
  await context.close();
}

async function signInPage(lang, colorScheme) {
  const context = await browser.newContext({
    locale: lang === "de" ? "de-DE" : "en-GB", colorScheme, viewport: { width: 1280, height: 860 },
  });
  const page = await context.newPage();
  await page.goto(`${keycloak}/realms/smallworlds/account/`);
  await page.waitForSelector("#kc-page-title");
  await page.screenshot({ path: `${screenshots}/${lang}-signin-${colorScheme}.png`, fullPage: true });
  check(`the sign-in page uses the theme (${lang}, ${colorScheme})`,
        (await page.locator('link[href*="smallworlds.css"]').count()) === 1);
  await context.close();
}

await walk("de", codes.de, "anna", "light");
await walk("en", codes.en, "ben", "dark");
console.log("sign-in page");
await signInPage("de", "light");
await signInPage("en", "dark");

check("no browser prompts or page errors", problems.length === 0, problems.join(" | "));
await browser.close();
if (failures.length) { console.log(`\n${failures.length} failed`); process.exit(1); }
console.log("\nall passed");
