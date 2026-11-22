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
COVERED = ["Alertmanager's alerts", "Argo CD Applications", "the node", "backups", "updates"]
NOT_YET = ["certificates", "alert delivery"]

# ring0.json is rewritten at least hourly (the follower's heartbeat); older than
# this, what it says about Ring 0 is unknown (design §4.6).
RING_STATE_STALE_SECONDS = 3600
# The Application that only Ring 0 runs: its Board shows Ring 0's own state.
RING0_MARKER = "canary-follower"

# Amber thresholds for what the Board reads itself, at or before the alerts'
# (apps/node-disk-alerts.yaml): a tile warns first, the alert then turns it red.
DISK_FREE_AMBER = 25        # % free on / or the data disk (NodeDiskSpaceEarlyWarning)
PV_FREE_AMBER = 20          # % free on the fullest volume (PersistentVolumeSpaceHigh)
DAYS_LEFT_AMBER = 7         # days until full at the 12 h rate (the alert says 3)
# A backup CronJob's last scheduled run gets this long to succeed.
RUN_GRACE_SECONDS = 2 * 3600

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


# --------------------------------------------------------------------- node

def _red_if_alerts(tile: str, firing: list[dict]) -> list[dict]:
    return [a for a in firing if a["tile"] == tile]


def _age(seconds: float) -> str:
    hours = seconds / 3600
    if hours < 1:
        return f"{max(int(seconds // 60), 0)} min"
    return f"{hours:.0f} h" if hours < 48 else f"{hours / 24:.0f} days"


def node_tile(nodes: Reading, metrics: Reading, firing: list[dict]) -> tuple[dict, list[dict]]:
    """The tile and its amber reasons."""
    items, reasons = [], []
    node_list = (nodes.data or {}).get("items", []) if isinstance(nodes.data, dict) else []
    ready = 0
    versions = set()
    for node in node_list:
        name = node.get("metadata", {}).get("name", "?")
        status = node.get("status", {})
        info = status.get("nodeInfo", {})
        versions.add(info.get("kubeletVersion", "?"))
        conditions = {c.get("type"): c for c in status.get("conditions", [])}
        if conditions.get("Ready", {}).get("status") == "True":
            ready += 1
        else:
            reasons.append({"colour": AMBER, "tile": "node", "link": None,
                            "text": f"Node {name} is not Ready: "
                                    f"{short(conditions.get('Ready', {}).get('message')) or 'no reason given'}"})
        for pressure in ("DiskPressure", "MemoryPressure", "PIDPressure"):
            if conditions.get(pressure, {}).get("status") == "True":
                reasons.append({"colour": AMBER, "tile": "node", "link": None,
                                "text": f"Node {name} reports {pressure}"})
    values = metrics.data if isinstance(metrics.data, dict) else {}
    for series in values.get("free_percent", []):
        mount, free = series["metric"].get("mountpoint", "?"), series["value"]
        days = next((d["value"] for d in values.get("days_left", [])
                     if d["metric"].get("mountpoint") == mount and d["value"] > 0), None)
        text = f"{mount} {free:.0f}% free" + (f", full in {days:.0f} days at this rate" if days and days < 60 else "")
        colour = AMBER if free < DISK_FREE_AMBER or (days is not None and days < DAYS_LEFT_AMBER) else GREEN
        items.append({"colour": colour, "text": text})
        if colour == AMBER:
            reasons.append({"colour": AMBER, "tile": "node", "link": None, "text": f"Disk {text}"})
    for series in values.get("pv_free_percent", []):
        colour = AMBER if series["value"] < PV_FREE_AMBER else GREEN
        items.append({"colour": colour, "text": f"fullest volume {series['value']:.0f}% free"})
        if colour == AMBER:
            reasons.append({"colour": AMBER, "tile": "node", "link": None,
                            "text": f"The fullest volume has {series['value']:.0f}% free"})
    uptime = next((s["value"] for s in values.get("uptime_seconds", [])), None)
    detail = " · ".join(x for x in (", ".join(sorted(versions)) if versions else "",
                                    f"up {_age(uptime)}" if uptime else "") if x)
    mine = _red_if_alerts("node", firing)
    if mine:
        colour = RED
    elif reasons:
        colour = AMBER
    elif not nodes.ok() or not metrics.ok():
        colour = GREY
    else:
        colour = GREEN if node_list else GREY
    if not node_list:
        headline = "not read"
    elif len(node_list) == 1:
        headline = "Ready" if ready else "not Ready"
    else:
        headline = f"{ready} of {len(node_list)} Ready"
    if mine:
        detail = f"{len(mine)} alert{'s' if len(mine) != 1 else ''} firing" + (f" · {detail}" if detail else "")
    return ({"id": "node", "title": "Node", "colour": colour, "headline": headline, "detail": detail,
             "items": items}, reasons)


