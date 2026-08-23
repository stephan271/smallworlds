/* Browser tests for the setup wizard's interface.
 *
 * The wizard is the one part of SmallWorlds whose whole purpose is what a
 * person sees, so "the API returns a plan" is not evidence that the wizard
 * works. This walks the five panels the way a first-time operator would:
 * refuses to continue with nothing filled in, swaps the conditional fields,
 * warns about webmail without mail, and renders the plan.
 *
 * Uses the Playwright already installed for e2e-tests/. Skipped, not failed,
 * when it is absent — see admin-tools/test-setup-wizard.sh.
 */

// ESM resolves a bare import from this file's own directory, not the working
// directory, so admin-tools/ cannot reach e2e-tests/node_modules by name. The
// wrapper passes the resolved path instead of a second Playwright install.
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");

const [url] = process.argv.slice(2);
if (!url) {
  console.error("usage: test-interface.mjs <wizard url>");
  process.exit(2);
}

const failures = [];
const check = (label, condition, detail = "") => {
  if (condition) console.log(`  ok    ${label}`);
  else { failures.push(label); console.log(`  FAIL  ${label}${detail ? " — " + detail : ""}`); }
};

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 900, height: 1200 } });
const problems = [];
page.on("pageerror", (error) => problems.push("pageerror: " + error.message));
page.on("console", (message) => {
  if (message.type() === "error") problems.push("console: " + message.text());
});

const panel = () => page.locator(".panel:not([hidden])");
const openPanel = () => panel().getAttribute("data-panel");

console.log("interface — the five panels");

await page.goto(url, { waitUntil: "networkidle" });
await page.waitForSelector('.step[data-state="current"]');
check("the wizard loads", (await openPanel()) === "community");
check("all five steps are shown", (await page.locator(".step").count()) === 5);
check("nothing is prefilled on a new machine", (await page.inputValue("#domain")) === "");

// Continue with nothing filled in has to refuse, and say which field and why.
await page.click("#next");
await page.waitForTimeout(500);
check("continuing with nothing filled in is refused", (await openPanel()) === "community");
check("the reason is shown", (await page.locator("#footnote").textContent()).includes("domain"));
check("the field itself is marked", (await page.locator("#domain.invalid").count()) === 1);

await page.fill("#domain", "example.org");
check("typing clears the marking", (await page.locator("#domain.invalid").count()) === 0);
await page.fill("#admin_email", "you@example.org");
await page.click("#next");
await page.waitForSelector('[data-panel="where"]:not([hidden])');
check("a complete panel continues", (await openPanel()) === "where");

check("a machine is preselected", await page.locator("#hetzner-fields").isVisible());
await page.click("#next");
await page.waitForTimeout(500);
check("continuing without a token is refused", (await openPanel()) === "where");

await page.check('input[name="target"][value="local"]');
await page.waitForTimeout(250);
check("choosing a LAN machine swaps the fields in",
      (await page.locator("#local-fields").isVisible()) &&
      !(await page.locator("#hetzner-fields").isVisible()));
check("the two directories are defaulted apart",
      (await page.inputValue("#data_dir")) !== (await page.inputValue("#backup_dir")));

await page.check('input[name="target"][value="hetzner"]');
await page.fill("#hcloud_token", "not-a-real-token");
await page.waitForTimeout(3500);
const hetzner = await page.locator("#check-hetzner").textContent();
check("an invalid Hetzner token is reported in place",
      /rejected|reach/i.test(hetzner), hetzner);

await page.click("#next");
await page.waitForSelector('[data-panel="repository"]:not([hidden])');
check("the repository panel is reached", (await openPanel()) === "repository");
check("a repository name is suggested", (await page.inputValue("#repo_name")).length > 0);
check("a release is pinned by default", /^v\d/.test(await page.inputValue("#version")));

await page.check('input[name="git_mode"][value="existing"]');
await page.waitForTimeout(200);
check("bringing your own repository swaps in the URL field",
      (await page.locator("#remote-url-field").isVisible()) &&
      !(await page.locator("#repo-name-field").isVisible()));
await page.check('input[name="git_mode"][value="create"]');
await page.fill("#git_token", "not-a-real-token");
await page.waitForTimeout(3000);

await page.click("#next");
await page.waitForSelector('[data-panel="apps"]:not([hidden])');
check("the applications panel is reached", (await openPanel()) === "apps");
check("every optional application is offered", (await page.locator(".app").count()) === 10);
check("a useful set is ticked", (await page.locator(".app input:checked").count()) === 4);
// docs/adr/0049 — mail is opt-in.
check("mail is not ticked", !(await page.locator('.app input[value="stalwart"]').isChecked()));

await page.locator('.app input[value="bulwark"]').check();
await page.waitForTimeout(300);
check("webmail without mail warns as the box is ticked",
      (await page.locator("#check-apps").textContent()).includes("Webmail"));
await page.locator('.app input[value="stalwart"]').check();
await page.waitForTimeout(300);
check("ticking mail clears the warning",
      (await page.locator("#check-apps").textContent()).trim() === "");
await page.locator('.app input[value="stalwart"]').uncheck();

await page.click("#next");
await page.waitForSelector(".plan-summary");
check("the plan is rendered", (await openPanel()) === "review");
check("the plan summarises every decision",
      (await page.locator(".plan-summary dt").count()) === 9);
check("the plan lists what will happen", (await page.locator(".plan-step").count()) === 4);
const hostnames = await page.locator(".hostname").allTextContents();
check("the plan lists the addresses members will use",
      hostnames.includes("identity.example.org") && hostnames.includes("files.example.org"));
check("the plan repeats the webmail warning",
      (await page.locator('[data-panel="review"] .check[data-status="warning"]').count()) >= 1);
check("the plan shows the commands that will run",
      (await page.locator(".commands code").allTextContents())
        .some((text) => text.includes("smallworlds-init.sh")));
// Phase 1 explains and does not act.
check("installing is not offered yet", await page.locator("#next").isDisabled());

await page.locator(".step").first().click();
await page.waitForSelector('[data-panel="community"]:not([hidden])');
check("answers survive going back", (await page.inputValue("#domain")) === "example.org");

const narrow = await browser.newPage({ viewport: { width: 380, height: 900 } });
await narrow.goto(url, { waitUntil: "networkidle" });
await narrow.waitForSelector(".step");
check("the page does not scroll sideways on a phone",
      await narrow.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1));

const dark = await browser.newPage({ viewport: { width: 900, height: 900 }, colorScheme: "dark" });
await dark.goto(url, { waitUntil: "networkidle" });
await dark.waitForSelector(".panel");
check("the page paints its own background in dark mode",
      await dark.evaluate(() => {
        const background = getComputedStyle(document.body).backgroundColor;
        return background !== "rgba(0, 0, 0, 0)" && background !== "transparent";
      }));

check("the page logged no errors", problems.length === 0, problems.join(" | "));

await browser.close();
console.log("");
if (failures.length) {
  console.log(`${failures.length} check(s) failed.`);
  process.exit(1);
}
console.log("All checks passed.");
