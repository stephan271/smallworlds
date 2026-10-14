/* Browser test for the join page (design §11).
 *
 * The page is the whole experience for an invited member, so "the API answers"
 * is not evidence that it works. This walks it as a member would: in the
 * invitation's language, switching language, choosing a username with live
 * feedback, fixing a form, and arriving at Keycloak with kc_locale set — plus
 * the resumed, recovery and failed cases and the done page.
 *
 * Any console error fails the run, which is how a CSP violation (an inline
 * script or style slipping in) shows up.
 *
 * usage: node test-interface.mjs '<json line printed by serve.py>'
 */
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");

const { url, codes } = JSON.parse(process.argv[2] || "{}");
if (!url) {
  console.error("usage: test-interface.mjs '<json from serve.py>'");
  process.exit(2);
}

const failures = [];
const check = (label, condition, detail = "") => {
  if (condition) console.log(`  ok    ${label}`);
  else { failures.push(label); console.log(`  FAIL  ${label}${detail ? " — " + detail : ""}`); }
};

const browser = await chromium.launch();
const problems = [];

async function newPage(locale = "en-US", viewport = { width: 390, height: 844 }) {
  const context = await browser.newContext({ locale, viewport });
  // Keycloak and the dashboard are not here; stand in for them.
  await context.route("https://identity.example.test/**", (route) =>
    route.fulfill({ contentType: "text/html", body: "<title>keycloak</title>keycloak" }));
  const page = await context.newPage();
  page.on("pageerror", (error) => problems.push("pageerror: " + error.message));
  page.on("console", (message) => {
    // A 4xx answer is part of the flow (a taken name, an unknown code); Chrome
    // logs each as "Failed to load resource". Anything else is a real error.
    if (message.type() === "error" && !message.text().startsWith("Failed to load resource")) {
      problems.push("console: " + message.text());
    }
  });
  return page;
}

const view = (page) => page.locator("[data-view]:not([hidden])").getAttribute("data-view");
const heading = (page) => page.locator("[data-view]:not([hidden]) h1").textContent();
const waitView = (page, name) => page.waitForSelector(`[data-view="${name}"]:not([hidden])`);

// ---------------------------------------------------------------------------
console.log("join page — a new member, invited in German, browsing in English");
{
  const page = await newPage("en-US");
  await page.goto(`${url}/#${codes.open_de}`);
  await waitView(page, "welcome");
  check("opens in the invitation's language", (await heading(page)).startsWith("Willkommen bei"));
  check("names the community", (await page.textContent("#host")) === "127.0.0.1");
  check("the code is not in any request line",
        !(await page.evaluate(() => performance.getEntriesByType("resource")
          .some((e) => e.name.includes("#") || /[A-Z0-9]{5}-[A-Z0-9]{5}/.test(e.name)))));

  await page.click('[data-lang="en"]');
  check("the switch changes the language", (await heading(page)).startsWith("Welcome to"));
  check("the switch reports its state",
        (await page.getAttribute('[data-lang="en"]', "aria-pressed")) === "true");

  await page.click("#start");
  await waitView(page, "form");

  await page.fill("#username", "Admin");
  check("lowercases while typing", (await page.inputValue("#username")) === "admin");
  await page.waitForSelector("text=This name is reserved");
  check("refuses a reserved name", true);

  await page.fill("#username", "taken");
  await page.waitForSelector("text=This name is taken");
  check("refuses a taken name", true);

  await page.fill("#username", "anna");
  await page.waitForSelector("text=“anna” is available.");
  check("confirms a free name", true);

  await page.click('[data-lang="de"]');
  check("live messages follow a language switch",
        (await page.textContent("#username-status")) === "„anna“ ist frei.");
  await page.click('[data-lang="en"]');

  await page.click("#profile-submit");
  check("an empty form is refused field by field",
        (await page.locator(".field.invalid").count()) === 3);
  check("and says why", (await page.textContent("#form-status")) === "Please check the marked fields.");

  await page.fill("#firstName", "Anna");
  await page.fill("#lastName", "Muster");
  await page.fill("#email", "taken@example.org");
  await page.click("#profile-submit");
  await page.waitForSelector("text=This address already belongs to an account.");
  check("a server-side refusal lands on its field", true);

  await page.fill("#email", "anna@example.org");
  await Promise.all([
    page.waitForURL("https://identity.example.test/**"),
    page.click("#profile-submit"),
  ]);
  const landed = new URL(page.url());
  check("arrives at Keycloak's action-token endpoint",
        landed.pathname === "/realms/smallworlds/login-actions/action-token");
  check("in the language the member chose", landed.searchParams.get("kc_locale") === "en");

  // Coming back to the same link resumes rather than starting over.
  await page.goto(`${url}/#${codes.open_de}`);
  await waitView(page, "resume");
  check("a started invitation resumes", (await heading(page)) === "Welcome back, anna!");
  await page.context().close();
}