# ------------------------------------------------------------------ backups

def _daily(schedule: str) -> bool:
    """A cron schedule that runs once a day: fixed minute and hour, every day."""
    fields = (schedule or "").split()
    if len(fields) == 6:  # CNPG's six-field form starts with seconds
        fields = fields[1:]
    return (len(fields) == 5 and fields[0].isdigit() and fields[1].isdigit()
            and fields[2:] == ["*", "*", "*"])


def database_backups(clusters: list[dict], stores: list[dict], now: float, stale_hours: int) -> list[dict]:
    """One line per CNPG cluster. With the barman-cloud plugin the time lives in
    the ObjectStore's serverRecoveryWindow, under the server the Cluster writes
    to (its serverName, else its own name); a server listed there that no
    Cluster writes to any more (immich-database before PostgreSQL 18) is
    ignored. Without the plugin, CNPG's own status."""
    windows = {(s["metadata"].get("namespace"), s["metadata"].get("name")):
               (s.get("status") or {}).get("serverRecoveryWindow") or {} for s in stores}
    items = []
    for cluster in clusters:
        meta = cluster.get("metadata", {})
        namespace, name = meta.get("namespace"), meta.get("name")
        status = cluster.get("status", {})
        plugin = next((p for p in cluster.get("spec", {}).get("plugins", [])
                       if p.get("name") == "barman-cloud.cloudnative-pg.io" and p.get("enabled", True)), None)
        if plugin:
            parameters = plugin.get("parameters", {})
            window = windows.get((namespace, parameters.get("barmanObjectName")), {}).get(
                parameters.get("serverName") or name, {})
            last = _seconds(window.get("lastSuccessfulBackupTime"))
        else:
            last = _seconds(status.get("lastSuccessfulBackup"))
        conditions = {c.get("type"): c for c in status.get("conditions", [])}
        problem = next((f"{t}: {short(conditions[t].get('message')) or conditions[t].get('reason')}"
                        for t in ("LastBackupSucceeded", "ContinuousArchiving")
                        if conditions.get(t, {}).get("status") == "False"), None)
        label = f"database {namespace}/{name}"
        if last is None:
            items.append({"colour": AMBER, "text": f"{label}: no successful backup recorded"})
        else:
            age = now - last
            colour = AMBER if age > stale_hours * 3600 or problem else GREEN
            items.append({"colour": colour, "text": f"{label}: {_age(age)} ago" + (f" — {problem}" if problem else "")})
    return items


def velero_backups(schedules: list[dict], backups: list[dict], now: float, stale_hours: int) -> list[dict]:
    items = []
    for schedule in schedules:
        name = schedule.get("metadata", {}).get("name")
        mine = sorted((b for b in backups
                       if (b.get("metadata", {}).get("labels") or {}).get("velero.io/schedule-name") == name),
                      key=lambda b: b.get("metadata", {}).get("creationTimestamp", ""))
        done = [b for b in mine if (b.get("status") or {}).get("phase") == "Completed"]
        last = _seconds((done[-1].get("status") or {}).get("completionTimestamp")) if done else None
        newest = (mine[-1].get("status") or {}).get("phase") if mine else None
        label = f"cluster state ({name})"
        if (schedule.get("spec") or {}).get("paused"):
            items.append({"colour": GREEN, "text": f"{label}: paused", "neutral": True})
        elif last is None:
            items.append({"colour": AMBER, "text": f"{label}: no completed backup"})
        else:
            failed = newest in ("Failed", "PartiallyFailed", "FailedValidation")
            colour = AMBER if now - last > stale_hours * 3600 or failed else GREEN
            items.append({"colour": colour, "text": f"{label}: {_age(now - last)} ago"
                          + (f" — the newest one is {newest}" if failed else "")})
    return items


