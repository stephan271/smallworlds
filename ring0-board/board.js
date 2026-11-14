// Ring 0 Board: draws ring0.json (written by the Canary Follower on Ring 0) as
// cards. Read-only and dependency-free. Every string from the file goes in as
// text, never as HTML: PR titles and commit messages are anyone's words.
"use strict";

const DEFAULT_DATA = "https://raw.githubusercontent.com/stephan271/smallworlds/ring-state/ring0.json";
const REFRESH_MS = 60_000;
// The follower writes on every change and at least hourly; older than this
// means it stopped reporting, and the board says so instead of looking calm.
const STALE_MS = 2.5 * 3600_000;

const STEPS = ["backup", "sync", "converge", "settle", "smoke", "checks", "storage", "quiet"];
const STEP_TEXT = {
  start: "starting", backup: "pre-update backup", sync: "syncing", converge: "waiting for every app to be healthy",
  settle: "letting the node settle", smoke: "smoke test of every address", checks: "end-to-end and data checks",
  storage: "storage checks (Garage, replicator)", quiet: "quiet period (no critical alert)",
};

const ROLES = {
  bulwark: "Webmail", collabora: "Document editor", dashboard: "Member homepage", "euro-office": "Document editor",
  excalidraw: "Whiteboard", forgejo: "Git hosting", immich: "Photos (Gallery)", jitsi: "Video calls",
  join: "Member invitations", nextcloud: "Files & calendars", "pod-gateway": "Personal data pods",
  stalwart: "Mail server", "cert-manager": "TLS certificates", "cert-manager-webhook-hetzner": "DNS challenges",
  "cloudnative-pg": "PostgreSQL operator", "cnpg-barman-plugin": "Database backups to S3", traefik: "Edge router",
  keycloak: "Single sign-on", headscale: "Operator VPN", "kube-prometheus-stack": "Metrics & alerts",
  "loki-stack": "Logs", velero: "Cluster backups", garage: "S3 storage", "garage-backup": "S3 backup",
  "backup-replicator": "Offsite copies", "canary-follower": "Follows main, verifies, merges updates",
  "smallworlds-root": "App of apps, shared bases, alert rules", repository: "Docs, scripts, Terraform: nothing Ring 0 runs",
  hermes: "Removed agent", remediation: "Removed agent",
};

// Which image is "the version" of an app; otherwise the first image that
// names the app, otherwise the first image.
const MAIN_IMAGE = {
  immich: /gallery-server|immich-server/, nextcloud: /\/nextcloud:/, forgejo: /forgejo\/forgejo/, jitsi: /jitsi\/web/,
  stalwart: /stalwart:/, bulwark: /webmail/, dashboard: /homepage/, "euro-office": /documentserver/,
  keycloak: /keycloak\/keycloak/, velero: /velero\/velero:/, "cnpg-barman-plugin": /plugin-barman-cloud:/,
  join: /python/, "pod-gateway": /python/, "canary-follower": /python/,
};

const GROUP_TITLES = { apps: "cards-apps", platform: "cards-platform", machinery: "cards-machinery" };

let data = null;

// ------------------------------------------------------------ helpers

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return el;
}

function chip(kind, text, title) {
  return h("span", { class: `chip ${kind}`, title: title || null }, text);
}

function ago(iso) {
  if (!iso) return "";
  const minutes = Math.round((Date.now() - Date.parse(iso)) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `${hours} h ago`;
  return `${Math.round(hours / 24)} days ago`;
}

function clock(iso) {
  if (!iso) return "";
  return new Date(iso).toLocaleString(undefined, { weekday: "short", hour: "2-digit", minute: "2-digit" });
}

function minutesSince(iso) {
  return iso ? Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 60000)) : null;
}

function repoUrl(path) {
  return `https://github.com/${data.repository}${path}`;
}

function pullLink(number) {
  return h("a", { href: repoUrl(`/pull/${number}`), target: "_blank", rel: "noopener" }, `#${number}`);
}

function commitLink(sha) {
  return h("a", { href: repoUrl(`/commit/${sha}`), target: "_blank", rel: "noopener", class: "mono" }, sha.slice(0, 7));
}

function imageVersion(ref) {
  const name = ref.split("@")[0];
  const slash = name.lastIndexOf("/");
  const colon = name.lastIndexOf(":");
  const repo = colon > slash ? name.slice(0, colon) : name;
  const tag = colon > slash ? name.slice(colon + 1) : "(digest)";
  return { short: `${repo.slice(repo.lastIndexOf("/") + 1)}:${tag}`, full: name };
}

function mainVersion(app) {
  if (!app) return null;
  if (app.source === "chart") return `${app.chart} chart ${app.version}`;
  if (!app.images.length) return null;
  const hint = MAIN_IMAGE[app.name];
  const image = (hint && app.images.find((i) => hint.test(i)))
    || app.images.find((i) => i.split("@")[0].includes(app.name))
    || app.images[0];
  return imageVersion(image).short;
}

