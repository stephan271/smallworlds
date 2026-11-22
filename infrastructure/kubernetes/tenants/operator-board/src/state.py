"""One state document from what the sources answered (design §4, §5).

Pure: given the readings and the time, the same document. The tests live here.

Colours (docs/adr/0068):
  red    an alert fires. Only Alertmanager can make a tile red (issue 02);
         nothing in this file does.
  amber  something has not settled, as the Board reads it.
  grey   a source did not answer; the last good answer is shown with its age.
  green  the source answered and nothing above holds.
Unknown is never green.
"""
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
COVERED = ["Argo CD Applications"]
NOT_YET = ["alerts", "the node", "backups", "certificates", "updates", "alert delivery"]


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


def application(app: dict, now: float, argocd_url: str | None, slow_minutes: int) -> dict:
    meta = app.get("metadata", {})
    name = meta.get("name", "?")
    namespace = meta.get("namespace", "argocd")
    sync, health = argo._sync_status(app), argo._health(app)
    running = argo._operation_running(app)
    started = _seconds(app.get("status", {}).get("operationState", {}).get("startedAt"))
    running_minutes = int((now - started) // 60) if running and started else None
    failure = argo._sync_failure(app)
    if sync == "Unknown" and health == "Unknown":
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


def applications_tile(cards: list[dict], reading: Reading) -> dict:
    total = len(cards)
    settled = sum(1 for c in cards if c["colour"] == GREEN)
    counts = Counter()
    for card in cards:
        if card["colour"] != GREEN:
            if card["sync"] != "Synced":
                counts[card["sync"]] += 1
            if card["health"] != "Healthy":
                counts[card["health"]] += 1
            if card["sync"] == "Synced" and card["health"] == "Healthy":
                counts["last sync failed" if card["why"] else "slow operation"] += 1
    if not reading.ok():
        colour = GREY
    elif settled < total:
        colour = AMBER
    else:
        colour = GREEN if total else GREY
    return {
        "id": "applications",
        "title": "Applications",
        "colour": colour,
        "headline": f"{settled} of {total} settled" if total else "none read",
        "detail": ", ".join(f"{n} {what}" for what, n in sorted(counts.items())) or
                  ("Synced and Healthy" if total else ""),
    }


def verdict(tiles: list[dict], reasons: list[dict]) -> dict:
    colour = min((t["colour"] for t in tiles), key=RANK.get, default=GREY)
    for reason in reasons:
        if RANK[reason["colour"]] < RANK[colour]:
            colour = reason["colour"]
    attention = [r for r in reasons if r["colour"] in (RED, AMBER)]
    if colour == RED:
        headline = f"{len(attention)} {'thing needs' if len(attention) == 1 else 'things need'} attention now"
    elif colour == AMBER:
        headline = f"{len(attention)} {'thing has' if len(attention) == 1 else 'things have'} not settled"
    elif colour == GREY:
        headline = "Part of the cluster could not be read"
    else:
        headline = "Nothing needs attention"
    return {"colour": colour, "headline": headline,
            "reasons": sorted(reasons, key=lambda r: RANK[r["colour"]]),
            "covers": COVERED, "not_yet": NOT_YET}


def build(applications: Reading, ingresses: Reading, now: float, *, base_repo_url: str,
          root_app: str, ring0_board_url: str, refresh_seconds: int, slow_minutes: int) -> dict:
    apps = (applications.data or {}).get("items", []) if isinstance(applications.data, dict) else []
    found = links_mod.from_ingresses((ingresses.data or {}).get("items", [])
                                     if isinstance(ingresses.data, dict) else [])
    argocd = next((f"https://{urlsplit(l['url']).netloc}" for l in found if l.get("id") == "argocd"),
                  None)

    cards = [application(a, now, argocd, slow_minutes) for a in apps]
    cards.sort(key=lambda c: (RANK[c["colour"]], c["wave"], c["name"]))
    revision, mixed = base_revision(apps, base_repo_url)

    reasons = []
    for card in cards:
        if card["colour"] in (AMBER, GREY):
            text = f"{card['name']}: {card['sync']} / {card['health']}"
            if card["running_minutes"] is not None:
                text += f", operation running for {card['running_minutes']} min"
            if card["why"]:
                text += f" — {card['why']}"
            reasons.append({"colour": card["colour"], "text": text, "link": card["link"],
                            "tile": "applications"})
    for name, reading in (("The Kubernetes API (Applications)", applications),
                          ("The Kubernetes API (Ingresses)", ingresses)):
        if reading.fetched_at is not None and not reading.ok():
            reasons.append(_source_reason(name, reading, now))
        elif reading.fetched_at is None:
            reasons.append({"colour": GREY, "text": f"{name} has not been read yet.",
                            "link": None, "tile": None})

    tiles = [applications_tile(cards, applications)]
    return {
        "schema": SCHEMA,
        "generated": iso(now),
        "refresh_seconds": refresh_seconds,
        "verdict": verdict(tiles, reasons),
        "tiles": tiles,
        "applications": cards,
        "base": {"revision": revision, "mixed": mixed},
        "links": links_mod.ordered(found + links_mod.fixed(
            base_repo_url, revision, _root_repo(apps, root_app), ring0_board_url)),
        "sources": {"applications": applications.describe(), "ingresses": ingresses.describe()},
    }
