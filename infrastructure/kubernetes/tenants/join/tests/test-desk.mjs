/* Browser test for the Invitation Desk (design §4, §11).
 *
 * The browser works on https://join.example.test as it would in production —
 * so Secure cookies, SameSite and the clipboard behave for real — and every
 * request to that origin is handed to the local listeners: /desk/* to the desk
 * port, the rest to the public one. identity.example.test is the fake Keycloak.
 *
 * Walks: sign-in, issuing one invitation (QR, message, copy), several at once
 * and printing their cards, withdrawing, the language switch, a new passkey for
 * a member — and that a code issued here opens on the member's page.
 *
 * usage: node test-desk.mjs '<json line printed by serve.py>'
 */
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");

const { url, deskUrl, fakeUrl } = JSON.parse(process.argv[2] || "{}");
if (!deskUrl) {
  console.error("usage: test-desk.mjs '<json from serve.py>'");
  process.exit(2);
}
const JOIN = "https://join.example.test";

const failures = [];
const check = (label, condition, detail = "") => {
  if (condition) console.log(`  ok    ${label}`);
  else { failures.push(label); console.log(`  FAIL  ${label}${detail ? " — " + detail : ""}`); }
};

const browser = await chromium.launch();
const context = await browser.newContext({ locale: "de-DE", viewport: { width: 1280, height: 1000 } });
await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: JOIN });

// Chromium follows a redirect in a fulfilled response by itself, past the
// routing, so a redirect is handed back as a page that moves on — whose next
// request is routed again. Its Set-Cookie headers still apply.
const bounce = (route, location, headers = {}) => {
  const safe = location.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
  return route.fulfill({ status: 200, headers, contentType: "text/html",
                         body: `<meta http-equiv="refresh" content="0;url=${safe}">` });
};
const forward = async (route, target) => {
  const response = await route.fetch({ url: target, maxRedirects: 0 });
  const location = response.headers()["location"];
  if (response.status() >= 300 && response.status() < 400 && location) {
    const headers = {};
    for (const { name, value } of response.headersArray()) {
      const key = name.toLowerCase();
      if (["location", "content-length", "content-type"].includes(key)) continue;
      headers[key] = headers[key] ? `${headers[key]}\n${value}` : value;
    }
    return bounce(route, location, headers);
  }
  return route.fulfill({ response });
};
await context.route(`${JOIN}/**`, (route) => {
  const requested = new URL(route.request().url());
  const base = requested.pathname.startsWith("/desk") ? deskUrl : url;
  return forward(route, base + requested.pathname + requested.search);
});
await context.route("https://identity.example.test/**", (route) => {
  const requested = new URL(route.request().url());
  if (requested.pathname.endsWith("/openid-connect/auth")) {
    return forward(route, fakeUrl + requested.pathname + requested.search);
  }
  if (requested.pathname.endsWith("/openid-connect/logout")) {
    return bounce(route, requested.searchParams.get("post_logout_redirect_uri"));
  }
  return route.fulfill({ contentType: "text/html", body: "<title>keycloak</title>keycloak" });
});

const page = await context.newPage();
const problems = [];
page.on("pageerror", (error) => problems.push("pageerror: " + error.message));
page.on("console", (message) => {
  if (message.type() === "error" && !message.text().startsWith("Failed to load resource")) {
    problems.push("console: " + message.text());
  }
});

// ---------------------------------------------------------------------------
console.log("desk — sign-in");
await page.goto(`${JOIN}/desk/`);
await page.waitForURL(`${JOIN}/desk/`);
await page.waitForFunction(() => document.getElementById("me").textContent.length > 0);
check("signs the Operator in through Keycloak", (await page.textContent("#me")) === "operator");
check("in the browser's language", (await page.textContent("#new h2")) === "Neue Einladung");
const cookies = await context.cookies(`${JOIN}/desk/`);
const session = cookies.find((c) => c.name === "join_desk");
check("with a strict, http-only session cookie",
      session && session.httpOnly && session.secure && session.sameSite === "Strict");

// ---------------------------------------------------------------------------
console.log("desk — one invitation");
await page.fill("#label", "Elena");
await page.check('input[name="locale"][value="de"]');
await page.click("#new-submit");
await page.waitForSelector("#issued:not([hidden])");
check("names the invitation", (await page.textContent("#issued-title")) === "Einladung für Elena");
const code = await page.textContent("#issued-code");
check("shows the code", /^[0-9A-Z]{5}-[0-9A-Z]{5}$/.test(code), code);
check("shows the link", (await page.textContent("#issued-link")) === `${JOIN}/#${code}`);
check("draws a QR code", (await page.locator("#issued-qr svg path").count()) === 1);
const text = await page.inputValue("#issued-message");
check("writes the message in the invitation's language",
      text.startsWith("Hallo Elena! Du bist zu example.test eingeladen.") && text.includes(`${JOIN}/#${code}`), text);