// ------------------------------------------------------------ verdicts

const VERDICT = {
  success: ["ok", "✓ verified"],
  failure: ["bad", "✕ rejected"],
  error: ["wait", "! not judged"],
  pending: ["busy", "◌ verifying"],
};

// A commit left at `pending` that Ring 0 is no longer checking was superseded:
// main moved on while it was being verified, and nobody will finish it.
function verdictOf(commit) {
  const v = commit.verdict;
  if (v && v.state === "pending" && commit.commit !== data.now.commit) return null;
  return v;
}

function verdictChip(verdict) {
  if (!verdict) return chip("", "– not verified alone", "Ring 0 moved on to a newer commit before this one was checked");
  const [kind, text] = VERDICT[verdict.state] || ["", verdict.state];
  return chip(kind, text, verdict.description || null);
}

// ------------------------------------------------------------ per card

function cardFacts(name) {
  const app = data.applications.find((a) => a.name === name) || null;
  const now = data.now;
  const nowCards = (data.commits.find((c) => c.commit === now.commit) || {}).cards || (now.pull && now.pull.cards) || [];
  const checking = now.state === "updating" && STEPS.concat(["start"]).includes(now.step) && nowCards.includes(name);
  const queued = data.pulls.filter((p) => p.queued && !p.rejected && p.cards.includes(name));
  const rejected = data.pulls.filter((p) => p.rejected && p.cards.includes(name));
  const other = data.pulls.filter((p) => !p.queued && !p.rejected && p.cards.includes(name));
  const changes = data.commits.filter((c) => c.cards.includes(name));
  const last = changes.find((c) => c.verdict && c.verdict.state !== "pending") || null;
  return { name, app, checking, queued, rejected, other, changes, last };
}

function healthKind(app) {
  if (!app) return "";
  if (["Degraded", "Missing"].includes(app.health)) return "bad";
  if (app.health === "Progressing" || app.sync === "OutOfSync") return "wait";
  if (app.health === "Healthy") return "ok";
  return "";
}

function cardKind(f) {
  if (healthKind(f.app) === "bad" || (f.last && f.last.verdict.state === "failure" && f.changes[0] === f.last)) return "bad";
  if (f.checking) return "busy";
  if (f.queued.length || healthKind(f.app) === "wait") return "wait";
  return f.app ? healthKind(f.app) : "";
}

function cardFlags(f) {
  const flags = [];
  if (f.checking) flags.push(chip("busy", `▶ checking now · ${STEP_TEXT[data.now.step] || data.now.step}`));
  for (const p of f.queued) flags.push(chip("wait", `⏳ queued #${p.number}`, p.title));
  for (const p of f.rejected) flags.push(chip("bad", `✕ rejected #${p.number}`, p.title));
  for (const p of f.other) flags.push(chip("", `○ open #${p.number}`, `${p.title} (not in the merge queue)`));
  if (f.last && !f.checking) {
    const [kind, text] = VERDICT[f.last.verdict.state] || ["", f.last.verdict.state];
    flags.push(chip(kind, `last change ${text.slice(2)} ${ago(f.last.verdict.date)}`, f.last.title));
  }
  return flags;
}

function card(f) {
  const kind = cardKind(f);
  const app = f.app;
  const status = app
    ? [h("i", { class: `dot ${healthKind(app)}`, "aria-hidden": "true" }), app.health,
       app.sync !== "Synced" ? chip("wait", app.sync) : null]
    : [h("i", { class: "dot", "aria-hidden": "true" }), "not on Ring 0"];
  const version = mainVersion(app);
  return h("button", { type: "button", class: `card ${kind}`, onclick: () => openDetail(f.name),
                       "aria-label": `${f.name}: details` },
    h("div", { class: "card-name" }, f.name),
    ROLES[f.name] ? h("div", { class: "card-role" }, ROLES[f.name]) : null,
    h("div", { class: "card-row" }, status),
    version ? h("div", { class: "card-version", title: version }, version) : null,
    h("div", { class: "flags" }, cardFlags(f)));
}

// ------------------------------------------------------------ banner