def cronjob_backups(cronjobs: list[dict], now: float, stale_hours: int) -> list[dict]:
    """File copies and the off-site replicator: CronJobs named for backups.
    A suspended one is shown as a fact, not a failure: suspending is a choice
    made in git (Ring 0 runs its replicator only from its storage checks)."""
    items = []
    for job in cronjobs:
        meta = job.get("metadata", {})
        name = meta.get("name", "")
        if "backup" not in name and "replicator" not in name:
            continue
        spec, status = job.get("spec", {}), job.get("status") or {}
        label = f"{meta.get('namespace')}/{name}"
        last_ok = _seconds(status.get("lastSuccessfulTime"))
        if spec.get("suspend"):
            when = f", last run {_age(now - last_ok)} ago" if last_ok else ""
            items.append({"colour": GREEN, "text": f"{label}: suspended{when}", "neutral": True})
            continue
        scheduled = _seconds(status.get("lastScheduleTime"))
        missed = (scheduled is not None and now - scheduled > RUN_GRACE_SECONDS
                  and (last_ok is None or last_ok < scheduled))
        stale = _daily(spec.get("schedule", "")) and last_ok is not None and now - last_ok > stale_hours * 3600
        if last_ok is None and scheduled is None:
            items.append({"colour": GREY, "text": f"{label}: has not run yet", "neutral": True})
        elif missed or stale:
            items.append({"colour": AMBER, "text": f"{label}: the last run did not succeed"
                          + (f" (last success {_age(now - last_ok)} ago)" if last_ok else "")})
        else:
            items.append({"colour": GREEN, "text": f"{label}: {_age(now - last_ok)} ago"})
    return items


def backups_tile(reading: Reading, now: float, stale_hours: int, firing: list[dict],
                 doc_link: str | None) -> tuple[dict, list[dict]]:
    data = reading.data if isinstance(reading.data, dict) else {}
    items = (database_backups(data.get("clusters", []), data.get("objectstores", []), now, stale_hours)
             + velero_backups(data.get("schedules", []), data.get("velero_backups", []), now, stale_hours)
             + cronjob_backups(data.get("cronjobs", []), now, stale_hours))
    reasons = [{"colour": AMBER, "tile": "backups", "link": doc_link, "text": f"Backup: {i['text']}"}
               for i in items if i["colour"] == AMBER]
    mine = _red_if_alerts("backups", firing)
    counted = [i for i in items if not i.get("neutral")]
    if mine:
        colour = RED
    elif reasons:
        colour = AMBER
    elif not reading.ok() or not counted:
        colour = GREY
    else:
        colour = GREEN
    stale = sum(1 for i in items if i["colour"] == AMBER)
    if not reading.ok() and not items:
        headline = "not read"
    elif stale:
        headline = f"{stale} not recent"
    else:
        headline = f"{len(counted)} recent" if counted else "none found"
    databases = sum(1 for i in items if i["text"].startswith("database "))
    detail = f"{databases} database{'s' if databases != 1 else ''}, cluster state, file copies"
    if mine:
        detail = f"{len(mine)} alert{'s' if len(mine) != 1 else ''} firing · {detail}"
    return ({"id": "backups", "title": "Backups", "colour": colour, "headline": headline,
             "detail": detail, "items": items}, reasons)


# ------------------------------------------------------------------ updates

def newest_verified(ring0: dict | None) -> dict | None:
    """The newest commit Ring 0 verified, from its Ring State: the end of the
    Update Desk's path (docs/adr/0067)."""
    if not isinstance(ring0, dict):
        return None
    for commit in ring0.get("commits", []):
        if (commit.get("verdict") or {}).get("state") == "success":
            return commit
    head = ring0.get("head") or {}
    return {"commit": head["commit"], "title": None, "date": head.get("date")} \
        if head.get("state") == "success" and head.get("commit") else None


