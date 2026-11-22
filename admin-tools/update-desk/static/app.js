"use strict";
// The Update Desk's page. Plain JS, no framework: it polls /api/state and redraws.

const TOKEN = window.DESK_TOKEN;
const $ = (id) => document.getElementById(id);
const opened = new Set();
let state = null;
let lastDiff = null;
let busy = false;
let grouping = "careful";
try { grouping = localStorage.getItem("desk.grouping") || "careful"; } catch (e) { /* private window */ }

function esc(value) {
  return String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
const short = (sha) => (sha && /^[0-9a-f]{40}$/.test(sha) ? sha.slice(0, 7) : sha || "?");
function when(iso) {
  if (!iso) return "?";
  const d = new Date(iso);
  return d.toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}
function duration(hours) {
  if (hours == null) return "";
  if (hours < 1) return `${Math.round(hours * 60)} min`;
  if (hours < 48) return `${Math.round(hours)} h`;
  return `${Math.round(hours / 24)} days`;
}
const pill = (text, kind = "") => `<span class="pill ${kind}">${esc(text)}</span>`;

async function api(path, body) {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    headers: { "X-Desk-Token": TOKEN, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await response.json().catch(() => ({ error: `HTTP ${response.status}` }));
  if (!response.ok || data.error) throw new Error(data.error || `HTTP ${response.status}`);
  return data;
}

async function act(label, path, body, after) {
  if (busy) return;
  busy = true;
  document.body.style.cursor = "progress";
  try {
    const result = await api(path, body || {});
    if (after) after(result);
    await load();
  } catch (error) {
    showAlert(`${label}: ${error.message}`, "bad");
  } finally {
    busy = false;
    document.body.style.cursor = "";
  }
}

let transient = [];
function showAlert(text, kind) {
  transient.push({ text, kind, until: Date.now() + 15000 });
  drawAlerts();
}

function changeList(changes, limit = 6) {
  const main = changes.filter((c) => !c.helper);
  const helpers = changes.filter((c) => c.helper);
  const items = main.slice(0, limit).map((c) => {
    const label = (c.kind === "chart" ? "chart " : "") + c.name.split("/").pop();
    return `<li><span class="name" title="${esc(c.name)}">${esc(label)}</span>
       <span class="vers">${esc(c.from || "new")} → <span class="to">${esc(c.to || "removed")}</span></span></li>`;
  });
  if (main.length > limit) items.push(`<li class="more">and ${main.length - limit} more</li>`);
  if (helpers.length) items.push(`<li class="helper more">helper images: ${helpers.map((c) => `${esc(c.name)} → ${esc(c.to)}`).join(", ")}</li>`);
  return items.length ? `<ul class="changes">${items.join("")}</ul>` : `<p class="more">configuration only, no version changes</p>`;
}

// ---------------------------------------------------------------- drawing

function drawAlerts() {
  const items = [];
  transient = transient.filter((a) => a.until > Date.now());
  for (const a of transient) items.push(`<div class="alert ${a.kind}">${esc(a.text)}</div>`);
  if (state) {
    if (state.fatal) items.push(`<div class="alert bad">The Desk failed: ${esc(state.fatal)}</div>`);
    if (state.path_error) items.push(`<div class="alert bad">${esc(state.path_error)}</div>`);
    if (state.verdict_error) items.push(`<div class="alert warn">${esc(state.verdict_error)}</div>`);
    if (state.fetch_error) items.push(`<div class="alert warn">Could not fetch the overlay: ${esc(state.fetch_error)}</div>`);
    const p = state.progress || {};
    if (p.cluster_error) items.push(`<div class="alert warn">The cluster does not answer, so the Desk cannot tell where an update stands or move the root pin.
      <pre>${esc(p.cluster_error)}</pre>Is this device on the community's tailnet, and is the kubeconfig the right one?</div>`);
    if (p.dirty_other && p.dirty_other.length) items.push(`<div class="alert warn">The overlay has other uncommitted changes (${esc(p.dirty_other.join(", "))}). The Desk does not touch them, but it will not write a step until they are committed or removed.</div>`);
    const ring0 = state.ring0 || {};
    if (ring0.error) items.push(`<div class="alert warn">${esc(ring0.error)}</div>`);
  }
  $("alerts").innerHTML = items.join("");
}

function drawHeader() {
  const path = state.path;
  const p = state.progress || {};
  const remote = (p.overlay && p.overlay.remote) || "";
  const domain = path ? path.community.domain : "";
  $("where").textContent = [domain, remote.replace(/^https:\/\/github\.com\//, "")].filter(Boolean).join(" · ");
  const ring0 = state.ring0 || {};
  const now = ring0.now || {};
  const age = ring0.updated ? (Date.now() - new Date(ring0.updated)) / 60000 : null;
  let text = "Ring 0: unknown", kind = "warn";
  if (now.state === "up-to-date") { text = "Ring 0 up to date"; kind = "ok"; }
  if (now.state === "updating") {
    const what = now.pull ? `#${now.pull.number}` : short(now.commit);
    text = `Ring 0 checking ${what}${now.step ? `: ${now.step}` : ""}`; kind = "info";
  }
  if (now.state === "stuck") { text = "Ring 0 stuck"; kind = "bad"; }
  if (age != null && age > 45) { text += " (not reporting)"; kind = "warn"; }
  const el = $("ring0");
  el.className = `pill ${kind}`;
  el.textContent = text;
  el.title = ring0.updated ? `as of ${when(ring0.updated)}${now.waiting ? " · " + now.waiting : ""}` : "";
}

function drawSummary() {
  const path = state.path;
  if (!path) { $("summary").innerHTML = ""; return; }
  const steps = path.steps;
  const migrations = steps.filter((s) => s.migrations.length).length;
  const byHand = steps.reduce((n, s) => n + s.manual.filter((m) => m.when !== "info").length, 0);
  const name = (ref) => (/^v\d/.test(ref) ? `release ${ref}` : `commit ${short(ref)}`);
  const progress = state.progress || {};
  const moving = progress.running_ref && progress.running_ref !== path.ref;
  const last = steps.length ? steps[steps.length - 1] : null;
  const first = moving
    ? `<div><div class="label">Your cluster runs</div><div class="figure">${esc(name(progress.running_ref))}</div>
         <div class="label">moving to ${esc(name(path.ref))} (${esc(when(state.path_start_date))}); the steps below start there</div></div>`
    : `<div><div class="label">Your community runs</div><div class="figure">${esc(name(path.ref))}</div>
         <div class="label">${esc(when(state.path_start_date))}</div></div>`;
  $("summary").innerHTML = `${first}
    <div><div class="label">Steps to Ring 0</div><div class="figure">${steps.length}</div>
      <div class="label">${last ? `up to ${esc(short(last.sha))}, verified ${esc(when(last.verified_at))}` : "you run what Ring 0 verified last"}</div></div>
    <div><div class="label">Steps that migrate a database</div><div class="figure">${migrations}</div>
      <div class="label">going back means restoring a backup</div></div>
    <div><div class="label">Things to do by hand</div><div class="figure">${byHand}</div>
      <div class="label">shown on the step that needs them</div></div>`;
}

function drawUnits() {
  const path = state.path;
  if (!path) { $("units").innerHTML = ""; return; }
  $("units").innerHTML = path.overview.map((row) => {
    let badge = pill("Current", "ok");
    if (row.steps.length) badge = pill(`${row.steps.length} step${row.steps.length > 1 ? "s" : ""}`, "info");
    const migrates = row.migrates.length ? pill("migrates its database", "warn") : "";
    return `<article class="unit"><header><h3>${esc(row.title)}</h3>${badge}</header>
      ${row.steps.length ? changeList(row.changes, 5) : `<p class="more">nothing newer on Ring 0</p>`}
      <div class="chips">${migrates}</div></article>`;
  }).join("");
}

function stepChips(step, units) {
  const chips = Object.keys(step.units).map((u) => pill(step.units[u].title));
  for (const m of step.migrations) chips.push(pill(`${m.tenant}: migrates`, "warn"));
  for (const m of step.manual) {
    if (m.when === "before") chips.push(pill("do something first", "bad"));
    if (m.when === "after") chips.push(pill("do something after", "warn"));
    if (m.when === "info") chips.push(pill("note", ""));
  }
  for (const s of step.stops) chips.push(pill(`Upgrade Stop ${s.version}`, "bad"));
  for (const r of step.releases) chips.push(pill(`release ${r}`, "ok"));
  if (step.node) chips.push(pill("new nodes only: bootstrap change"));
  return chips.join("");
}

function stepBody(step) {
  const parts = [];
  for (const unit of Object.values(step.units)) {
    parts.push(`<h3>${esc(unit.title)}</h3>${changeList(unit.changes, 12)}`);
  }
  for (const m of step.migrations) {
    parts.push(`<div class="box warn"><strong>${esc(m.tenant)}</strong> migrates its database:
      ${esc(m.because.map((c) => `${c.name} ${c.from} → ${c.to}`).join("; "))}.
      ${m.protected ? "The pre-update backup hook backs it up before the sync." : "<strong>No pre-update backup hook yet</strong>: take a backup before this step."}
      Going back means restoring that backup.</div>`);
  }
  for (const m of step.manual) {
    const kind = m.when === "before" ? "bad" : m.when === "after" ? "warn" : "";
    const label = m.when === "before" ? "Before this step" : m.when === "after" ? "After this step" : "Note";
    parts.push(`<div class="box ${kind}"><strong>${label}: ${esc(m.title)}</strong><pre>${esc(m.detail)}</pre></div>`);
  }
  for (const s of step.stops) parts.push(`<div class="box bad"><strong>Upgrade Stop</strong>: ${esc(s.image)} must run ${esc(s.version)} before ${esc(s.before)}. ${esc(s.why)}</div>`);
  const commits = step.commits.map((c) => {
    const verdict = c.verdict === "success" ? "" : ` <span class="muted">(${esc(c.verdict || "not checked")} on its own)</span>`;
    const touched = c.units.length ? "" : ` <span class="muted">(does not touch your cluster)</span>`;
    return `<li><code>${esc(short(c.sha))}</code> ${esc(c.subject)}${verdict}${touched}</li>`;
  });
  parts.push(`<h3>Commits on main</h3><ul>${commits.join("")}</ul>`);
  parts.push(`<p class="muted">Ring 0 verified <code>${esc(short(step.sha))}</code> on ${esc(when(step.verified_at))}
    and ${step.ring0_current ? `has run it for ${esc(duration(step.ring0_hours))}` : `ran it for ${esc(duration(step.ring0_hours))} before the next change`}.
    Undo: ${step.undo === "revert" ? "revert the overlay commit" : "restore the pre-update backup"}.</p>`);
  return parts.join("");
}

function drawPath() {
  const path = state.path;
  if (!path) { $("path").innerHTML = ""; $("beyond").innerHTML = ""; return; }
  if (!path.steps.length) {
    $("path").innerHTML = `<div class="card"><strong>Nothing to do.</strong> Your community runs what Ring 0 verified last.</div>`;
  } else {
    const inFlight = !["done", "cluster-unknown"].includes(state.progress.stage);
    const byNumber = Object.fromEntries(path.steps.map((s) => [s.number, s]));
    $("path").innerHTML = path.batches[grouping].map((batch, index) => {
      const steps = [];
      for (let n = batch.first; n <= batch.last; n++) steps.push(byNumber[n]);
      const last = byNumber[batch.last];
      const range = batch.first === batch.last ? `Step ${batch.first}` : `Steps ${batch.first}–${batch.last}`;
      const kind = batch.kind === "simple"
        ? pill("simple: undo by revert", "ok")
        : pill(steps.some((s) => s.migrations.length) ? "migrates a database" : "needs you", "warn");
      const label = index === 0 ? `Plan ${range.toLowerCase()}` : `Plan up to step ${batch.last}`;
      const button = `<button class="${index === 0 ? "" : "ghost small"}" data-plan="${esc(last.sha)}" ${inFlight ? "disabled title='Finish the update in progress first'" : ""}>${esc(label)}</button>`;
      const rows = steps.map((step) => {
        const isOpen = opened.has(step.sha) ? "open" : "";
        const titles = step.titles.slice(0, 3).map((t) => `<div class="line">${esc(t)}</div>`).join("");
        const more = step.titles.length > 3 ? `<div class="more">and ${step.titles.length - 3} more</div>` : "";
        return `<details class="step" data-sha="${esc(step.sha)}" ${isOpen}>
          <summary><span class="num">${step.number}</span>
            <span class="what">${titles}${more}<span class="chips">${stepChips(step)}</span></span>
            <span class="side"><span class="muted">${esc(when(step.verified_at))}</span>
              <button class="ghost small" data-plan="${esc(step.sha)}" ${inFlight ? "disabled" : ""}>up to here</button></span>
          </summary><div class="body">${opened.has(step.sha) ? stepBody(step) : ""}</div></details>`;
      }).join("");
      return `<div class="batch ${index === 0 ? "next" : ""}">
        <div class="batch-head"><span><span class="title">${esc(range)}</span> ${kind}</span>${button}</div>${rows}</div>`;
    }).join("");
  }
  const beyond = path.beyond || [];
  $("beyond").innerHTML = beyond.length
    ? `<div class="card"><strong>${beyond.length} newer commit${beyond.length > 1 ? "s" : ""} on main</strong> are not part of a verified step yet:
        <ul>${beyond.map((c) => `<li><code>${esc(short(c.sha))}</code> ${esc(c.subject)} <span class="muted">(${esc(c.verdict || "not checked yet")})</span></li>`).join("")}</ul></div>`
    : "";
}

const STAGES = [["written", "Overlay written"], ["committed", "Committed"], ["pushed", "Pushed"],
                ["rolling", "Root pin moved"], ["done", "Rolled out"]];

function drawProgress() {
  const p = state.progress || {};
  const section = $("progress-section");
  const last = state.last_update;
  const showDone = p.stage === "done" && last && last.recent;
  if (!["written", "committed", "pushed", "rolling"].includes(p.stage) && !showDone) {
    section.hidden = true;
    return;
  }
  section.hidden = false;
  const order = STAGES.map((s) => s[0]);
  const at = order.indexOf(p.stage);
  $("stages").innerHTML = STAGES.map(([id, label], i) =>
    `<li class="${i < at || p.stage === "done" ? "done" : i === at ? "now" : ""}">${esc(label)}</li>`).join("");

  const parts = [];
  const routed = (state.path && state.path.routed) || [];
  const planned = state.planned && state.planned.to_ref === p.working_ref ? state.planned : null;
  if (p.running_ref && p.running_ref !== p.working_ref) {
    parts.push(`<p class="muted">From ${esc(short(p.running_ref))} to ${esc(short(p.working_ref))}${planned && planned.steps.length ? `, steps ${esc(planned.steps[0])}–${esc(planned.steps[planned.steps.length - 1])}` : ""}.</p>`);
  }
  if (planned && !showDone) {
    for (const m of planned.manual.filter((x) => x.when === "after")) {
      parts.push(`<div class="box">Once this is through, you will: <strong>${esc(m.title)}</strong> (the Desk shows how).</div>`);
    }
  }
  if (p.stage === "written") {
    parts.push(`<p>The overlay's files now name <code>${esc(short(p.working_ref))}</code>. Nothing is committed yet.</p>`);
    if (lastDiff) parts.push(`<details><summary>Show the change</summary><pre>${esc(lastDiff)}</pre></details>`);
    parts.push(`<div class="action-row"><button data-act="commit">Commit</button>
      <button class="ghost" data-act="discard">Discard the change</button></div>`);
  }
  if (p.stage === "committed") {
    parts.push(`<p>Committed as <code>${esc(short(p.overlay.head))}</code>, not pushed yet.</p>
      <div class="box warn">Pushing <strong>starts the update</strong> for the applications your overlay routes to
      its own repository (${esc(routed.join(", ") || "none")}): they follow the overlay's main and sync within about three minutes.
      The rest follows when you move the root pin, the next button.</div>
      <div class="action-row"><button data-act="push">Push to origin/main</button></div>`);
  }
  if (p.stage === "pushed") {
    const root = p.root || {};
    parts.push(`<p>Pushed. The root Application still points at overlay commit <code>${esc(short(root.targetRevision))}</code>;
      moving it to <code>${esc(short(p.overlay.origin))}</code> brings the Platform and every Application definition along.</p>
      <pre>kubectl -n argocd patch applications.argoproj.io smallworlds-root --type merge -p '{"spec":{"source":{"targetRevision":"${esc(p.overlay.origin)}"}}}'</pre>
      <div class="action-row"><button data-act="pin">Move the root pin</button></div>`);
  }
  if (p.stage === "rolling" || p.stage === "done") {
    const ready = (p.applications || []).length - (p.not_ready || []).length;
    parts.push(`<p>${ready} of ${(p.applications || []).length} Applications are Synced and Healthy at the new commit.</p>`);
    if ((p.not_ready || []).length) {
      parts.push(`<table class="apps-table">${p.not_ready.map((a) => `<tr><td>${esc(a.name)}</td><td class="muted">${esc(a.reason)}</td></tr>`).join("")}</table>
        <div class="action-row"><button class="ghost" data-act="refresh-apps">Ask Argo CD to look again (hard refresh)</button>
        <span class="muted">An Application stays unhealthy for 30 minutes before ApplicationNotHealthy alerts you.</span></div>`);
    }
    const backups = (state.backups || []).slice(0, 6);
    if (backups.length) {
      parts.push(`<h3>Recent backups</h3><table class="apps-table">${backups.map((b) =>
        `<tr><td>${esc(b.namespace)}/${esc(b.name)}</td><td>${esc(b.by)}</td><td>${pill(b.phase || "pending", b.phase === "completed" ? "ok" : b.phase === "failed" ? "bad" : "info")}</td></tr>`).join("")}</table>`);
    }
  }
  if (showDone) {
    parts.push(`<div class="box ok"><strong>Done.</strong> ${esc(last.subject)}</div>`);
    for (const m of last.after || []) {
      parts.push(`<div class="box warn"><strong>Now do this by hand: ${esc(m.title)}</strong><pre>${esc(m.detail)}</pre></div>`);
    }
    parts.push(`<div class="action-row"><button class="ghost" data-act="undo-plan">Undo this update …</button></div>`);
  }
  $("progress").innerHTML = parts.join("");
}

function drawLog() {
  $("log").innerHTML = (state.log || []).map((e) =>
    `<div class="log-entry">${pill(e.ok ? "ok" : "failed", e.ok ? "ok" : "bad")} <strong>${esc(e.at)} ${esc(e.action)}</strong>
     ${e.command ? `<pre>${esc(e.command)}</pre>` : ""}${e.output ? `<pre class="muted">${esc(e.output)}</pre>` : ""}</div>`).join("")
    || `<p class="muted">Nothing yet.</p>`;
}

function draw() {
  drawAlerts();
  if (!state || state.fatal) return;
  drawHeader();
  drawSummary();
  drawProgress();
  drawUnits();
  drawPath();
  drawLog();
}

// ---------------------------------------------------------------- the plan

function planHtml(plan) {
  const parts = [];
  const steps = plan.steps.length ? (plan.steps.length === 1 ? `step ${plan.steps[0]}` : `steps ${plan.steps[0]}–${plan.steps[plan.steps.length - 1]}`) : "";
  $("plan-title").textContent = plan.kind === "undo" ? `Undo: back to ${short(plan.to_ref)}` : `Take ${steps} → ${short(plan.to)}`;
  if (plan.blocked) parts.push(`<div class="box bad">${esc(plan.blocked)}</div>`);

  const before = plan.manual.filter((m) => m.when === "before");
  const after = plan.manual.filter((m) => m.when === "after");
  const info = plan.manual.filter((m) => m.when === "info");
  for (const m of before) parts.push(`<div class="box bad"><strong>Do this first (step ${m.step}): ${esc(m.title)}</strong><pre>${esc(m.detail)}</pre></div>`);
  for (const s of plan.stops) parts.push(`<div class="box bad"><strong>Upgrade Stop:</strong> ${esc(s.image)} must run ${esc(s.version)} before ${esc(s.before)}. ${esc(s.why)} Take a shorter step.</div>`);

  parts.push(`<h3>What changes</h3>`);
  for (const unit of Object.values(plan.units)) {
    parts.push(`<div class="box"><strong>${esc(unit.title)}</strong>${unit.steps.length ? ` <span class="muted">(steps ${esc(unit.steps.join(", "))})</span>` : ""}${changeList(unit.changes, 10)}</div>`);
  }
  if (!Object.keys(plan.units).length) parts.push(`<p class="muted">No version changes.</p>`);

  if (plan.migrations.length) {
    parts.push(`<h3>Databases</h3>`);
    for (const m of plan.migrations) {
      parts.push(`<div class="box warn"><strong>${esc(m.tenant)}</strong> migrates:
        ${esc(m.because.map((c) => `${c.name} ${c.from} → ${c.to}`).join("; "))}.
        ${m.protected ? "The pre-update backup hook takes a backup before the sync and holds the update back if it fails."
                      : "<strong>Nothing backs it up by itself in this sync</strong> (the hook arrives with these steps). Take a backup now."}</div>`);
    }
    const backups = state.backups || [];
    parts.push(`<p>Take an extra backup now if you like. It is a CNPG Backup in object storage, labelled for this step.</p>`);
    parts.push(plan.databases.map((d) => {
      const taken = backups.find((b) => b.namespace === d.namespace && b.cluster === d.name && b.by === "desk" && b.for === plan.to.slice(0, 12));
      const status = taken ? pill(taken.phase || "running", taken.phase === "completed" ? "ok" : taken.phase === "failed" ? "bad" : "info") : "";
      return `<div class="action-row"><button class="ghost small" data-backup="${esc(d.namespace)}/${esc(d.name)}">Back up ${esc(d.namespace)}/${esc(d.name)}</button> ${status}</div>`;
    }).join("") || `<p class="muted">No database found in the cluster for these applications${state.progress.cluster_error ? " (the cluster does not answer)" : ""}.</p>`);
  }

  for (const m of info) parts.push(`<div class="box"><strong>Note (step ${m.step}): ${esc(m.title)}</strong><pre>${esc(m.detail)}</pre></div>`);
  for (const m of after) parts.push(`<div class="box warn"><strong>After this (step ${m.step}): ${esc(m.title)}</strong><pre>${esc(m.detail)}</pre></div>`);

  parts.push(`<h3>Going back</h3><p>${plan.undo === "revert"
    ? "Undo by reverting: the Desk offers <em>Undo</em> once the update is through. Nothing migrates."
    : "<strong>Restore only.</strong> A database migrates, so reverting the overlay does not bring the old state back. Going back means restoring the pre-update backup (doc/storage-and-backup.md, “Rolling an update back”), which loses what members did since."}</p>`);
  parts.push(`<details><summary>Overlay commit message</summary><pre>${esc(plan.message)}</pre></details>`);

  const needsConfirm = plan.migrations.length || before.length || plan.stops.length;
  if (!plan.blocked) {
    parts.push(`<div class="check">${needsConfirm ? `<label><input type="checkbox" id="plan-ok"> I have read the boxes above${before.length ? " and done what they ask first" : ""}.</label>` : ""}</div>
      <div class="action-row"><button id="plan-write" ${needsConfirm ? "disabled" : ""}>Write the overlay change</button>
      <span class="muted">Writes the files only. You commit, push and move the root pin next, one button each.</span></div>`);
  }
  return parts.join("");
}

let currentPlan = null;
async function openPlan(loader) {
  try {
    currentPlan = await loader();
  } catch (error) {
    showAlert(error.message, "bad");
    return;
  }
  $("plan").innerHTML = planHtml(currentPlan);
  const dialog = $("plan-dialog");
  if (!dialog.open) dialog.showModal();
}

// ---------------------------------------------------------------- events

document.addEventListener("click", (event) => {
  const target = event.target.closest("button, summary");
  if (!target) return;
  if (target.dataset.plan) {
    event.preventDefault();
    event.stopPropagation();
    openPlan(() => api("/api/plan", { to: target.dataset.plan }));
    return;
  }
  if (target.dataset.backup) {
    const [namespace, name] = target.dataset.backup.split("/");
    act("Backup", "/api/backup", { namespace, name }, () => { $("plan").innerHTML = planHtml(currentPlan); });
    return;
  }
  if (target.id === "plan-write") {
    act("Write", "/api/write", { to: currentPlan.to_ref }, (result) => {
      lastDiff = result.diff;
      $("plan-dialog").close();
    });
    return;
  }
  const action = target.dataset.act;
  if (action === "undo-plan") return void openPlan(() => api("/api/undo-plan", {}));
  if (action === "commit") return void act("Commit", "/api/commit");
  if (action === "discard") return void act("Discard", "/api/discard", {}, () => { lastDiff = null; });
  if (action === "push") return void act("Push", "/api/push");
  if (action === "pin") return void act("Move the root pin", "/api/pin");
  if (action === "refresh-apps") return void act("Hard refresh", "/api/refresh-apps");
});

document.addEventListener("change", (event) => {
  if (event.target.id === "plan-ok") $("plan-write").disabled = !event.target.checked;
  if (event.target.name === "grouping") {
    grouping = event.target.value;
    try { localStorage.setItem("desk.grouping", grouping); } catch (e) { /* fine */ }
    drawPath();
  }
});

document.addEventListener("toggle", (event) => {
  const step = event.target.closest && event.target.closest("details.step");
  if (!step || event.target !== step) return;
  const sha = step.dataset.sha;
  if (step.open) {
    opened.add(sha);
    const found = state.path.steps.find((s) => s.sha === sha);
    step.querySelector(".body").innerHTML = found ? stepBody(found) : "";
  } else {
    opened.delete(sha);
  }
}, true);

$("refresh").addEventListener("click", () => act("Refresh", "/api/refresh"));

async function load() {
  try {
    state = await api("/api/state");
  } catch (error) {
    state = { fatal: error.message };
  }
  draw();
}

for (const input of document.querySelectorAll("input[name=grouping]")) input.checked = input.value === grouping;
load();
setInterval(() => { if (!busy && !$("plan-dialog").open) load(); }, 6000);