function banner() {
  const now = data.now;
  const el = document.getElementById("banner");
  const title = now.pull ? `${now.pull.title} (#${now.pull.number})`
    : ((data.commits.find((c) => c.commit === now.commit) || {}).title || now.commit.slice(0, 7));
  let kind, label, head, sub = "";
  if (now.state === "stuck") {
    kind = "bad"; label = "Stuck";
    head = `Ring 0 is stuck since ${clock(now.stuck.since)}`;
    sub = `${now.stuck.summary || ""} Nothing new is merged until a person resolves it.`;
  } else if (now.state === "up-to-date") {
    kind = "ok"; label = "Up to date";
    head = "Ring 0 is up to date";
    sub = `main's head ${now.commit.slice(0, 7)} verified ${ago(data.head.date)}. ${data.head.description || ""}`;
  } else if (now.step === "merge") {
    kind = "ok"; label = "Green";
    head = "Ring 0 is green; the next update is merged within minutes";
  } else if (now.step === "retry") {
    kind = "wait"; label = "Retrying";
    head = `Ring 0 could not judge ${title}; it tries again`;
    sub = data.head.description || "";
  } else if (now.step === "waiting") {
    kind = "busy"; label = "Starting";
    head = `Ring 0 is about to check ${title}`;
  } else {
    kind = "busy"; label = "Checking";
    head = `Ring 0 is checking ${title}`;
    const m = minutesSince(now.started);
    sub = `${STEP_TEXT[now.step] || now.step} for ${minutesSince(now.step_since)} min · started ${m} min ago`;
  }
  const steps = now.state === "updating" && STEPS.concat(["start"]).includes(now.step)
    ? h("ol", { class: "steps", "aria-label": "Verification steps" },
        STEPS.map((s, i) => {
          const at = STEPS.indexOf(now.step);
          const cls = s === now.step ? "now" : (at > i ? "done" : "");
          return h("li", { class: cls, "aria-current": s === now.step ? "step" : null }, s);
        }))
    : null;
  const queue = now.queued.length
    ? h("div", { class: "queue" }, `${now.queued.length} update${now.queued.length > 1 ? "s" : ""} waiting in the merge queue, oldest first:`,
        h("ol", {}, now.queued.map((q) => h("li", {}, pullLink(q.number), " ", q.title))))
    : h("p", { class: "queue" }, "No update is waiting in the merge queue.");
  el.className = `banner ${kind}`;
  el.replaceChildren(...[
    h("span", { class: `banner-state chip ${kind}` }, label),
    h("p", { class: "banner-head" }, head),
    sub ? h("p", { class: "banner-sub" }, sub) : null,
    steps, queue,
    now.action ? h("p", { class: "queue" }, `Last action of the merge queue: ${now.action}`) : null,
  ].filter(Boolean));
}

// ------------------------------------------------------------ lists

function pullItem(p) {
  const flags = [];
  if (p.rejected) flags.push(chip("bad", "rejected by Ring 0"));
  else if (p.queued) flags.push(chip("wait", "in the merge queue"));
  else flags.push(chip("", p.author === "renovate[bot]" ? "not queued (major or excluded)" : "needs a person to merge"));
  if (p.draft) flags.push(chip("", "draft"));
  for (const c of p.cards) flags.push(chip("", c));
  return h("li", {},
    h("div", { class: "row1" }, pullLink(p.number), h("span", {}, p.title)),
    h("div", { class: "meta" }, `${p.author} · opened ${ago(p.created)}`),
    h("div", { class: "flags" }, flags));
}

function commitItem(c) {
  return h("li", {},
    h("div", { class: "row1" }, commitLink(c.commit), h("span", {}, c.title)),
    h("div", { class: "meta" }, `${c.author || "?"} · ${ago(c.date)}`, c.pull ? [" · ", pullLink(c.pull)] : null),
    h("div", { class: "flags" }, verdictChip(verdictOf(c)), c.cards.map((x) => chip("", x))),
    verdictOf(c) && c.verdict.description && c.verdict.state !== "pending"
      ? h("div", { class: "meta" }, c.verdict.description) : null);
}

const shownAll = new Set();

function fillList(id, items, render, empty, limit = Infinity) {
  const el = document.getElementById(id);
  const all = shownAll.has(id) || items.length <= limit;
  const rows = (all ? items : items.slice(0, limit)).map(render);
  if (!all) {
    rows.push(h("li", {}, h("button", { type: "button", class: "more",
      onclick: () => { shownAll.add(id); fillList(id, items, render, empty, limit); } },
      `Show all ${items.length}`)));
  }
  el.replaceChildren(...(items.length ? rows : [h("li", { class: "empty" }, empty)]));
}

// ------------------------------------------------------------ detail

