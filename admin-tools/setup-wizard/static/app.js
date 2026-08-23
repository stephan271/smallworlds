/* SmallWorlds setup wizard — client.
   No framework and no build step, matching the server. See
   plans-and-walkthroughs/design-setup-wizard.md. */

"use strict";

const PANELS = [
  { id: "community",  label: "Community" },
  { id: "where",      label: "Machine" },
  { id: "repository", label: "Settings" },
  { id: "apps",       label: "Apps" },
  { id: "review",     label: "Review" },
];

let state = null;      // defaults and the catalog, from the server
let current = 0;
let furthest = 0;
const el = (id) => document.getElementById(id);

/* --- talking to the wizard ---------------------------------------------- */

async function api(route, body) {
  const response = await fetch(route, {
    method: body === undefined ? "GET" : "POST",
    headers: {
      "X-Setup-Token": window.SETUP_TOKEN,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!response.ok) throw new Error(`${route} returned ${response.status}`);
  return response.json();
}

/* --- rendering a check result ------------------------------------------- */

const MARKS = { ok: "✓", warning: "!", error: "×", unknown: "?" };

function showCheck(id, result) {
  const node = el(id);
  if (!node) return;
  node.className = "check";
  if (!result) { node.textContent = ""; node.removeAttribute("data-status"); return; }
  node.dataset.status = result.status;
  node.textContent = "";
  const mark = document.createElement("span");
  mark.className = "mark";
  mark.textContent = MARKS[result.status] || "?";
  node.append(mark, document.createTextNode(result.summary));
  if (result.detail) {
    const detail = document.createElement("span");
    detail.className = "detail";
    detail.textContent = result.detail;
    node.append(detail);
  }
  if (result.remote) {
    const nested = document.createElement("span");
    nested.className = "detail";
    nested.textContent = result.remote.summary;
    node.append(nested);
  }
}

function showBusy(id, message) {
  const node = el(id);
  if (!node) return;
  node.className = "check busy";
  node.removeAttribute("data-status");
  node.textContent = message;
}

/* A check is advisory: it must never leave the operator unable to continue
   because a network call was slow or a resolver was restrictive. */
function runCheck(id, message, call) {
  showBusy(id, message);
  call().then((result) => showCheck(id, result))
        .catch((error) => showCheck(id, { status: "unknown", summary: String(error) }));
}

/* --- debounce ------------------------------------------------------------ */

function debounce(fn, delay) {
  let timer = null;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), delay);
  };
}

/* --- collecting the answers --------------------------------------------- */

function answers() {
  const target = document.querySelector('input[name="target"]:checked');
  const gitMode = document.querySelector('input[name="git_mode"]:checked');
  const usingLocal = target && target.value === "local";
  return {
    domain: el("domain").value.trim().toLowerCase(),
    admin_email: el("admin_email").value.trim(),
    env_ext: el("env_ext").value.trim(),
    onboarding_mode: el("onboarding_mode").value,

    target: target ? target.value : "",
    ssh_target: el("ssh_target").value.trim(),
    data_dir: el("data_dir").value.trim(),
    backup_dir: el("backup_dir").value.trim(),
    local_tls: el("local_tls").value,
    local_public: el("local_public").value,
    hcloud_token: usingLocal ? el("hcloud_token_local").value : el("hcloud_token").value,
    use_saved_hcloud_token: state ? state.has_saved_hcloud_token : false,

    git_mode: gitMode ? gitMode.value : "create",
    git_token: el("git_token").value,
    use_saved_git_token: state ? state.has_saved_git_token : false,
    git_username: el("git_username").value.trim(),
    remote_url: el("remote_url").value.trim(),
    repo_name: el("repo_name").value.trim(),
    repo_path: el("repo_path").value.trim(),
    version: el("version").value.trim(),

    apps: Array.from(document.querySelectorAll(".app input:checked")).map((i) => i.value),
  };
}

/* --- panel navigation ---------------------------------------------------- */