def _steps(n: int) -> str:
    # Commits, not steps: the Update Desk groups commits into steps of its own.
    return f"{n} verified commit{'s' if n != 1 else ''}"


def _ring0_line(ring0: dict) -> dict:
    now = ring0.get("now") or {}
    state = now.get("state")
    if state == "stuck":
        stuck = now.get("stuck") or {}
        text = f"Ring 0 is stuck{': ' + short(stuck.get('summary')) if stuck.get('summary') else ''}"
    elif state == "updating":
        what = (now.get("pull") or {}).get("title") or (now.get("commit") or "")[:7]
        text = f"Ring 0 is checking {what}" + (f" ({now['step']})" if now.get("step") else "")
    else:
        text = "Ring 0 is up to date"
    return {"colour": GREEN, "text": text, "neutral": True}


def updates_tile(reading: Reading, running: str | None, mixed: list[str], ring0_here: bool, now: float,
                 firing: list[dict], board_url: str) -> tuple[dict, list[dict]]:
    data = reading.data if isinstance(reading.data, dict) else {}
    ring0 = data.get("ring0")
    reasons, items = [], []
    mine = _red_if_alerts("updates", firing)

    def tile(colour, headline, detail=""):
        if mine:
            colour = RED
            detail = f"{len(mine)} alert{'s' if len(mine) != 1 else ''} firing" + (f" · {detail}" if detail else "")
        return {"id": "updates", "title": "Updates", "colour": colour, "headline": headline,
                "detail": detail, "items": items}

    if data.get("disabled"):
        return tile(GREY, "unknown", "Ring State is turned off (RING_STATE_URL)"), reasons
    if not isinstance(ring0, dict):
        return tile(GREY, "not read"), reasons
    stale = now - (_seconds(ring0.get("updated")) or 0) > RING_STATE_STALE_SECONDS
    if stale:
        reasons.append({"colour": GREY, "tile": "updates", "link": board_url,
                        "text": f"Ring 0 has not reported since {ring0.get('updated')}: "
                                "where this cluster stands is unknown."})

    if ring0_here:
        line = _ring0_line(ring0)
        items.append(line)
        head = ring0.get("head") or {}
        if (ring0.get("now") or {}).get("state") == "stuck":
            reasons.append({"colour": AMBER, "tile": "updates", "link": board_url, "text": line["text"]})
            colour = AMBER
        else:
            colour = GREY if stale or not reading.ok() else GREEN
        verdict = head.get("state")
        items.append({"colour": GREEN if verdict == "success" else AMBER if verdict == "failure" else GREY,
                      "text": f"main's head {str(head.get('commit', ''))[:7]}: {verdict or 'no verdict'}",
                      "neutral": verdict not in ("success", "failure")})
        return tile(colour, "this is Ring 0", "follows main and verifies every commit"), reasons

    newest = newest_verified(ring0)
    commits = ring0.get("commits", [])
    position = {c.get("commit"): i for i, c in enumerate(commits)}
    if running:
        title = next((c.get("title") for c in commits if c.get("commit") == running), None)
        items.append({"colour": GREEN, "text": f"runs {running[:7]}" + (f" — {short(title)}" if title else ""),
                      "neutral": True})
    if newest:
        age = _seconds(newest.get("date"))
        items.append({"colour": GREEN, "neutral": True,
                      "text": f"newest verified {newest['commit'][:7]}"
                              + (f" — {short(newest.get('title'))}" if newest.get("title") else "")
                              + (f", {_age(now - age)} ago" if age else "")})
    items.append(_ring0_line(ring0))
    if mixed:
        reasons.append({"colour": AMBER, "tile": "updates", "link": None,
                        "text": "Applications run different base commits: " + ", ".join(mixed[:6])})

    if not running:
        return tile(GREY, "unknown", "no Application runs the base repository"), reasons
    if not newest:
        return tile(GREY, "unknown", "Ring 0 has verified nothing it lists"), reasons
    compare = data.get("compare") or {}
    if running == newest["commit"]:
        colour, headline = GREEN, "up to date with Ring 0"
    elif running in position:
        newer = sum(1 for c in commits[:position[running]]
                    if (c.get("verdict") or {}).get("state") == "success")
        verdict = (commits[position[running]].get("verdict") or {}).get("state")
        if newer:
            colour, headline = AMBER, f"{_steps(newer)} behind"
        elif verdict == "failure":
            colour, headline = AMBER, "runs a commit Ring 0 rejected"
        else:
            colour, headline = AMBER, "runs a commit Ring 0 has not verified"
    elif compare.get("status") == "identical":
        colour, headline = GREEN, "up to date with Ring 0"
    elif compare.get("status") == "ahead":
        colour, headline = AMBER, f"{compare.get('ahead_by')} commits behind"
    elif compare.get("status") == "behind":
        colour, headline = AMBER, "runs a commit Ring 0 has not verified"
    elif compare.get("status") == "diverged":
        colour, headline = AMBER, "runs a commit that is not on main"
    else:
        colour, headline = AMBER, "more than 25 commits behind"
    if colour == AMBER:
        reasons.append({"colour": AMBER, "tile": "updates", "link": board_url,
                        "text": f"Updates: {headline} — the Update Desk takes this cluster along Ring 0's path"})
    if stale or not reading.ok():
        colour = GREY
    return tile(colour, headline, "against Ring 0's verified commits"), reasons


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
            headline += f", and {unsettled} more {'needs' if unsettled == 1 else 'need'} a look"
    elif colour == AMBER:
        headline = f"{unsettled} {'thing needs' if unsettled == 1 else 'things need'} a look"
    elif colour == GREY:
        headline = "Part of the cluster could not be read"
    else:
        headline = "Nothing needs attention"
    return {"colour": colour, "headline": headline,
            "reasons": sorted(reasons, key=lambda r: RANK[r["colour"]]),
            "suppressed": [{"text": _alert_text(a), "link": a["link"]} for a in suppressed],
            "covers": COVERED, "not_yet": NOT_YET}


