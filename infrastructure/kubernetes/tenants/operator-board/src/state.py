"""One state document from what the sources answered (design §4, §5).

Pure: given the readings and the time, the same document. The tests live here.

Colours (docs/adr/0068):
  red    an alert fires. Only Alertmanager's answer makes anything red.
  amber  something has not settled, as the Board reads it.
  grey   a source did not answer; the last good answer is shown with its age.
  green  the source answered and nothing above holds.
Unknown is never green.
"""
import urllib.parse
from collections import Counter
from dataclasses import dataclass
from datetime import datetime, timezone
from urllib.parse import urlsplit

import argo
import links as links_mod

SCHEMA = 1
GREEN, AMBER, RED, GREY = "green", "amber", "red", "grey"
# The verdict is the worst of these, in this order (design §4.1).
RANK = {RED: 0, AMBER: 1, GREY: 2, GREEN: 3}
MESSAGE_LIMIT = 300

# Member-facing applications; every other Application is the Platform. The same
# list as the Canary Follower's ringstate.py; tests/test_board.py compares them.
COMMUNITY_APPS = frozenset({"bulwark", "collabora", "dashboard", "euro-office", "excalidraw",
                            "forgejo", "immich", "jitsi", "join", "nextcloud", "pod-gateway",
                            "stalwart"})
MACHINERY = frozenset({"canary-follower", "smallworlds-root"})

# What the verdict covers so far, and what it does not read yet. The page says
# both, so that green never claims more than was read (design §11).
COVERED = ["Alertmanager's alerts", "Argo CD Applications"]
NOT_YET = ["the node", "backups", "certificates", "updates", "alert delivery"]

# Severities that fire continuously by design (Watchdog, InfoInhibitor). The push
# route discards them too (doc/alerting.md); they never count here.
QUIET_SEVERITIES = frozenset({"none", "info"})
# The alert that proves Prometheus → Alertmanager works. Missing means the
# pipeline is broken, and no alert can say so: amber (design §4.7).
WATCHDOG = "Watchdog"

# Which tile an alert belongs to, by name prefix, first match wins (design §4.1).
# tests/test_board.py checks every rule in the repository against this table, so
# a new rule has to be placed on purpose: on a tile, or in ON_ALERTS_TILE.
ALERT_TILES = (
    (("CNPG", "Velero", "PodArchive", "Backup"), "backups"),
    (("Node", "KubeNode", "PersistentVolume", "KubeletDown"), "node"),
    (("Application", "KubePod", "KubeDeployment", "KubeStatefulSet", "KubeDaemonSet",
      "KubeJob", "KubeContainer"), "applications"),
    (("Certificate",), "certificates"),
    (("Canary", "Ring0"), "updates"),
)
# Rules of ours that belong on no tile but Alerts itself. Empty so far.
ON_ALERTS_TILE = frozenset()
TILE_TITLES = {"applications": "Applications", "alerts": "Alerts", "backups": "Backups",
               "node": "Node", "certificates": "Certificates", "updates": "Updates"}


@dataclass
class Reading:
    """One source's last attempt. `data` is the last good answer, kept when a
    later attempt fails, so the page can show it greyed with its age."""
    data: object = None
    error: str | None = None
    fetched_at: float | None = None
    last_good_at: float | None = None

    def ok(self) -> bool:
        return self.fetched_at is not None and self.error is None

    def describe(self) -> dict:
        return {"ok": self.ok(), "error": self.error, "fetched_at": iso(self.fetched_at),
                "last_good_at": iso(self.last_good_at)}


