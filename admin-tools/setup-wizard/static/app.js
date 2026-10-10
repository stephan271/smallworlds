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
  // "A server you rent elsewhere" is the local target with a fixed posture:
  // on the internet, real certificates, and its own firewall.
  const rented = Boolean(target) && target.value === "vps";
  const usingLocal = Boolean(target) && (target.value === "local" || rented);
  const storage = document.querySelector('input[name="object_storage"]:checked');
  return {
    domain: el("domain").value.trim().toLowerCase(),
    admin_email: el("admin_email").value.trim(),
    env_ext: el("env_ext").value.trim(),
    onboarding_mode: el("onboarding_mode").value,

    target: rented ? "local" : (target ? target.value : ""),
    ssh_target: el("ssh_target").value.trim(),
    data_dir: el("data_dir").value.trim(),
    backup_dir: el("backup_dir").value.trim(),
    local_tls: rented ? "letsencrypt" : el("local_tls").value,
    local_public: rented ? "yes" : el("local_public").value,
    host_firewall: rented ? "yes" : "no",
    library_dir: el("library_dir").value.trim(),
    photo_backend: el("photo_backend").value,
    hcloud_token: usingLocal ? el("hcloud_token_local").value : el("hcloud_token").value,
    use_saved_hcloud_token: state ? state.has_saved_hcloud_token : false,

    object_storage: storage ? storage.value : "garage",
    s3_endpoint: el("s3_endpoint").value.trim(),
    s3_region: el("s3_region").value.trim(),
    s3_bucket_prefix: el("s3_bucket_prefix").value.trim(),
    s3_access_key_id: el("s3_access_key_id").value.trim(),
    s3_secret_access_key: el("s3_secret_access_key").value,
    use_saved_s3_secret: state ? state.has_saved_s3_secret : false,
    s3_backup_endpoint: el("s3_backup_endpoint").value.trim(),
    s3_backup_region: el("s3_backup_region").value.trim(),
    s3_backup_access_key_id: el("s3_backup_access_key_id").value.trim(),
    s3_backup_secret_access_key: el("s3_backup_secret_access_key").value,
    use_saved_s3_backup_secret: state ? state.has_saved_s3_backup_secret : false,

    git_mode: gitMode ? gitMode.value : "create",
    git_token: el("git_token").value,
    use_saved_git_token: state ? state.has_saved_git_token : false,
    git_username: el("git_username").value.trim(),
    remote_url: el("remote_url").value.trim(),
    repo_name: el("repo_name").value.trim(),
    repo_path: el("repo_path").value.trim(),
    version: el("version").value.trim(),

    apps: Array.from(document.querySelectorAll(".app input:checked"))
      .map((i) => i.value)
      .filter(Boolean),
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
  document.querySelector('[data-panel="install"]').hidden = true;
  el("steps").hidden = false;
  el("back").disabled = current === 0;
  el("next").textContent = current === PANELS.length - 1 ? "Install" : "Continue";
  el("next").disabled = false;
  el("footnote").textContent = "";
  renderSteps();
  window.scrollTo({ top: 0, behavior: "instant" });
  if (PANELS[current].id === "review") requestPlan();
}

/* Installing leaves the panelled flow entirely: there is nothing left to
   answer, and offering Back would suggest the install could be un-done. */
function showInstalling() {
  PANELS.forEach((panel) => {
    document.querySelector(`[data-panel="${panel.id}"]`).hidden = true;
  });
  document.querySelector('[data-panel="install"]').hidden = false;
  el("steps").hidden = true;
  document.querySelector(".actions").hidden = true;
  window.scrollTo({ top: 0, behavior: "instant" });
}

/* Field-level problems are reported against the panel that owns them, so
   "domain is required" never appears three screens away from the domain. */
const FIELD_PANEL = {
  domain: 0, admin_email: 0, env_ext: 0, onboarding_mode: 0,
  target: 1, ssh_target: 1, data_dir: 1, backup_dir: 1, hcloud_token: 1,
  library_dir: 1, host_firewall: 1, photo_backend: 3,
  object_storage: 1, s3_endpoint: 1, s3_region: 1, s3_bucket_prefix: 1,
  s3_access_key_id: 1, s3_secret_access_key: 1, s3_backup_endpoint: 1,
  s3_backup_region: 1, s3_backup_access_key_id: 1, s3_backup_secret_access_key: 1,
  git_token: 2, remote_url: 2, repo_name: 2, version: 2,
  apps: 3,
};