function renderSteps() {
  const rail = el("steps");
  rail.textContent = "";
  PANELS.forEach((panel, index) => {
    const button = document.createElement("button");
    button.className = "step";
    button.type = "button";
    button.dataset.state = index === current ? "current" : (index < furthest ? "done" : "todo");
    button.dataset.reachable = index <= furthest ? "yes" : "no";
    const dot = document.createElement("span");
    dot.className = "dot";
    dot.textContent = index < current ? "✓" : String(index + 1);
    const label = document.createElement("span");
    label.className = "label";
    label.textContent = panel.label;
    button.append(dot, label);
    if (index <= furthest) button.addEventListener("click", () => go(index));
    rail.append(button);
  });
}

function go(index) {
  current = Math.max(0, Math.min(PANELS.length - 1, index));
  furthest = Math.max(furthest, current);
  PANELS.forEach((panel, i) => {
    document.querySelector(`[data-panel="${panel.id}"]`).hidden = i !== current;
  });
  el("back").disabled = current === 0;
  el("next").textContent = current === PANELS.length - 1 ? "Install" : "Continue";
  el("next").disabled = current === PANELS.length - 1;   // Phase 2 arms this
  el("footnote").textContent = current === PANELS.length - 1
    ? "Installing is Phase 2 — this build stops at the plan."
    : "";
  renderSteps();
  window.scrollTo({ top: 0, behavior: "instant" });
  if (PANELS[current].id === "review") requestPlan();
}

/* Field-level problems are reported against the panel that owns them, so
   "domain is required" never appears three screens away from the domain. */
const FIELD_PANEL = {
  domain: 0, admin_email: 0, env_ext: 0, onboarding_mode: 0,
  target: 1, ssh_target: 1, data_dir: 1, backup_dir: 1, hcloud_token: 1,
  git_token: 2, remote_url: 2, repo_name: 2, version: 2,
};

function advance() {
  const collected = answers();
  api("/api/plan", { answers: collected }).then((result) => {
    document.querySelectorAll("input").forEach((i) => i.classList.remove("invalid"));
    if (result.valid) { go(current + 1); return; }
    const blocking = result.problems.filter((p) => (FIELD_PANEL[p.field] ?? 99) <= current);
    if (blocking.length === 0) { go(current + 1); return; }
    blocking.forEach((problem) => {
      const field = el(problem.field) ||
        (problem.field === "hcloud_token" ? el("hcloud_token_local") : null);
      if (field) field.classList.add("invalid");
    });
    el("footnote").textContent = blocking[0].message;
    const field = el(blocking[0].field);
    if (field) field.focus();
  });
}

/* --- the applications panel ---------------------------------------------- */

function renderApps() {
  el("always-installed").textContent = "";
  const intro = document.createElement("span");
  intro.textContent = "Always installed: ";
  el("always-installed").append(intro);
  state.catalog.always.forEach((app, index) => {
    const name = document.createElement("strong");
    name.textContent = app.label;
    el("always-installed").append(name);
    el("always-installed").append(
      document.createTextNode(index < state.catalog.always.length - 1 ? ", " : ". "));
  });
  el("always-installed").append(document.createTextNode(
    "Identity is what every other application signs you in with."));

  const grid = el("app-grid");
  grid.textContent = "";
  state.catalog.optional.forEach((app) => {
    const label = document.createElement("label");
    label.className = "app";

    const input = document.createElement("input");
    input.type = "checkbox";
    input.value = app.name;
    input.checked = state.apps.includes(app.name);
    input.addEventListener("change", reviewAppSelection);

    const body = document.createElement("span");
    const name = document.createElement("span");
    name.className = "app-name";
    name.textContent = app.label;
    const weight = document.createElement("span");
    weight.className = "weight";
    weight.textContent = app.weight;
    name.append(weight);
    const description = document.createElement("span");
    description.className = "app-desc";
    description.textContent = app.description;
    body.append(name, description);

    label.append(input, body);
    grid.append(label);
  });
  reviewAppSelection();
}

/* The bulwark-without-stalwart warning, shown the moment the box is ticked
   rather than printed after the overlay has already been written. */
function reviewAppSelection() {
  const selected = Array.from(document.querySelectorAll(".app input:checked")).map((i) => i.value);
  const notes = state.catalog.optional
    .filter((app) => app.requires && selected.includes(app.name) && !selected.includes(app.requires))
    .map((app) => app.requires_note);
  showCheck("check-apps", notes.length
    ? { status: "warning", summary: notes.join(" ") }
    : null);
}