SOURCE_NAMES = {"alerts": "Alertmanager", "applications": "The Kubernetes API (Applications)",
                "ingresses": "The Kubernetes API (Ingresses)", "nodes": "The Kubernetes API (nodes)",
                "prometheus": "Prometheus", "backups": "The Kubernetes API (backups)",
                "rings": "Ring 0's Ring State"}


def build(readings: dict, now: float, *, base_repo_url: str, root_app: str, ring0_board_url: str,
          refresh_seconds: int, slow_minutes: int, stale_hours: int = 26) -> dict:
    readings = {key: readings.get(key) or Reading() for key in SOURCE_NAMES}
    applications, ingresses, alerts = readings["applications"], readings["ingresses"], readings["alerts"]
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
    web = links_mod.github_web(base_repo_url)
    node, node_reasons = node_tile(readings["nodes"], readings["prometheus"], firing)
    backups, backup_reasons = backups_tile(
        readings["backups"], now, stale_hours, firing,
        f"{web}/blob/{revision or 'main'}/doc/storage-and-backup.md" if web else None)
    for reason in node_reasons:
        reason["link"] = reason["link"] or tool("grafana")
    updates, update_reasons = updates_tile(
        readings["rings"], revision, mixed,
        any(a.get("metadata", {}).get("name") == RING0_MARKER for a in apps), now, firing, ring0_board_url)
    reasons += node_reasons + backup_reasons + update_reasons
    for key, reading in readings.items():
        name = SOURCE_NAMES[key]
        if reading.fetched_at is not None and not reading.ok():
            reasons.append(_source_reason(name, reading, now))
        elif reading.fetched_at is None:
            reasons.append({"colour": GREY, "text": f"{name} has not been read yet.",
                            "link": None, "tile": None})

    tiles = [alerts_tile(firing, suppressed, watchdog, alerts),
             applications_tile(cards, applications, firing), node, backups, updates]
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
        "sources": {key: reading.describe() for key, reading in readings.items()},
    }