function advance() {
  if (PANELS[current].id === "review") { install(); return; }
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
  state.catalog.optional.filter((app) => !app.choice_group).forEach((app) => {
    grid.append(renderAppChoice(app, "checkbox"));
  });

  (state.catalog.choice_groups || []).forEach((group) => {
    const fieldset = document.createElement("fieldset");
    fieldset.className = "app-choice-group";
    const legend = document.createElement("legend");
    legend.textContent = group.label;
    const detail = document.createElement("p");
    detail.className = "hint";
    detail.textContent = group.description;
    fieldset.append(legend, detail);

    const none = {
      name: "",
      label: group.none_label,
      weight: "light",
      description: "Files remain available; office documents download or open locally.",
    };
    const groupApps = state.catalog.optional.filter((app) => app.choice_group === group.name);
    fieldset.append(renderAppChoice(none, "radio", group.name,
      !groupApps.some((app) => state.apps.includes(app.name))));
    groupApps.forEach((app) => {
      fieldset.append(renderAppChoice(app, "radio", group.name));
    });
    grid.append(fieldset);
  });
  reviewAppSelection();
}

function renderAppChoice(app, type, groupName = "", forceChecked = false) {
    const label = document.createElement("label");
    label.className = "app";

    const input = document.createElement("input");
    input.type = type;
    if (groupName) input.name = `app-choice-${groupName}`;
    input.value = app.name;
    input.checked = forceChecked || state.apps.includes(app.name);
    input.addEventListener("change", () => {
      if (app.auto_select_requires && app.requires && input.checked) {
        const dependency = document.querySelector(`.app input[value="${app.requires}"]`);
        if (dependency) dependency.checked = true;
      }
      reviewAppSelection();
    });

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
    return label;
}

/* The bulwark-without-stalwart warning, shown the moment the box is ticked
   rather than printed after the overlay has already been written. */
function reviewAppSelection() {
  const selected = Array.from(document.querySelectorAll(".app input:checked"))
    .map((i) => i.value)
    .filter(Boolean);
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
    ["Files and backups", result.summary.storage],
    ["Photos", result.summary.photos],
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
  const rented = value === "vps";
  el("hetzner-fields").hidden = value !== "hetzner";
  el("local-fields").hidden = value !== "local" && !rented;
  // A rented server is always public with real certificates, so those two
  // questions are answered by the choice itself.
  el("local-tls-field").hidden = rented;
  el("local-public-field").hidden = rented;
  el("vps-hint").hidden = !rented;
}

/* Which photo application is only a question once Photos is selected. */
function applyPhotoBackendVisibility() {
  const immich = document.querySelector('.app input[value="immich"]');
  el("photo-backend-field").hidden = !(immich && immich.checked);
}

/* With a provider there is no backup directory to ask for: every backup goes
   off the machine, which is what the second disk was standing in for. */
function applyStorageVisibility() {
  const storage = document.querySelector('input[name="object_storage"]:checked');
  const external = storage && storage.value === "external";
  el("external-storage-fields").hidden = !external;
  el("backup-dir-field").hidden = external;
  if (external && !el("s3_bucket_prefix").value) suggestBucketPrefix();
}

/* Mirrors default_bucket_prefix in plan.py, which mirrors the script. */
function suggestBucketPrefix() {
  const raw = ("sw-" + el("domain").value.trim() + el("env_ext").value.trim())
    .toLowerCase().replace(/\./g, "-").replace(/[^a-z0-9-]/g, "");
  el("s3_bucket_prefix").value = raw.slice(0, 30).replace(/-+$/, "");
}

/* The settings repository's name and the directory it is written to are the
   same choice twice: leaving them to drift means the operator's disk disagrees
   with their Git host for no reason. So renaming the repository renames the
   directory -- until the operator edits the path themselves, after which it is
   theirs and the wizard stops touching it. */
let derivedRepoPath = "";