/* --- the plan ------------------------------------------------------------ */

function requestPlan() {
  const target = el("plan");
  target.textContent = "Working out what will happen…";
  api("/api/plan", { answers: answers() }).then((result) => {
    target.textContent = "";
    if (!result.valid) {
      const list = document.createElement("div");
      list.className = "check";
      list.dataset.status = "error";
      list.textContent = "Some answers are still missing: " +
        result.problems.map((p) => p.message).join(" ");
      target.append(list);
      return;
    }
    renderPlan(target, result);
  }).catch((error) => { target.textContent = String(error); });
}

function renderPlan(target, result) {
  const summary = document.createElement("dl");
  summary.className = "plan-summary";
  const rows = [
    ["Domain", result.summary.domain],
    ["Environment", result.summary.environment],
    ["Runs on", result.summary.target],
    ["Certificates", result.summary.certificates],
    ["Reachable", result.summary.reachable],
    ["Members join", result.summary.onboarding === "invitation"
      ? "by invitation" : "by signing up themselves"],
    ["Settings repo", result.summary.repository],
    ["Pinned release", result.summary.version],
    ["Applications", String(result.summary.application_count)],
  ];
  rows.forEach(([term, value]) => {
    const dt = document.createElement("dt"); dt.textContent = term;
    const dd = document.createElement("dd"); dd.textContent = value;
    summary.append(dt, dd);
  });
  target.append(summary);

  result.warnings.forEach((text) => {
    const note = document.createElement("div");
    note.className = "check";
    note.dataset.status = "warning";
    const mark = document.createElement("span");
    mark.className = "mark"; mark.textContent = "!";
    note.append(mark, document.createTextNode(text));
    note.style.marginBottom = ".7rem";
    target.append(note);
  });

  target.append(section("What happens"));
  result.steps.forEach((step, index) => {
    const row = document.createElement("div");
    row.className = "plan-step";
    const number = document.createElement("span");
    number.className = "n"; number.textContent = String(index + 1);
    const body = document.createElement("div");
    const title = document.createElement("h3"); title.textContent = step.title;
    const detail = document.createElement("p"); detail.textContent = step.detail;
    body.append(title, detail);
    row.append(number, body);
    target.append(row);
  });

  target.append(section("Addresses your community will use"));
  const hosts = document.createElement("div");
  hosts.className = "hostnames";
  result.hostnames.forEach((entry) => {
    const chip = document.createElement("span");
    chip.className = "hostname";
    chip.textContent = entry.host;
    chip.title = entry.app;
    hosts.append(chip);
  });
  target.append(hosts);

  const commands = document.createElement("div");
  commands.className = "commands";
  const heading = document.createElement("h3");
  heading.textContent = "Exactly what will be run";
  commands.append(heading);
  result.commands.forEach((entry) => {
    const why = document.createElement("p");
    why.className = "why"; why.textContent = entry.purpose;
    const code = document.createElement("code");
    code.textContent = `${entry.environment} ${entry.command}`;
    commands.append(code, why);
  });
  const files = document.createElement("p");
  files.className = "why";
  files.textContent = "Your answers are saved to " +
    result.config_files.map((f) => f.path).join(" and ") +
    ", readable only by you. Nothing secret is committed to Git.";
  commands.append(files);
  target.append(commands);
}

function section(title) {
  const heading = document.createElement("h2");
  heading.className = "section";
  heading.textContent = title;
  return heading;
}

/* --- wiring -------------------------------------------------------------- */

function applyTargetVisibility() {
  const target = document.querySelector('input[name="target"]:checked');
  const value = target ? target.value : "";
  el("hetzner-fields").hidden = value !== "hetzner";
  el("local-fields").hidden = value !== "local";
}

function applyGitModeVisibility() {
  const mode = document.querySelector('input[name="git_mode"]:checked');
  const creating = !mode || mode.value === "create";
  el("repo-name-field").hidden = !creating;
  el("remote-url-field").hidden = creating;
}