def iso(seconds: float | None) -> str | None:
    if seconds is None:
        return None
    return datetime.fromtimestamp(seconds, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _seconds(text: str | None) -> float | None:
    if not text:
        return None
    try:
        return datetime.fromisoformat(text.replace("Z", "+00:00")).timestamp()
    except ValueError:
        return None


def short(text: str | None) -> str | None:
    if not text:
        return None
    text = " ".join(text.split())
    return text if len(text) <= MESSAGE_LIMIT else text[:MESSAGE_LIMIT - 1] + "…"


def group_of(name: str) -> str:
    if name in MACHINERY:
        return "machinery"
    return "apps" if name in COMMUNITY_APPS else "platform"


# ------------------------------------------------------------------ alerts

def tile_of(alertname: str, labels: dict | None = None) -> str:
    for prefixes, tile in ALERT_TILES:
        if alertname.startswith(prefixes):
            return tile
    if alertname == "TargetDown" and (labels or {}).get("job") == "node-exporter":
        return "node"
    return "alerts"


def alert(raw: dict, grafana_url: str | None) -> dict | None:
    """One alert as the page shows it, or None for the ones that never count."""
    labels = raw.get("labels") or {}
    name = labels.get("alertname", "?")
    severity = labels.get("severity", "warning")
    if severity in QUIET_SEVERITIES:
        return None
    status = raw.get("status") or {}
    suppressed = bool(status.get("silencedBy") or status.get("inhibitedBy") or status.get("mutedBy")
                      or status.get("state") == "suppressed")
    where = next((labels[k] for k in ("name", "pod", "persistentvolumeclaim", "cluster", "job",
                                      "namespace") if labels.get(k)), None)
    annotations = raw.get("annotations") or {}
    return {
        "name": name,
        "severity": severity,
        "tile": tile_of(name, labels),
        "where": where,
        "namespace": labels.get("namespace"),
        "app": labels.get("name") if name.startswith("Application") else None,
        "summary": short(annotations.get("summary") or annotations.get("description")),
        "since": raw.get("startsAt"),
        "suppressed": suppressed,
        "link": (f"{grafana_url}/alerting/list?search={urllib.parse.quote(name)}"
                 if grafana_url else None),
    }


def _alert_text(a: dict) -> str:
    where = f"{a['where']}, " if a["where"] else ""
    text = f"{a['name']} ({where}{a['severity']})"
    return f"{text}: {a['summary']}" if a["summary"] else text


def alerts_tile(firing: list[dict], suppressed: list[dict], watchdog: bool, reading: Reading) -> dict:
    severities = Counter(a["severity"] for a in firing)
    detail = ", ".join(f"{n} {s}" for s, n in sorted(severities.items())) if firing else ""
    if suppressed:
        detail = f"{detail} · {len(suppressed)} silenced or inhibited".lstrip(" ·")
    if not reading.ok():
        colour = GREY
    elif firing:
        colour = RED
    elif not watchdog:
        colour = AMBER
        detail = "Watchdog is not firing: alerts may not reach Alertmanager"
    else:
        colour = GREEN
        detail = detail or "Watchdog firing: the alert pipeline works"
    return {"id": "alerts", "title": "Alerts", "colour": colour,
            "headline": (f"{len(firing)} firing" if firing else "none firing") if reading.ok()
            else "not read",
            "detail": detail}


# ------------------------------------------------------------- applications

def why(app: dict) -> str | None:
    """The reason Argo CD gives for what has not settled, or None."""
    if found := argo._upstream_unreachable(app):
        return f"{argo.UNREACHABLE} {found}"
    if failure := argo._sync_failure(app):
        return failure
    status = app.get("status", {})
    for condition in status.get("conditions", []):
        if condition.get("message"):
            return f"{condition.get('type', 'Condition')}: {condition['message']}"
    health = status.get("health", {})
    if health.get("status") not in (None, "Healthy") and health.get("message"):
        return health["message"]
    for resource in status.get("resources", []):
        state = resource.get("health", {})
        if state.get("status") not in (None, "Healthy"):
            text = f"{resource.get('kind')} {resource.get('name')}: {state['status']}"
            return f"{text} ({state['message']})" if state.get("message") else text
    operation = status.get("operationState", {})
    if argo._operation_running(app) and operation.get("message"):
        return operation["message"]
    return None


def application(app: dict, now: float, argocd_url: str | None, slow_minutes: int,
                firing: list[dict] = ()) -> dict:
    meta = app.get("metadata", {})
    name = meta.get("name", "?")
    namespace = meta.get("namespace", "argocd")
    destination = app.get("spec", {}).get("destination", {}).get("namespace")
    sync, health = argo._sync_status(app), argo._health(app)
    running = argo._operation_running(app)
    started = _seconds(app.get("status", {}).get("operationState", {}).get("startedAt"))
    running_minutes = int((now - started) // 60) if running and started else None
    failure = argo._sync_failure(app)
    # An alert is this application's when it names it (the Application alerts)
    # or fires in the namespace it deploys to.
    mine = sorted({a["name"] for a in firing
                   if a["app"] == name or (destination and a["namespace"] == destination)})
    if mine:
        colour = RED
    elif sync == "Unknown" and health == "Unknown":
        colour = GREY
    elif (sync != "Synced" or health != "Healthy" or failure
          or (running_minutes is not None and running_minutes >= slow_minutes)):
        colour = AMBER
    else:
        colour = GREEN
    try:
        wave = int((meta.get("annotations") or {}).get("argocd.argoproj.io/sync-wave", "0"))
    except ValueError:
        wave = 0
    return {
        "name": name,
        "group": group_of(name),
        "sync": sync,
        "health": health,
        "colour": colour,
        "alerts": mine,
        "why": short(why(app)) if colour != GREEN else None,
        "running_minutes": running_minutes,
        "wave": wave,
        "link": f"{argocd_url}/applications/{namespace}/{name}" if argocd_url else None,
    }


def base_revision(apps: list[dict], base_repo_url: str) -> tuple[str | None, list[str]]:
    """The base commit this cluster runs, and the Applications that run another
    one. After the Update Desk's base-pin block they all name the same commit."""
    by_app = {}
    for app in apps:
        for source, revision in zip(argo._sources(app), argo._revisions(app)):
            if source.get("repoURL", "").rstrip("/") == base_repo_url and revision:
                by_app[app["metadata"]["name"]] = revision
    if not by_app:
        return None, []
    common = Counter(by_app.values()).most_common(1)[0][0]
    return common, sorted(f"{n}@{r[:12]}" for n, r in by_app.items() if r != common)


def _root_repo(apps: list[dict], root_app: str) -> str | None:
    for app in apps:
        if app.get("metadata", {}).get("name") == root_app:
            sources = argo._sources(app)
            return sources[0].get("repoURL") if sources else None
    return None


def _source_reason(name: str, reading: Reading, now: float) -> dict:
    if reading.last_good_at is None:
        text = f"{name} did not answer: {reading.error}. Nothing has been read from it yet."
    else:
        minutes = int((now - reading.last_good_at) // 60)
        text = (f"{name} did not answer: {reading.error}. "
                f"What is shown is its last answer, {minutes} min old.")
    return {"colour": GREY, "text": text, "link": None, "tile": None}


def applications_tile(cards: list[dict], reading: Reading, firing: list[dict]) -> dict:
    total = len(cards)
    settled = sum(1 for c in cards if c["colour"] == GREEN)
    counts = Counter()
    for card in cards:
        if card["colour"] != GREEN:
            if card["sync"] != "Synced":
                counts[card["sync"]] += 1
            if card["health"] != "Healthy":
                counts[card["health"]] += 1
            if card["sync"] == "Synced" and card["health"] == "Healthy" and not card["alerts"]:
                counts["last sync failed" if card["why"] else "slow operation"] += 1
    mine = [a for a in firing if a["tile"] == "applications"]
    if mine or any(c["colour"] == RED for c in cards):
        colour = RED
    elif not reading.ok():
        colour = GREY
    elif settled < total:
        colour = AMBER
    else:
        colour = GREEN if total else GREY
    detail = ", ".join(f"{n} {what}" for what, n in sorted(counts.items()))
    if mine:
        detail = f"{len(mine)} alert{'s' if len(mine) != 1 else ''} firing" + (f"; {detail}" if detail else "")
    return {
        "id": "applications",
        "title": "Applications",
        "colour": colour,
        "headline": f"{settled} of {total} settled" if total else "none read",
        "detail": detail or ("Synced and Healthy" if total else ""),
    }


def verdict(tiles: list[dict], reasons: list[dict], suppressed: list[dict]) -> dict:
    colour = min((t["colour"] for t in tiles), key=RANK.get, default=GREY)
    for reason in reasons:
        if RANK[reason["colour"]] < RANK[colour]:
            colour = reason["colour"]
    firing = sum(1 for r in reasons if r["colour"] == RED)
    unsettled = sum(1 for r in reasons if r["colour"] == AMBER)
    if colour == RED:
        headline = f"{firing} {'alert' if firing == 1 else 'alerts'} firing"
        if unsettled:
            headline += f", and {unsettled} {'thing has' if unsettled == 1 else 'things have'} not settled"
    elif colour == AMBER:
        headline = f"{unsettled} {'thing has' if unsettled == 1 else 'things have'} not settled"
    elif colour == GREY:
        headline = "Part of the cluster could not be read"
    else:
        headline = "Nothing needs attention"
    return {"colour": colour, "headline": headline,
            "reasons": sorted(reasons, key=lambda r: RANK[r["colour"]]),
            "suppressed": [{"text": _alert_text(a), "link": a["link"]} for a in suppressed],
            "covers": COVERED, "not_yet": NOT_YET}


def build(applications: Reading, ingresses: Reading, alerts: Reading, now: float, *,
          base_repo_url: str, root_app: str, ring0_board_url: str, refresh_seconds: int,
          slow_minutes: int) -> dict:
    apps = (applications.data or {}).get("items", []) if isinstance(applications.data, dict) else []
    found = links_mod.from_ingresses((ingresses.data or {}).get("items", [])
                                     if isinstance(ingresses.data, dict) else [])

    def tool(tool_id: str) -> str | None:
        return next((f"https://{urlsplit(l['url']).netloc}" for l in found if l.get("id") == tool_id),
                    None)

    shown = [a for a in (alert(raw, tool("grafana")) for raw in
                         (alerts.data if isinstance(alerts.data, list) else [])) if a]
    firing = [a for a in shown if not a["suppressed"]]
    suppressed = [a for a in shown if a["suppressed"]]
    watchdog = any((raw.get("labels") or {}).get("alertname") == WATCHDOG
                   for raw in (alerts.data if isinstance(alerts.data, list) else []))

    cards = [application(a, now, tool("argocd"), slow_minutes, firing) for a in apps]
    cards.sort(key=lambda c: (RANK[c["colour"]], c["wave"], c["name"]))
    revision, mixed = base_revision(apps, base_repo_url)

    reasons = [{"colour": RED, "text": _alert_text(a), "link": a["link"], "tile": a["tile"]}
               for a in sorted(firing, key=lambda a: (a["severity"] != "critical", a["name"]))]
    if alerts.ok() and not watchdog:
        reasons.append({"colour": AMBER, "tile": "alerts", "link": tool("grafana"),
                        "text": "Watchdog is not firing: Prometheus may not be reaching Alertmanager, "
                                "so other alerts would not either."})
    # A red card is one problem, already named by its alert's reason; the card
    # itself still shows what Argo CD says.
    for card in cards:
        if card["colour"] in (AMBER, GREY):
            text = f"{card['name']}: {card['sync']} / {card['health']}"
            if card["running_minutes"] is not None:
                text += f", operation running for {card['running_minutes']} min"
            if card["why"]:
                text += f" — {card['why']}"
            reasons.append({"colour": card["colour"], "text": text, "link": card["link"],
                            "tile": "applications"})
    for name, reading in (("Alertmanager", alerts),
                          ("The Kubernetes API (Applications)", applications),
                          ("The Kubernetes API (Ingresses)", ingresses)):
        if reading.fetched_at is not None and not reading.ok():
            reasons.append(_source_reason(name, reading, now))
        elif reading.fetched_at is None:
            reasons.append({"colour": GREY, "text": f"{name} has not been read yet.",
                            "link": None, "tile": None})

    tiles = [alerts_tile(firing, suppressed, watchdog, alerts),
             applications_tile(cards, applications, firing)]
    return {
        "schema": SCHEMA,
        "generated": iso(now),
        "refresh_seconds": refresh_seconds,
        "verdict": verdict(tiles, reasons, suppressed),
        "tiles": tiles,
        "alerts": {"firing": firing, "suppressed": suppressed, "watchdog": watchdog},
        "applications": cards,
        "base": {"revision": revision, "mixed": mixed},
        "links": links_mod.ordered(found + links_mod.fixed(
            base_repo_url, revision, _root_repo(apps, root_app), ring0_board_url)),
        "sources": {"alerts": alerts.describe(), "applications": applications.describe(),
                    "ingresses": ingresses.describe()},
    }