function openDetail(name) {
  const f = cardFacts(name);
  const app = f.app;
  const body = [];
  if (ROLES[name]) body.push(h("p", { class: "card-role" }, ROLES[name]));
  body.push(h("div", { class: "flags" }, cardFlags(f)));
  if (app) {
    const op = app.operation || {};
    body.push(h("dl", { class: "facts" },
      h("dt", {}, "Health"), h("dd", {}, h("i", { class: `dot ${healthKind(app)}` }), " ", app.health),
      h("dt", {}, "Sync"), h("dd", {}, app.sync),
      h("dt", {}, "Comes from"), h("dd", {}, {
        chart: `Helm chart ${app.chart} ${app.version}`,
        base: "this repository's main, directly",
        overlay: "this repository's main, through Ring 0's overlay",
      }[app.source]),
      app.revision ? [h("dt", {}, "Revision"), h("dd", { class: "mono" }, app.revision.slice(0, 12))] : null,
      h("dt", {}, "Last sync"), h("dd", {}, op.phase ? `${op.phase} ${ago(op.finished)}` : "—"),
      h("dt", {}, "Sync wave"), h("dd", {}, String(app.wave))));
    if (op.message) body.push(h("p", { class: "message" }, op.message));
    if (app.images.length) {
      body.push(h("h3", {}, "Running images"),
        h("ul", { class: "images" }, app.images.map((i) => h("li", {}, imageVersion(i).full))));
    }
  } else {
    body.push(h("p", {}, name === "repository"
      ? "Changes here touch nothing Ring 0 runs (documentation, scripts, Terraform). Ring 0 still verifies the commit."
      : "Ring 0 does not run this, so it cannot verify an update to it. Such a PR is not queued: it waits until a person merges or closes it."));
  }
  const pulls = [...f.queued, ...f.rejected, ...f.other];
  body.push(h("h3", {}, "Open pull requests"),
    h("ol", { class: "list" }, pulls.length ? pulls.map(pullItem) : h("li", { class: "empty" }, "None.")));
  body.push(h("h3", {}, `Recent changes on main (of the last ${data.commits.length})`),
    h("ol", { class: "list" }, f.changes.length ? f.changes.map(commitItem) : h("li", { class: "empty" }, "None.")));
  document.getElementById("detail-title").textContent = name;
  document.getElementById("detail-body").replaceChildren(...body);
  const dialog = document.getElementById("detail");
  if (!dialog.open) dialog.showModal();
  dialog.dataset.card = name;
}

// ------------------------------------------------------------ draw

function draw() {
  banner();
  const known = new Set(data.applications.map((a) => a.name));
  for (const id of Object.values(GROUP_TITLES)) document.getElementById(id).replaceChildren();
  for (const app of data.applications) {
    document.getElementById(GROUP_TITLES[app.group] || "cards-platform").append(card(cardFacts(app.name)));
  }
  // A card Ring 0 does not run is shown only while a PR is open for it: that
  // update cannot be verified here and waits for a person. Commits alone
  // (e.g. the removal of something that no longer exists) do not keep it.
  const elsewhere = new Set();
  for (const item of data.pulls) {
    for (const c of item.cards) if (!known.has(c)) elsewhere.add(c);
  }
  const repo = elsewhere.delete("repository") || data.commits.some((c) => c.cards.includes("repository"));
  document.getElementById("cards-machinery").append(...(repo ? [card(cardFacts("repository"))] : []));
  document.getElementById("sec-elsewhere").hidden = elsewhere.size === 0;
  document.getElementById("cards-elsewhere").replaceChildren(...[...elsewhere].sort().map((n) => card(cardFacts(n))));
  fillList("pulls", data.pulls, pullItem, "No open pull request.");
  fillList("commits", data.commits, commitItem, "No commit.", 10);

  const asOf = document.getElementById("as-of");
  asOf.textContent = `As of ${clock(data.updated)} (${ago(data.updated)})`;
  const problem = document.getElementById("problem");
  const stale = Date.now() - Date.parse(data.updated) > STALE_MS;
  problem.hidden = !stale;
  problem.textContent = stale ? `The follower has not reported since ${clock(data.updated)}. What you see may be out of date; Ring 0's real state is unknown.` : "";

  const dialog = document.getElementById("detail");
  if (dialog.open && dialog.dataset.card) openDetail(dialog.dataset.card);
}

async function load() {
  const source = new URLSearchParams(location.search).get("data") || DEFAULT_DATA;
  document.getElementById("data-link").href = source;
  try {
    const response = await fetch(`${source}${source.includes("?") ? "&" : "?"}t=${Date.now()}`, { cache: "no-store" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    data = await response.json();
    draw();
  } catch (error) {
    const problem = document.getElementById("problem");
    problem.hidden = false;
    problem.textContent = data
      ? `Could not refresh (${error.message}); showing what was loaded ${ago(data.updated)}.`
      : `ring0.json could not be loaded (${error.message}). The Canary Follower may not have published it yet.`;
    if (!data) document.getElementById("as-of").textContent = "No data";
  }
}

document.getElementById("detail-close").addEventListener("click", () => document.getElementById("detail").close());
document.getElementById("detail").addEventListener("click", (event) => {
  if (event.target.id === "detail") event.target.close();  // a click on the backdrop
});
load();
setInterval(load, REFRESH_MS);