function fill() {
  el("domain").value = state.domain;
  el("admin_email").value = state.admin_email;
  el("env_ext").value = state.env_ext;
  el("onboarding_mode").value = state.onboarding_mode;

  const target = document.querySelector(`input[name="target"][value="${state.target}"]`);
  if (target) target.checked = true;
  el("ssh_target").value = state.ssh_target;
  el("data_dir").value = state.data_dir;
  el("backup_dir").value = state.backup_dir;
  el("local_tls").value = state.local_tls;
  el("local_public").value = state.local_public;

  const gitMode = document.querySelector(`input[name="git_mode"][value="${state.git_mode}"]`);
  if (gitMode) gitMode.checked = true;
  el("git_username").value = state.git_username;
  el("remote_url").value = state.remote_url;
  el("repo_name").value = state.repo_name;
  el("repo_path").value = state.repo_path;
  el("version").value = state.version;

  // A remembered token stays on this machine: the field says one is held
  // rather than carrying it into the page.
  if (state.has_saved_hcloud_token) {
    el("hcloud_token").placeholder = "a saved token will be used — type to replace it";
    el("hcloud_token_local").placeholder = "a saved token will be used — type to replace it";
  }
  if (state.has_saved_git_token) {
    el("git_token").placeholder = "a saved token will be used — type to replace it";
  }

  applyTargetVisibility();
  applyGitModeVisibility();
  renderApps();
}

const checkDomain = debounce(() => {
  const domain = el("domain").value.trim().toLowerCase();
  if (!domain.includes(".")) { showCheck("check-dns", null); return; }
  runCheck("check-dns", "Looking up the nameservers…",
           () => api("/api/check/dns", { domain }));
}, 700);

const checkHetzner = debounce((fieldId, checkId) => {
  const token = el(fieldId).value;
  if (!token && !state.has_saved_hcloud_token) { showCheck(checkId, null); return; }
  runCheck(checkId, "Asking Hetzner…", () => api("/api/check/hetzner", {
    token, use_saved: !token, domain: el("domain").value.trim().toLowerCase(),
  }));
}, 600);

const checkGit = debounce(() => {
  const token = el("git_token").value;
  if (!token && !state.has_saved_git_token) { showCheck("check-git", null); return; }
  const mode = document.querySelector('input[name="git_mode"]:checked');
  runCheck("check-git", "Checking the token…", () => api("/api/check/git", {
    token,
    use_saved: !token,
    remote_url: mode && mode.value === "existing" ? el("remote_url").value.trim() : "",
    username: el("git_username").value.trim(),
  }));
}, 600);

function wire() {
  el("back").addEventListener("click", () => go(current - 1));
  el("next").addEventListener("click", advance);

  el("domain").addEventListener("input", checkDomain);
  el("hcloud_token").addEventListener("input", () => checkHetzner("hcloud_token", "check-hetzner"));
  el("hcloud_token_local").addEventListener(
    "input", () => checkHetzner("hcloud_token_local", "check-hetzner-local"));
  el("git_token").addEventListener("input", checkGit);
  el("remote_url").addEventListener("input", checkGit);

  document.querySelectorAll('input[name="target"]').forEach((input) => {
    input.addEventListener("change", () => {
      applyTargetVisibility();
      if (input.value === "hetzner") {
        runCheck("check-tools", "Checking this machine…", async () => {
          const result = await api("/api/check/local", {});
          return result.ssh_key.status === "error" ? result.ssh_key : result.tools;
        });
      } else {
        showCheck("check-tools", null);
      }
    });
  });

  document.querySelectorAll('input[name="git_mode"]').forEach((input) => {
    input.addEventListener("change", () => { applyGitModeVisibility(); checkGit(); });
  });

  el("probe-target").addEventListener("click", () => {
    runCheck("check-target", "Connecting…", () => api("/api/check/target", {
      ssh_target: el("ssh_target").value.trim(),
      data_dir: el("data_dir").value.trim(),
      backup_dir: el("backup_dir").value.trim(),
    }));
  });

  // Typing anywhere clears a stale "you still need to…" note.
  document.addEventListener("input", (event) => {
    if (event.target.matches("input, select")) {
      event.target.classList.remove("invalid");
      el("footnote").textContent = "";
    }
  });
}

api("/api/state").then((loaded) => {
  state = loaded;
  fill();
  wire();
  go(0);
  if (state.domain) checkDomain();
}).catch((error) => {
  document.getElementById("main").textContent =
    "Could not reach the wizard: " + error + ". Open the exact link printed in your terminal.";
});
