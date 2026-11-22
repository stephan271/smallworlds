"""The way from what a community runs to what Ring 0 runs, in Ring 0's own steps.

Ring 0 takes one update at a time and verifies each one (docs/adr/0060). A
community can follow it the same way: every **step** ends on a commit of main
that Ring 0 verified, so every state the community passes through ran exactly
like that on Ring 0. A step holds the commits since the previous step; commits
Ring 0 did not verify on their own (a rejected update and its revert, a commit
superseded before it was checked) are carried inside the next verified one.

Only steps that change something this community deploys are listed: a commit
that touches only Stalwart is no step for a community without mail. What it
changes is the net difference between the two verified commits, so an update
and its revert in between cancel out.

Steps can be taken together. `batches` proposes how: runs of simple steps
(nothing migrates, nothing to do by hand) together, and each step that
migrates a database or needs the Operator on its own.
"""
from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime, timezone

import community as community_module
import miniyaml
import units as units_module
import versions
from basegit import BaseRepo

K8S = "infrastructure/kubernetes/"
CACHE_IMAGES = ("redis", "valkey", "memcached")
# Images a migrating tenant runs that are not its database or application: the
# PostgreSQL client of Immich's pod export, for example. CNPG's own images are
# ghcr.io/cloudnative-pg/postgresql or a catalog image, never these.
CLIENT_IMAGES = ("postgres",)
PULL = re.compile(r"\(#(\d+)\)$|^Merge pull request #(\d+)")


def _parse_time(text: str | None) -> datetime | None:
    if not text:
        return None
    try:
        return datetime.fromisoformat(text.replace("Z", "+00:00"))
    except ValueError:
        return None


def _hours(start: datetime | None, end: datetime | None) -> float | None:
    if not start or not end:
        return None
    return round((end - start).total_seconds() / 3600, 1)


def _version_key(text: str) -> tuple:
    return tuple(int(n) for n in re.findall(r"\d+", text)[:4])


def pull_of(subject: str) -> int | None:
    m = PULL.search(subject)
    return int(m.group(1) or m.group(2)) if m else None


@dataclass
class Context:
    base: BaseRepo
    catalog: units_module.Catalog
    community: community_module.Community
    overlay_patched: set[str] = field(default_factory=set)
    operator_steps: list[dict] = field(default_factory=list)
    stops: list[dict] = field(default_factory=list)
    releases: dict[str, str] = field(default_factory=dict)
    now: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    _versions: dict = field(default_factory=dict)

    @classmethod
    def load_metadata(cls, base: BaseRepo, sha: str) -> tuple[list[dict], list[dict]]:
        """Operator Steps and Upgrade Stops as main has them now: an entry added
        later still belongs to the older commit it names. Falls back to this
        checkout's copy while main does not have the file yet."""
        def read(name):
            text = base.show(sha, K8S + name)
            if text is None:
                try:
                    with open(f"{base.path}/{K8S}{name}") as handle:
                        text = handle.read()
                except OSError:
                    text = ""
            return miniyaml.loads(text) or {}
        return read("operator-steps.yaml").get("steps") or [], read("upgrade-stops.yaml").get("stops") or []

    # -- versions -------------------------------------------------------------
    def prefixes(self, unit: str, tenants: list[str] | None = None) -> list[str]:
        catalog, community = self.catalog, self.community
        chosen = tenants if tenants is not None else [
            t for t in community.tenants if catalog.unit_of_tenant(t) == unit]
        result = [f"{K8S}tenants/{t}/" for t in sorted(chosen)]
        if tenants is None:
            result += [f"{K8S}apps/{a}.yaml" for a in sorted(community.applications)
                       if catalog.unit_of_application(a) == unit]
            if unit == units_module.PLATFORM:
                result.append(K8S + "bases/")
        return result

    def versions_at(self, sha: str, prefixes: list[str]) -> dict[str, set[str]]:
        key = (sha, tuple(prefixes))
        if key not in self._versions:
            found: dict[str, set[str]] = {}
            files = [(blob, path) for blob, path in self.base.files(sha, prefixes)
                     if "/tests/" not in path]
            texts = self.base.blobs([blob for blob, _ in files])
            for blob, path in files:
                versions.merge(found, versions.versions_in(path, texts[blob]))
            self._versions[key] = versions.settle(found)
        return self._versions[key]

    # -- rules ----------------------------------------------------------------
    def applies(self, entry: dict) -> bool:
        condition = entry.get("applies") or {}
        if (domain := condition.get("domain_not")) and self.community.domain == domain:
            return False
        if (wanted := condition.get("runs")) and not set(wanted) & self.community.units:
            return False
        if (application := condition.get("overlay_patches")) and application not in self.overlay_patched:
            return False
        return True