function parentOf(path) {
  const trimmed = String(path || "").replace(/\/+$/, "");
  const cut = trimmed.lastIndexOf("/");
  return cut > 0 ? trimmed.slice(0, cut) : (cut === 0 ? "" : ".");
}

function syncRepoPathToName() {
  const name = el("repo_name").value.trim();
  const path = el("repo_path");
  // Anything the operator typed themselves wins, now and from then on.
  if (path.value && path.value !== derivedRepoPath) { showRepoPathPreview(); return; }
  if (!name) { showRepoPathPreview(); return; }
  derivedRepoPath = parentOf(derivedRepoPath || path.value) + "/" + name;
  path.value = derivedRepoPath;
  showRepoPathPreview();
}

function showRepoPathPreview() {
  const preview = el("repo-path-preview");
  if (preview) preview.textContent = el("repo_path").value || "(not set)";
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

  const remembered = state.target === "local" && state.host_firewall === "yes"
    ? "vps" : state.target;
  const target = document.querySelector(`input[name="target"][value="${remembered}"]`);
  el("library_dir").value = state.library_dir;
  el("photo_backend").value = state.photo_backend;
  if (target) target.checked = true;
  el("ssh_target").value = state.ssh_target;
  el("data_dir").value = state.data_dir;
  el("backup_dir").value = state.backup_dir;
  el("local_tls").value = state.local_tls;
  el("local_public").value = state.local_public;

  const storage = document.querySelector(
    `input[name="object_storage"][value="${state.object_storage}"]`);
  if (storage) storage.checked = true;
  el("s3_endpoint").value = state.s3_endpoint;
  el("s3_region").value = state.s3_region;
  el("s3_bucket_prefix").value = state.s3_bucket_prefix;
  el("s3_access_key_id").value = state.s3_access_key_id;
  el("s3_backup_endpoint").value = state.s3_backup_endpoint;
  el("s3_backup_region").value = state.s3_backup_region;
  el("s3_backup_access_key_id").value = state.s3_backup_access_key_id;
  el("backup-storage").open = Boolean(state.s3_backup_endpoint);
  if (state.has_saved_s3_secret) {
    el("s3_secret_access_key").placeholder = "a saved key will be used — type to replace it";
  }
  if (state.has_saved_s3_backup_secret) {
    el("s3_backup_secret_access_key").placeholder = "a saved key will be used — type to replace it";
  }

  const gitMode = document.querySelector(`input[name="git_mode"][value="${state.git_mode}"]`);
  if (gitMode) gitMode.checked = true;
  el("git_username").value = state.git_username;
  el("remote_url").value = state.remote_url;
  el("repo_name").value = state.repo_name;
  el("repo_path").value = state.repo_path;
  // The default the server computed counts as derived, so a first rename still
  // moves the directory; a path remembered from a previous run does not, since
  // it is where the operator's overlay actually is.
  derivedRepoPath = state.repo_path;
  showRepoPathPreview();
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
  applyStorageVisibility();
  applyGitModeVisibility();
  renderApps();
  applyPhotoBackendVisibility();
  // The app list is rebuilt on some changes, so this listens where the boxes
  // live rather than on the boxes themselves.
  el("app-grid").addEventListener("change", applyPhotoBackendVisibility);
}

/* One checker for both keys: the primary provider's and, when backups go
   somewhere else, that one's. Nothing is asked until the form could succeed. */