await page.click("#copy-message");
await page.waitForSelector("#toast:not([hidden])");
check("copies the message", (await page.evaluate(() => navigator.clipboard.readText())) === text);
await page.waitForSelector("#invitation-rows tr");
check("lists it as open",
      (await page.textContent("#invitation-rows tr:first-child .badge")) === "Offen");

// The code just issued works on the member's page.
const member = await context.newPage();
await member.goto(`${JOIN}/#${code}`);
await member.waitForSelector('[data-view="welcome"]:not([hidden])');
check("the code opens the member's page", (await member.textContent('[data-view="welcome"] h1'))
  .startsWith("Willkommen"));
await member.close();

// ---------------------------------------------------------------------------
console.log("desk — several at once, and their cards");
await page.click("#tab-many");
check("switches to a list of names", await page.isVisible("#labels") && !(await page.isVisible("#label")));
await page.fill("#labels", "Ben\nCarla\n\nDavid\n");
await page.check('input[name="locale"][value="en"]');
await page.click("#new-submit");
await page.waitForSelector("#issued-many:not([hidden])");
check("issues one per name", (await page.locator("#issued-list li").count()) === 3);
check("titles the batch", (await page.textContent("#issued-title")) === "3 Einladungen");
await page.evaluate(() => {
  window.print = () => {
    const cards = [...document.querySelectorAll("#print-area .print-card")];
    window.__printed = { count: cards.length, qrs: cards.filter((c) => c.querySelector("svg")).length,
                         first: cards[0]?.innerText || "" };
  };
});
await page.click("#print-all");
const printed = await page.evaluate(() => window.__printed);
check("prints a card each, with a QR code", printed && printed.count === 3 && printed.qrs === 3,
      JSON.stringify(printed));
check("cards carry both languages and the name",
      printed && printed.first.includes("Ben") && printed.first.includes("Scanne") && printed.first.includes("Scan the QR"));

// ---------------------------------------------------------------------------
console.log("desk — withdrawing and the language switch");
const elenaRow = page.locator("#invitation-rows tr", { hasText: "Elena" });
await elenaRow.locator('button[data-action="revoke"]').click();
await page.waitForSelector("#revoke-dialog[open]");
check("asks before withdrawing", await page.isVisible("#revoke-dialog"));
check("offers no account deletion for an unstarted invitation", !(await page.isVisible("#revoke-delete-row")));
await page.click('#revoke-dialog button[value="confirm"]');
await page.waitForFunction(() => [...document.querySelectorAll("#invitation-rows tr")]
  .some((r) => r.textContent.includes("Elena") && r.textContent.includes("Zurückgezogen")));
check("withdraws it", true);

await page.click('[data-lang="en"]');
check("the Desk switches language", (await page.textContent("#list h2")) === "All invitations");
check("the list follows", (await elenaRow.locator(".badge").textContent()) === "Withdrawn");

// ---------------------------------------------------------------------------
console.log("desk — a new passkey for a member");
await page.fill("#member-search", "dorian");
await page.waitForSelector("#members .member");
check("finds the member", (await page.textContent("#members .member strong")) === "dorian");
await page.click("#members .member");
await page.waitForSelector("#recovery-form:not([hidden])");
check("removing old passkeys is the default", await page.isChecked("#remove-passkeys"));
await page.click("#recovery-submit");
await page.waitForFunction(() => document.getElementById("issued-title").textContent.includes("dorian"));
check("issues a recovery invitation", (await page.textContent("#issued-title")) === "New passkey for dorian");
check("its message is a recovery message, in the chosen language",
      (await page.inputValue("#issued-message")).startsWith("Hallo dorian! Mit diesem Link"));
check("and says the old passkeys are gone", await page.isVisible("#issued-removed"));

// ---------------------------------------------------------------------------
console.log("desk — narrow screen and sign-out");
await page.setViewportSize({ width: 360, height: 800 });
const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
check("no horizontal scrolling at 360 px", overflow <= 0, `${overflow}px too wide`);
await page.click('a[href="/desk/logout"]');
await page.waitForURL(`${JOIN}/desk/signed-out`);
// fetch from the page, so the request goes through the same routing.
const after = await page.evaluate(() => fetch("/desk/api/me").then((r) => r.status));
check("signing out ends the session", after === 401);

check("no console errors or CSP violations", problems.length === 0, problems.join(" | "));
await browser.close();
if (failures.length) {
  console.log(`\n${failures.length} failed`);
  process.exit(1);
}
console.log("\nall passed");