_DATABASE_IMAGE = re.compile(r"postgresql|vectorchord|pgvecto|postgis")


def _same_postgres_major(change: dict) -> bool:
    """A PostgreSQL image that moves within its major version (16.13 → 16.15)
    rewrites nothing: minor releases share the data format."""
    if change["kind"] != "image" or not _DATABASE_IMAGE.search(change["name"]):
        return False
    majors = lambda text: {v.strip().split(".")[0].split("-")[0] for v in text.split(",") if v.strip()}
    return bool(change["from"]) and majors(change["from"]) == majors(change["to"])


def _migrations(context: Context, previous: str, sha: str, unit: str) -> list[dict]:
    """Tenants of this unit whose database the step may rewrite: an application
    or PostgreSQL image (or chart) changed in a tenant that migrates."""
    catalog = context.catalog
    found = []
    for tenant in catalog.units[unit].migrates:
        if tenant not in context.community.tenants:
            continue
        prefixes = context.prefixes(unit, [tenant])
        moved = [c for c in versions.changes(context.versions_at(previous, prefixes),
                                             context.versions_at(sha, prefixes))
                 if not c["helper"] and versions.short_name(c["name"]).split("/")[-1] not in CACHE_IMAGES
                 and versions.short_name(c["name"]) not in CLIENT_IMAGES
                 and not _same_postgres_major(c)]
        if not moved:
            continue
        protected = any("pre-update-backup" in (context.base.show(previous, f"{K8S}tenants/{t}/kustomization.yaml") or "")
                        for t in context.community.tenants if catalog.unit_of_tenant(t) == unit)
        found.append({"unit": unit, "tenant": tenant, "because": moved[:4], "protected": protected})
    return found


def _stops(context: Context, previous: str, sha: str) -> list[dict]:
    found = []
    for stop in context.stops:
        unit = context.catalog.unit_of_tenant(stop.get("unit", "")) or stop.get("unit")
        if unit not in context.community.units:
            continue
        prefixes = context.prefixes(unit)
        image = "image:" + stop.get("image", "")
        before = context.versions_at(previous, prefixes).get(image, set())
        after = context.versions_at(sha, prefixes).get(image, set())
        if not before or not after:
            continue
        stop_at, limit = _version_key(stop["version"]), _version_key(stop["before"])
        if min(_version_key(v) for v in before) < stop_at and max(_version_key(v) for v in after) >= limit:
            found.append({"unit": unit, "image": stop["image"], "version": stop["version"],
                          "before": stop["before"], "why": stop.get("why", "")})
    return found


def compute(context: Context, start: str, verdicts) -> dict:
    """Every step from `start` (the main commit the community runs) to Ring 0's
    newest verified commit, plus what lies beyond it."""
    base, catalog, community = context.base, context.catalog, context.community
    commits = base.first_parent_after(start)
    paths = base.paths_per_commit(start)
    start_commit = base.commit(start)
    verdicts.lookup([c.sha for c in commits], since=start_commit.date)
    step_entries = {entry["commit"]: entry for entry in context.operator_steps}

    def describe(commit, index):
        verdict = verdicts.of(commit.sha)
        touched = sorted({catalog.unit_of_path(p) for p in paths.get(commit.sha, [])
                          if community.relevant(p, catalog)} - {None})
        return {"sha": commit.sha, "subject": commit.subject, "pull": pull_of(commit.subject),
                "date": commit.date, "author": commit.author, "verdict": verdict.get("state"),
                "verified_at": verdict.get("at"), "units": touched,
                "node": any(units_module.touches_node(p) for p in paths.get(commit.sha, [])),
                "release": context.releases.get(commit.sha), "position": index}

    steps, pending, previous = [], [], start
    for index, commit in enumerate(commits):
        pending.append(describe(commit, index))
        if verdicts.of(commit.sha).get("state") != "success":
            continue
        relevant = [p for p in base.changed_paths(previous, commit.sha) if community.relevant(p, catalog)]
        if not relevant:
            continue
        following = commits[index + 1] if index + 1 < len(commits) else None
        steps.append(_step(context, previous, commit, following, pending, relevant, step_entries))
        previous, pending = commit.sha, []
    for number, step in enumerate(steps, 1):
        step["number"] = number

    target = previous
    return {
        "start": start,
        "target": target,
        "steps": steps,
        "beyond": pending,  # newer commits not (yet) part of a verified step
        "batches": batches(steps),
        "overview": overview(context, start, target, steps),
    }