function s3Checker(prefix, checkId, which, savedFlag) {
  return debounce(() => {
    const secret = el(prefix + "secret_access_key").value;
    const body = {
      which,
      endpoint: el(prefix + "endpoint").value.trim(),
      region: el(prefix + "region").value.trim(),
      access_key_id: el(prefix + "access_key_id").value.trim(),
      secret,
      use_saved: !secret,
    };
    const hasSecret = secret || (state && state[savedFlag]);
    if (!body.endpoint || !body.region || !body.access_key_id || !hasSecret) {
      showCheck(checkId, null);
      return;
    }
    runCheck(checkId, "Asking the storage provider…", () => api("/api/check/s3", body));
  }, 700);
}
const checkS3 = s3Checker("s3_", "check-s3", "primary", "has_saved_s3_secret");
const checkS3Backup = s3Checker("s3_backup_", "check-s3-backup", "backup", "has_saved_s3_backup_secret");

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
    token, use_saved: !token,
    domain: el("domain").value.trim().toLowerCase(),
    // The environment decides which Primary IP is this cluster's, and that IP
    // is what pins the location the check reports on.
    env_ext: el("env_ext").value.trim(),
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
  el("repo_name").addEventListener("input", syncRepoPathToName);
  el("repo_path").addEventListener("input", showRepoPathPreview);

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

  document.querySelectorAll('input[name="object_storage"]').forEach((input) => {
    input.addEventListener("change", () => { applyStorageVisibility(); checkS3(); });
  });
  ["endpoint", "region", "access_key_id", "secret_access_key"].forEach((field) => {
    el("s3_" + field).addEventListener("input", checkS3);
    el("s3_backup_" + field).addEventListener("input", checkS3Backup);
  });

  el("probe-target").addEventListener("click", () => {
    runCheck("check-target", "Connecting…", () => api("/api/check/target", {
      ssh_target: el("ssh_target").value.trim(),
      data_dir: el("data_dir").value.trim(),
      backup_dir: el("backup_dir").value.trim(),
      object_storage: answers().object_storage,
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

/* An install already under way when the page opens — a refresh, a second tab,
   or coming back after closing the browser — has to win over the first panel.
   Both are settled before either paints, rather than racing each other. */
Promise.all([api("/api/state"), api("/api/install/status").catch(() => ({ status: "idle" }))])
  .then(([loaded, snapshot]) => {
    state = loaded;
    fill();
    wire();
    if (snapshot.status && snapshot.status !== "idle") {
      showInstalling();
      applySnapshot(snapshot);
      if (snapshot.status === "running" || snapshot.status === "starting") watch();
      return;
    }
    go(0);
    if (state.domain) checkDomain();
  })
  .catch((error) => {
    document.getElementById("main").textContent =
      "Could not reach the wizard: " + error +
      ". Open the exact link printed in your terminal.";
  });

/* --- installing ---------------------------------------------------------- */

let events = null;
let lastResult = {};
let lastGuidance = [];

const STAGE_GLYPHS = { pending: "", running: "•", done: "✓", failed: "×" };

function renderStages(stages) {
  const list = el("stages");
  list.textContent = "";
  stages.forEach((stage) => {
    const item = document.createElement("li");
    item.className = "stage";
    item.dataset.state = stage.state;
    item.dataset.stage = stage.id;

    const glyph = document.createElement("span");
    glyph.className = "glyph";
    glyph.textContent = STAGE_GLYPHS[stage.state] || "";

    const body = document.createElement("span");
    body.append(document.createTextNode(stage.label));
    const note = document.createElement("span");
    note.className = "note";
    note.textContent = stage.state === "done" ? "" : (stage.note || "");
    body.append(note);
    item.append(glyph, body);
    list.append(item);
  });
}

function updateStage(stage) {
  const existing = document.querySelector(`.stage[data-stage="${stage.id}"]`);
  if (!existing) { return; }
  existing.dataset.state = stage.state;
  existing.querySelector(".glyph").textContent = STAGE_GLYPHS[stage.state] || "";
  const note = existing.querySelector(".note");
  if (note) note.textContent = stage.state === "done" ? "" : (stage.note || "");
}

function appendLog(lines) {
  const log = el("log");
  const atBottom = log.scrollHeight - log.scrollTop - log.clientHeight < 40;
  lines.forEach((line) => log.append(document.createTextNode(line + "\n")));
  // Follow the tail, unless the operator has scrolled up to read something.
  if (atBottom) log.scrollTop = log.scrollHeight;
}

function showProgress(progress) {
  if (!progress || !progress.total) { el("progress").hidden = true; return; }
  el("progress").hidden = false;
  const share = Math.round((progress.healthy / progress.total) * 100);
  el("progress-fill").style.width = share + "%";
  el("progress-text").textContent =
    `${progress.healthy} of ${progress.total} applications ready`;
}

function renderGuidance(entries, visible) {
  lastGuidance = entries || lastGuidance;
  const block = el("guidance");
  const list = el("guidance-list");
  if (!block || !list) return;
  if (!visible || !lastGuidance.length) { block.hidden = true; return; }
  list.textContent = "";
  lastGuidance.forEach((entry) => {
    const item = document.createElement("li");
    const heading = document.createElement("h3");
    heading.textContent = entry.heading;
    const detail = document.createElement("p");
    detail.textContent = entry.detail;
    item.append(heading, detail);
    list.append(item);
  });
  block.hidden = false;
}

function applySnapshot(snapshot) {
  if (snapshot.stages) renderStages(snapshot.stages);
  if (snapshot.guidance) lastGuidance = snapshot.guidance;
  if (snapshot.log && snapshot.log.length) {
    el("log").textContent = "";
    appendLog(snapshot.log);
  }
  showProgress(snapshot.progress);
  applyStatus(snapshot);
}

function applyStatus(snapshot) {
  const status = snapshot.status;
  const heading = el("install-heading");
  const lede = el("install-lede");
  const outcome = el("install-outcome");
  el("cancel").hidden = status !== "running" && status !== "starting";
  // Advice belongs to a stop, not to a working install: an operator watching a
  // healthy rollout should not be reading about what to do when it breaks.
  renderGuidance(null, status === "failed" || status === "cancelled");

  if (status === "succeeded") {
    const unsettled = Array.from(document.querySelectorAll('.stage[data-state="failed"]'));
    // Anything still marked running when the installer exited did finish — the
    // scripts do not emit a completion marker for every stage.
    document.querySelectorAll('.stage[data-state="running"]').forEach((stage) => {
      stage.dataset.state = "done";
      stage.querySelector(".glyph").textContent = STAGE_GLYPHS.done;
      const note = stage.querySelector(".note");
      if (note) note.textContent = "";
    });
    if (unsettled.length) {
      heading.textContent = "Installed, but not everything settled";
      lede.textContent = "The cluster is up and keeps working on its own. One part " +
        "had not finished when the installer stopped watching.";
      showCheck("install-outcome", { status: "warning", summary:
        unsettled.map((stage) => stage.textContent.trim()).join("; ") +
        " — check with: kubectl get application -n argocd" });
    } else {
      heading.textContent = "Your cloud is up";
      lede.textContent = "Every application reported healthy.";
      showCheck("install-outcome", { status: "ok", summary: "Installation finished." });
    }
    finish({ ...snapshot, result: snapshot.result || lastResult });
  } else if (status === "failed") {
    heading.textContent = "The install stopped";
    lede.textContent = "Nothing was rolled back. Fix what the message describes and " +
      "run the wizard again — it carries on from where it got to.";
    showCheck("install-outcome", { status: "error", summary: snapshot.error ||
      "The installer failed. The log below has the details." });
    el("log-panel").open = true;
  } else if (status === "cancelled") {
    heading.textContent = "Stopped";
    lede.textContent = "";
    showCheck("install-outcome", { status: "warning", summary: snapshot.error || "Stopped." });
  } else {
    outcome.textContent = "";
    outcome.className = "check";
  }
}

/* --- handover ------------------------------------------------------------ */

function finish(snapshot) {
  const result = snapshot.result || {};
  const credentials = result.credentials || [];
  if (!credentials.length && !(result.urls || []).length) return;

  el("handover").hidden = false;
  el("shutdown").hidden = false;

  const list = el("credentials");
  list.textContent = "";
  credentials.forEach((entry) => {
    const row = document.createElement("div");
    row.className = "credential";
    const who = document.createElement("span");
    who.className = "who";
    who.textContent = entry.name;
    const secret = document.createElement("code");
    secret.className = "secret";
    secret.textContent = entry.value;
    row.append(who, secret);
    if (entry.note) {
      const why = document.createElement("p");
      why.className = "why";
      why.textContent = entry.note;
      row.append(why);
    }
    list.append(row);
  });

  const urls = el("result-urls");
  urls.textContent = "";
  (result.urls || []).forEach((entry) => {
    const link = document.createElement("a");
    link.className = "hostname";
    link.href = entry.url;
    link.target = "_blank";
    link.rel = "noreferrer";
    link.textContent = entry.url.replace(/^https:\/\//, "");
    link.title = entry.name;
    urls.append(link);
  });

  const dashboard = (result.urls || []).find((entry) => entry.name === "Dashboard");
  if (dashboard) {
    el("open-dashboard").href = dashboard.url;
    el("open-dashboard").hidden = false;
  }

  if (result.hosts) {
    el("hosts-block").hidden = false;
    el("hosts-line").textContent = result.hosts;
  }
  if (result.kubeconfig) {
    el("kubeconfig-block").hidden = false;
    el("kubeconfig-path").textContent = result.kubeconfig;
  }

  el("save-credentials").onclick = () => saveCredentials(result);
  document.querySelector("#log-panel summary").textContent =
    "Show what the installer did";
}

/* Handed over as a file rather than only shown, because these exist in exactly
   two places — this page, and the cluster. Built in the browser: the wizard
   never writes a credential to disk itself. */
function saveCredentials(result) {
  const when = new Date().toISOString().replace("T", " ").slice(0, 16);
  const lines = [
    "SmallWorlds — administrator credentials",
    `Created ${when}`,
    "",
    "Keep this file somewhere safe. Anyone holding it can administer your cloud.",
    "",
  ];
  (result.credentials || []).forEach((entry) => {
    lines.push(`${entry.name}`);
    lines.push(`  ${entry.value}`);
    if (entry.note) lines.push(`  (${entry.note})`);
    lines.push("");
  });
  if (result.kubeconfig) lines.push(`Kubeconfig: ${result.kubeconfig}`, "");
  if ((result.urls || []).length) {
    lines.push("Addresses:");
    result.urls.forEach((entry) => lines.push(`  ${entry.name}: ${entry.url}`));
    lines.push("");
  }
  if (result.hosts) {
    lines.push("Add to your router's DNS, Pi-hole, or /etc/hosts:");
    lines.push(`  ${result.hosts}`, "");
  }

  const blob = new Blob([lines.join("\n")], { type: "text/plain" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = "smallworlds-credentials.txt";
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(link.href), 5000);
  el("save-credentials").textContent = "Saved";
}

function install() {
  el("next").disabled = true;
  el("footnote").textContent = "Starting…";
  api("/api/install", { answers: answers() }).then((result) => {
    if (!result.started) {
      el("next").disabled = false;
      el("footnote").textContent = result.message ||
        (result.problems ? result.problems[0].message : "Could not start.");
      return;
    }
    showInstalling();
    renderStages(result.stages || []);
    watch();
  }).catch((error) => {
    el("next").disabled = false;
    el("footnote").textContent = String(error);
  });
}

function watch() {
  if (events) events.close();
  events = new EventSource(`/api/events?t=${encodeURIComponent(window.SETUP_TOKEN)}`);
  events.addEventListener("snapshot", (message) => applySnapshot(JSON.parse(message.data)));
  events.addEventListener("stage", (message) => updateStage(JSON.parse(message.data)));
  events.addEventListener("log", (message) => appendLog([JSON.parse(message.data)]));
  events.addEventListener("progress", (message) => showProgress(JSON.parse(message.data)));
  events.addEventListener("result", (message) =>
    (lastResult = JSON.parse(message.data)));
  events.addEventListener("guidance", (message) =>
    (lastGuidance = JSON.parse(message.data)));
  events.addEventListener("status", (message) =>
    applyStatus({ status: JSON.parse(message.data) }));
  events.addEventListener("done", (message) => {
    applySnapshot(JSON.parse(message.data));
    events.close();
    events = null;
  });
  // The browser reconnects a dropped stream on its own, and the server hands
  // every new watcher a snapshot — so a reconnect re-syncs rather than
  // resuming mid-sentence.
  events.onerror = () => {};
}

el("cancel").addEventListener("click", () => {
  el("cancel").disabled = true;
  api("/api/install/cancel", {}).catch(() => {});
});

/* The wizard is single-use: once the cluster is up it has no further job, and
   leaving a loopback server holding the credentials running is worse than
   closing it. */
el("shutdown").addEventListener("click", () => {
  el("shutdown").disabled = true;
  api("/api/shutdown", {}).catch(() => {});
  el("handover").hidden = true;
  el("log-panel").hidden = true;
  el("install-heading").textContent = "The wizard is closed";
  el("install-lede").textContent =
    "Your cloud carries on without it. Run ./smallworlds-setup again if you need it.";
  showCheck("install-outcome", null);
  renderGuidance(null, false);
});
