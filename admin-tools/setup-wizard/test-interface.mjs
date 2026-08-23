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

const [url, mode = "plan"] = process.argv.slice(2);
if (!url) {
  console.error("usage: test-interface.mjs <wizard url> [plan|install]");
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

if (mode === "install") {
  await walkInstall();
  await finish();
}

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
check("installing is offered", !(await page.locator("#next").isDisabled()));

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
await finish();

async function finish() {
  await browser.close();
  console.log("");
  if (failures.length) {
    console.log(`${failures.length} check(s) failed.`);
    process.exit(1);
  }
  console.log("All checks passed.");
  process.exit(0);
}

/* The install walk runs against a wizard pointed at a stub checkout whose two
   scripts print markers and exit, so this exercises the whole browser path —
   Install, the stage list, the streamed log, the outcome — without touching a
   cloud account. */
async function walkInstall() {
  console.log("interface — installing");
  await page.goto(url, { waitUntil: "networkidle" });
  await page.waitForSelector('.step[data-state="current"]');

  await page.fill("#domain", "example.org");
  await page.fill("#admin_email", "you@example.org");
  await page.click("#next");
  await page.waitForSelector('[data-panel="where"]:not([hidden])');
  await page.fill("#hcloud_token", "a-token-that-must-not-be-shown");
  await page.click("#next");
  await page.waitForSelector('[data-panel="repository"]:not([hidden])');
  await page.check('input[name="git_mode"][value="existing"]');
  await page.fill("#remote_url", "https://github.com/octocat/overlay.git");
  // The username lives behind Advanced settings: it defaults to
  // x-access-token, which is what a GitHub token wants.
  await page.locator('[data-panel="repository"] .advanced summary').click();
  await page.fill("#git_username", "octocat");
  await page.fill("#git_token", "another-token-that-must-not-be-shown");
  await page.click("#next");
  await page.waitForSelector('[data-panel="apps"]:not([hidden])');
  await page.click("#next");
  await page.waitForSelector(".plan-summary");

  await page.click("#next");
  await page.waitForSelector('[data-panel="install"]:not([hidden])', { timeout: 20000 });
  check("clicking Install shows the progress view", true);
  check("the step rail is put away", !(await page.locator("#steps").isVisible()));
  check("Back is no longer offered", !(await page.locator(".actions").isVisible()));
  check("every expected stage is listed up front",
        (await page.locator(".stage").count()) >= 8);

  await page.waitForSelector('#install-outcome[data-status]', { timeout: 90000 });
  const outcome = await page.locator("#install-outcome").textContent();
  check("the install reports success", /finished/i.test(outcome), outcome);
  check("finished stages are ticked",
        (await page.locator('.stage[data-state="done"]').count()) >= 2);
  check("stages nobody reached stay pending",
        (await page.locator('.stage[data-state="pending"]').count()) >= 1);
  // The scripts do not emit a completion marker for every stage, so nothing
  // may be left pulsing once the installer has exited.
  check("no stage is left running after the install ends",
        (await page.locator('.stage[data-state="running"]').count()) === 0);
  check("a finished stage drops its in-progress note",
        (await page.locator('.stage[data-state="done"] .note').first().textContent())
          .trim() === "");
  check("the convergence progress bar appeared",
        await page.locator("#progress").isVisible());
  check("the progress bar says how many applications are ready",
        /\d+ of \d+ applications ready/.test(
          await page.locator("#progress-text").textContent()));

  await page.locator("#log-panel summary").click();
  const log = await page.locator("#log").textContent();
  check("the streamed log is shown", log.includes("writing the overlay"));
  check("markers are turned into stages, not printed", !log.includes(">>> step:"));
  check("a secret a script printed is redacted",
        !log.includes("another-token-that-must-not-be-shown") && log.includes("[redacted]"));
  check("the tokens typed into the wizard never appear",
        !log.includes("must-not-be-shown"));

  // Reopening mid-install must show the install, not the first panel again.
  const second = await browser.newPage({ viewport: { width: 900, height: 1000 } });
  await second.goto(url, { waitUntil: "networkidle" });
  await second.waitForSelector('[data-panel="install"]:not([hidden])', { timeout: 20000 });
  check("reopening the page returns to the install",
        (await second.locator(".stage").count()) >= 8);
  check("the log is replayed for the reopened page",
        (await second.locator("#log").textContent()).includes("writing the overlay"));
  check("the page logged no errors", problems.length === 0, problems.join(" | "));
}