def _step(context, previous, commit, following, pending, relevant, step_entries) -> dict:
    catalog = context.catalog
    touched_units = sorted({catalog.unit_of_path(p) for p in relevant} - {None},
                           key=lambda u: (u == units_module.PLATFORM, u))
    unit_changes = {}
    for unit in touched_units:
        prefixes = context.prefixes(unit)
        unit_changes[unit] = {
            "title": catalog.units[unit].title,
            "changes": versions.changes(context.versions_at(previous, prefixes),
                                        context.versions_at(commit.sha, prefixes)),
            "files": sum(1 for p in relevant if catalog.unit_of_path(p) == unit),
            "commits": [c["subject"] for c in pending if unit in c["units"]],
        }
    migrations = [m for unit in touched_units for m in _migrations(context, previous, commit.sha, unit)]
    manual = []
    for entry in pending:
        if (rule := step_entries.get(entry["sha"])) and context.applies(rule):
            manual.append({"when": rule.get("when", "info"), "title": rule.get("title", ""),
                           "detail": rule.get("detail", ""), "commit": entry["sha"]})
    verified_at = _parse_time(pending[-1]["verified_at"])
    left_at = _parse_time(following.date) if following else None
    soak = _hours(verified_at, left_at or context.now)
    # A person's push can land while Ring 0 still checks the previous commit;
    # that commit then ran for no time at all after its verdict.
    soak = max(soak, 0.0) if soak is not None else None
    titles = [c["subject"] for c in pending if c["units"]] or [commit.subject]
    return {
        "sha": commit.sha,
        "previous": previous,
        "subject": titles[0],
        "titles": titles,
        "verified_at": pending[-1]["verified_at"],
        "ring0_hours": soak,
        "ring0_current": following is None,
        "commits": pending,
        "units": unit_changes,
        "migrations": migrations,
        "undo": "restore" if migrations else "revert",
        "manual": manual,
        "stops": _stops(context, previous, commit.sha),
        "releases": [c["release"] for c in pending if c["release"]],
        "node": any(c["node"] for c in pending),
        "simple": not migrations and not any(m["when"] in ("before", "after") for m in manual),
    }


def batches(steps: list[dict]) -> dict[str, list[dict]]:
    """Two ways to group the steps; the Operator may also choose any end point.

    `careful`: runs of simple steps together, every step that migrates a
    database or needs the Operator on its own. One migration per sync, as Ring 0
    took them.
    `fewer`: as few groups as the rules allow. A migration the pre-update backup
    does not cover yet, and an Upgrade Stop, stay alone; a step with something to
    do before it starts a group, one with something to do after it ends one."""
    careful, run = [], []
    for step in steps:
        if step["simple"]:
            run.append(step["number"])
            continue
        if run:
            careful.append({"first": run[0], "last": run[-1], "kind": "simple"})
            run = []
        careful.append({"first": step["number"], "last": step["number"], "kind": "careful"})
    if run:
        careful.append({"first": run[0], "last": run[-1], "kind": "simple"})

    fewer, group = [], []

    def close():
        if group:
            kind = "simple" if all(s["simple"] for s in group) else "careful"
            fewer.append({"first": group[0]["number"], "last": group[-1]["number"], "kind": kind})
            group.clear()

    for step in steps:
        alone = step["stops"] or any(not m["protected"] for m in step["migrations"])
        before = any(m["when"] == "before" for m in step["manual"])
        after = any(m["when"] == "after" for m in step["manual"])
        if alone or before:
            close()
        group.append(step)
        if alone or after:
            close()
    close()
    return {"careful": careful, "fewer": fewer}


def overview(context: Context, start: str, target: str, steps: list[dict]) -> list[dict]:
    """One row per unit the community runs: its versions now and at Ring 0's
    newest verified commit, and the steps that touch it."""
    rows = []
    order = sorted(context.community.units, key=lambda u: (u == units_module.PLATFORM, u))
    for unit in order:
        prefixes = context.prefixes(unit)
        moved = versions.changes(context.versions_at(start, prefixes), context.versions_at(target, prefixes))
        touching = [s["number"] for s in steps if unit in s["units"]]
        rows.append({
            "unit": unit,
            "title": context.catalog.units[unit].title,
            "changes": moved,
            "steps": touching,
            "migrates": [s["number"] for s in steps if any(m["unit"] == unit for m in s["migrations"])],
            "current": not touching,
        })
    return rows