// ---------------------------------------------------------------------------
console.log("join page — the other ways in");
{
  const page = await newPage("de-DE");
  await page.goto(`${url}/`);
  await waitView(page, "enter");
  check("without a code it asks for one, in the browser's language",
        (await heading(page)) === "Deine Einladung");
  await page.fill("#code", codes.open_en.toLowerCase().replace("-", " "));
  await page.click("#enter-form button");
  await waitView(page, "welcome");
  check("a typed code in any form is accepted", true);
  check("the invitation's language wins over the browser's",
        (await heading(page)).startsWith("Welcome to"));

  await page.goto(`${url}/#AAAAA-AAAAA`);
  await waitView(page, "problem");
  check("an unknown code is explained",
        (await page.textContent("#problem-text")).startsWith("This invitation code does not exist")
        || (await page.textContent("#problem-text")).startsWith("Diesen Einladungscode gibt es nicht"));

  await page.goto(`${url}/#${codes.expired}`);
  await page.reload();
  await waitView(page, "problem");
  check("an expired invitation is explained",
        /abgelaufen|expired/.test(await page.textContent("#problem-text")));
  check("and says who can help", await page.isVisible("#problem-ask"));

  await page.goto(`${url}/#${codes.started}`);
  await page.reload();
  await waitView(page, "resume");
  check("a started invitation greets by username", (await heading(page)).includes("carla"));

  await page.goto(`${url}/#${codes.recovery}`);
  await page.reload();
  await waitView(page, "recovery");
  check("a recovery invitation skips the form", (await heading(page)).includes("dora"));
  await Promise.all([
    page.waitForURL("https://identity.example.test/**"),
    page.click('[data-view="recovery"] [data-continue]'),
  ]);
  check("and goes straight to Keycloak", new URL(page.url()).searchParams.has("key"));
  await page.context().close();
}

// ---------------------------------------------------------------------------
console.log("done page");
{
  const page = await newPage("en-US", { width: 1200, height: 900 });
  await page.goto(`${url}/done/de`);
  await page.waitForFunction(() => document.querySelector("h1").textContent.length > 0);
  check("speaks the language in its path", (await page.textContent("h1")) === "Geschafft!");
  await page.waitForFunction(() => document.getElementById("dashboard").href.startsWith("https://dashboard."));
  check("links to the dashboard",
        (await page.getAttribute("#dashboard", "href")) === "https://dashboard.example.test");
  await page.context().close();
}

// ---------------------------------------------------------------------------
console.log("layout at phone width");
{
  const page = await newPage("en-US", { width: 320, height: 640 });
  await page.goto(`${url}/#${codes.open_en}`);
  await waitView(page, "welcome");
  await page.click("#start");
  await waitView(page, "form");
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  check("no horizontal scrolling at 320 px", overflow <= 0, `${overflow}px too wide`);
  await page.context().close();
}

check("no console errors or CSP violations", problems.length === 0, problems.join(" | "));
await browser.close();
if (failures.length) {
  console.log(`\n${failures.length} failed`);
  process.exit(1);
}
console.log("\nall passed");
