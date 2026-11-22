// Operator Board (docs/adr/0068): draws /api/state. Read-only and
// dependency-free. Every string goes in as text, never as HTML: Argo CD's
// messages carry whatever a failing resource said.
"use strict";

const STALE_FACTOR = 3;  // the document is stale after this many refreshes
const KIND = { green: "ok", amber: "wait", red: "bad", grey: "idle" };
const STATE_TEXT = { green: "All well", amber: "Not settled", red: "Attention", grey: "Unknown" };
const GROUP_ORDER = ["Operate", "Members", "Alerts", "Updates", "Reference"];

const ROLES = {
  bulwark: "Webmail", collabora: "Document editor", dashboard: "Member homepage", "euro-office": "Document editor",
  excalidraw: "Whiteboard", forgejo: "Git hosting", immich: "Photos", jitsi: "Video calls",
  join: "Member invitations", nextcloud: "Files & calendars", "pod-gateway": "Personal data pods",
  stalwart: "Mail server", "cert-manager": "TLS certificates", "cert-manager-webhook-hetzner": "DNS challenges",
  "cloudnative-pg": "PostgreSQL operator", "cnpg-barman-plugin": "Database backups to S3", traefik: "Edge router",
  keycloak: "Single sign-on", headscale: "Operator VPN", "member-network": "Member VPN",
  "kube-prometheus-stack": "Metrics & alerts", "loki-stack": "Logs", velero: "Cluster backups",
  garage: "S3 storage", "garage-backup": "S3 backup", "backup-replicator": "Offsite copies",
  "canary-follower": "Follows main, verifies, merges updates", "smallworlds-root": "App of apps, shared bases, alert rules",
  "operator-board": "This page",
};

let data = null;
let refreshMs = 30_000;
let timer = null;

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === null || v === undefined || v === false) continue;
    if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    el.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return el;
}

function dot(colour) {
  return h("i", { class: `dot ${KIND[colour] || ""}`, "aria-hidden": "true" });
}

function chip(kind, text) {
  return h("span", { class: `chip ${kind}` }, text);
}

function ago(iso) {
  if (!iso) return "never";
  const minutes = Math.round((Date.now() - Date.parse(iso)) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  return hours < 36 ? `${hours} h ago` : `${Math.round(hours / 24)} days ago`;
}

function clock(iso) {
  return iso ? new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "";
}

function link(url, text) {
  return url ? h("a", { href: url, target: "_blank", rel: "noopener" }, text) : text;
}

// ------------------------------------------------------------ sections

function drawBanner() {
  const v = data.verdict;
  const banner = document.getElementById("banner");
  banner.className = `banner ${KIND[v.colour] === "idle" ? "" : KIND[v.colour]}`;
  const state = document.getElementById("banner-state");
  state.className = `banner-state chip ${KIND[v.colour]}`;
  state.textContent = STATE_TEXT[v.colour] || v.colour;
  document.getElementById("banner-head").textContent = v.headline;
  document.getElementById("reasons").replaceChildren(...v.reasons.map((r) =>
    h("li", {}, dot(r.colour), h("span", {}, r.text, r.link ? " " : null, r.link ? link(r.link, "→ open") : null))));
  const notYet = v.not_yet && v.not_yet.length ? ` Not read yet: ${v.not_yet.join(", ")}.` : "";
  document.getElementById("coverage").textContent = `This verdict covers ${v.covers.join(", ")}.${notYet}`;
}

function drawTiles() {
  document.getElementById("tiles").replaceChildren(...data.tiles.map((t) =>
    h("div", { class: `tile ${KIND[t.colour]}` },
      h("div", { class: "tile-title" }, t.title),
      h("div", { class: "tile-head" }, dot(t.colour), t.headline),
      t.detail ? h("div", { class: "tile-detail" }, t.detail) : null)));
}

function drawHub() {
  const groups = new Map(GROUP_ORDER.map((g) => [g, []]));
  for (const l of data.links) (groups.get(l.group) || groups.get("Operate")).push(l);
  const sections = [];
  for (const [name, items] of groups) {
    if (!items.length) continue;
    sections.push(h("section", { class: "hub-group" }, h("h3", {}, name),
      h("ul", {}, items.map((l) => h("li", {},
        link(l.url, l.name),
        l.note ? h("span", { class: "hub-note" }, l.note) : null,
        l.command ? h("code", { class: "hub-command" }, l.command) : null)))));
  }
  document.getElementById("hub").replaceChildren(...sections);
}

function card(app) {
  const kind = KIND[app.colour];
  const status = [dot(app.colour), app.health, app.sync !== "Synced" ? chip("wait", app.sync) : null,
    app.running_minutes !== null ? chip("busy", `operation ${app.running_minutes} min`) : null];
  const body = [
    h("div", { class: "card-name" }, app.name),
    ROLES[app.name] ? h("div", { class: "card-role" }, ROLES[app.name]) : null,
    h("div", { class: "card-row" }, status),
    app.why ? h("div", { class: "card-why" }, app.why) : null,
  ];
  return app.link
    ? h("a", { class: `card ${kind}`, href: app.link, target: "_blank", rel: "noopener",
               "aria-label": `${app.name} in Argo CD` }, body)
    : h("div", { class: `card ${kind}` }, body);
}

function drawCards() {
  document.getElementById("cards").replaceChildren(...data.applications.map(card));
}

function draw() {
  document.getElementById("as-of").textContent = `as of ${clock(data.generated)}`;
  drawBanner();
  drawTiles();
  drawHub();
  drawCards();
  const stale = Date.now() - Date.parse(data.generated) > STALE_FACTOR * refreshMs;
  showProblem(stale ? `The Board has not refreshed since ${clock(data.generated)} (${ago(data.generated)}). ` +
    "What you see may be out of date; the cluster's real state is unknown." : null);
}

function showProblem(text) {
  const problem = document.getElementById("problem");
  problem.hidden = !text;
  problem.textContent = text || "";
}

async function load() {
  try {
    const response = await fetch("/api/state", { cache: "no-store" });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    data = await response.json();
    refreshMs = (data.refresh_seconds || 30) * 1000;
    draw();
  } catch (error) {
    if (data) {
      draw();
      showProblem(`The Board did not answer (${error.message}). Showing what it said ${ago(data.generated)}; ` +
        "the cluster may be down — the heartbeat on healthchecks.io says whether it is.");
    } else {
      showProblem(`The Board has nothing to show yet (${error.message}). It reads the cluster when it starts; ` +
        "try again in a moment.");
      document.getElementById("as-of").textContent = "No data";
    }
  }
  clearTimeout(timer);
  timer = setTimeout(load, refreshMs);
}

document.getElementById("host").textContent = location.host;
load();
