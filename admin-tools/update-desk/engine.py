"""The Update Desk's logic, apart from its web server.

Where an update stands is never stored: it is read every time from the overlay
checkout (working tree, HEAD, origin/main) and from the cluster (the root
Application's pin, each Application's revision, sync and health). So the Desk
can be stopped and started again in the middle of an update, and a step done by
hand in a terminal shows up as done.

    idle       nothing in flight; the path lists the steps still to take
    written    the overlay's files name a new base commit, not committed yet
    committed  committed, not pushed
    pushed     pushed; the root Application still points at an older overlay commit
    rolling    Argo CD is bringing the cluster to the new commit
    done       every Application is Synced and Healthy at the new commit
"""
from __future__ import annotations

import os
import re
import threading
import time
from datetime import datetime, timezone

import cluster as cluster_module
import community as community_module
import path as path_module
import pins
import units as units_module
import versions
from basegit import BaseRepo, GitError
from overlaygit import OverlayError, OverlayRepo, TRAILER_FROM, TRAILER_PIN
from verdicts import Verdicts

HERE = os.path.dirname(os.path.abspath(__file__))


def _repo_key(url: str | None) -> str:
    """'owner/name' of a GitHub URL in any of its spellings."""
    if not url:
        return ""
    m = re.search(r"github\.com[/:]([^/]+/[^/]+?)(?:\.git)?/?$", url.strip())
    return m.group(1).lower() if m else url.strip().lower()


class DeskError(RuntimeError):
    pass


class Desk:
    def __init__(self, base_path: str, overlay_path: str, kubeconfig: str | None,
                 cluster=None, verdicts=None, base=None, overlay=None):
        self.base = base or BaseRepo(base_path)
        self.overlay = overlay or OverlayRepo(overlay_path)
        self.cluster = cluster or cluster_module.Cluster(kubeconfig)
        self.verdicts = verdicts or Verdicts()
        self.lock = threading.RLock()
        self.path: dict | None = None
        self.path_key: tuple | None = None
        self.path_error: str | None = None
        self.context: path_module.Context | None = None
        self.log: list[dict] = []
        self.fetch_error: str | None = None
        self.planned: dict | None = None

    # -- bookkeeping -----------------------------------------------------------
    def _record(self, action: str, ok: bool, command: str = "", output: str = "") -> dict:
        entry = {"at": datetime.now(timezone.utc).strftime("%H:%M:%S"), "action": action,
                 "ok": ok, "command": command, "output": output[-2000:]}
        self.log.append(entry)
        del self.log[:-50]
        return entry

    def catalog(self) -> units_module.Catalog:
        text = self.base.show(self.base.head(), units_module.UNITS_FILE)
        if text is None:
            with open(os.path.join(HERE, "..", "..", units_module.UNITS_FILE)) as handle:
                text = handle.read()
        return units_module.Catalog.parse(text)

    def start_of(self, ref: str) -> str:
        """The main commit an overlay ref stands for."""
        if pins.SHA.match(ref):
            if not self.base.is_ancestor(ref, self.base.main):
                raise DeskError(f"the overlay names {ref[:12]}, which is not on main")
            return ref
        if pins.TAG.match(ref):
            tagged = self.base.resolve(ref)
            if not tagged:
                raise DeskError(f"the release {ref} is not known here: git fetch --tags origin")
            if self.base.is_ancestor(tagged, self.base.main):
                return tagged
            parent = self.base.resolve(tagged + "^")
            if parent and self.base.is_ancestor(parent, self.base.main):
                return parent
            raise DeskError(f"{ref} was not cut from main")
        raise DeskError(f"the overlay follows {ref!r}, not a fixed commit. Pin it to a release "
                        "or a commit first: a moving ref cannot be updated step by step.")

    def expected_base(self, ref: str) -> str | None:
        """The commit Argo CD reports for a base Application pinned to `ref`."""
        return ref if pins.SHA.match(ref) else self.base.resolve(ref)

    # -- the path --------------------------------------------------------------
    def refresh(self, fetch: bool = True) -> None:
        with self.lock:
            if fetch:
                self.base.fetch()
                self.fetch_error = self.overlay.fetch()
            self.verdicts.load_ring0()
            self.path_key = None
            self._ensure_path()

    def _ensure_path(self) -> None:
        try:
            overlay = pins.read(self.overlay.path)
            if overlay.ref is None:
                raise DeskError("the overlay names several base refs at once: " +
                                ", ".join(f"{ref} ({', '.join(files)})" for ref, files in overlay.refs.items()))
            head = self.base.head()
            key = (overlay.ref, head, tuple(sorted(overlay.routed.items())))
            if key == self.path_key and self.path is not None:
                return
            start = self.start_of(overlay.ref)
            catalog = self.catalog()
            community = community_module.at(self.base, [start, head], overlay, catalog)
            operator_steps, stops = path_module.Context.load_metadata(self.base, head)
            self.context = path_module.Context(
                base=self.base, catalog=catalog, community=community,
                overlay_patched=overlay.patched, operator_steps=operator_steps, stops=stops,
                releases=self.base.release_points())
            self.path = path_module.compute(self.context, start, self.verdicts)
            self.path["ref"] = overlay.ref
            self.path["overlay"] = overlay
            self.path_key = key
            self.path_error = None
        except (DeskError, GitError, pins.PinError, OverlayError, ValueError) as error:
            self.path, self.path_key, self.path_error = None, None, str(error)

    # -- where an update stands ------------------------------------------------
    def progress(self) -> dict:
        status = self.overlay.status()
        working = pins.read(self.overlay.path)
        at_head = self.overlay.pin_at("HEAD")
        at_origin = self.overlay.pin_at("origin/main") if status["origin"] else None
        applications = self.cluster.applications()
        result = {"overlay": status, "working_ref": working.ref,
                  "head_ref": at_head.ref if at_head else None,
                  "origin_ref": at_origin.ref if at_origin else None,
                  "cluster_error": self.cluster.last_error if applications is None else None,
                  "applications": applications or [], "not_ready": [], "root": None,
                  "running_ref": None}
        pin_files = set().union(*working.refs.values()) if working.refs else set()
        dirty_pin_files = [f for f in status["dirty"] if f in pin_files]
        other_dirty = [f for f in status["dirty"] if f not in pin_files]
        result["dirty_other"] = other_dirty

        # What the cluster runs: the base ref in the overlay commit the root
        # Application points at (or origin/main's, for a root that follows it).
        root = next((a for a in applications or [] if a["name"] == cluster_module.ROOT_APP), None)
        result["root"] = root
        root_pinned = bool(root and root["targetRevision"] and pins.SHA.match(root["targetRevision"]))
        result["root_pinned"] = root_pinned
        if root_pinned:
            running = self.overlay.pin_at(root["targetRevision"])
            result["running_ref"] = running.ref if running else None
        elif root:
            result["running_ref"] = result["origin_ref"]

        if dirty_pin_files or (at_head and working.ref != at_head.ref):
            result["stage"] = "written"
            result["files"] = dirty_pin_files
            return result
        if status["ahead"] > 0:
            result["stage"] = "committed"
            return result
        if applications is None:
            result["stage"] = "cluster-unknown"
            return result

        origin = status["origin"]
        if root_pinned and root["targetRevision"] != origin:
            result["stage"] = "pushed"
            # What the pin move brings, so "Move the root pin" is never a leap:
            # an overlay commit pushed earlier and never rolled out may change
            # nothing at all, or much more than this Desk's own step.
            result["pending"] = self.overlay.between(root["targetRevision"], origin)
            return result

        ref = result["origin_ref"] or working.ref
        base_sha = self.expected_base(ref) if ref else None
        overlay_key = _repo_key(status["remote"])
        for app in applications:
            reason = None
            repo = _repo_key(app["repoURL"])
            if app["sync"] != "Synced":
                reason = app["sync"] or "unknown sync"
            elif app["health"] != "Healthy":
                reason = app["health"] or "unknown health"
            elif app["operation"]["phase"] in ("Running", "Terminating"):
                reason = "syncing"
            elif repo == pins.BASE_REPO.lower() and base_sha and app["revision"] != base_sha:
                reason = f"at {str(app['revision'])[:7]}, not {base_sha[:7]}"
            elif repo == overlay_key and origin and app["revision"] != origin:
                reason = f"at overlay {str(app['revision'])[:7]}, not {origin[:7]}"
            if app["operation"]["phase"] in ("Failed", "Error") and not reason:
                reason = "last sync failed: " + app["operation"]["message"][:160]
            if reason:
                result["not_ready"].append({"name": app["name"], "reason": reason})
        result["stage"] = "rolling" if result["not_ready"] else "done"
        return result

    # -- plans -----------------------------------------------------------------
    def plan(self, to: str) -> dict:
        with self.lock:
            self._ensure_path()
            if not self.path:
                raise DeskError(self.path_error or "no path")
            steps = self.path["steps"]
            index = next((i for i, s in enumerate(steps) if s["sha"] == to), None)
            if index is None:
                raise DeskError("that commit is not a step on this community's path")
            included = steps[:index + 1]
            return self._plan(self.path["start"], to, included, kind="forward")

    def undo_plan(self) -> dict:
        with self.lock:
            self._ensure_path()
            if not self.context:
                raise DeskError(self.path_error or "no path")
            trailers = self.overlay.trailers("HEAD")
            current, previous = trailers.get(TRAILER_PIN), trailers.get(TRAILER_FROM)
            working = pins.read(self.overlay.path).ref
            if not current or not previous or working != current:
                raise DeskError("the overlay's last commit is not a step this Desk took, so there "
                                "is nothing it can undo by itself")
            plan = self._plan(self.start_of(working), self.start_of(previous), [], kind="undo",
                              to_ref=previous)
            if plan["migrations"]:
                plan["blocked"] = ("This step migrated a database. Reverting the overlay does not "
                                   "undo that: restore the pre-update backup instead "
                                   "(doc/storage-and-backup.md, 'Rolling an update back').")
            return plan

    def _plan(self, start: str, to: str, included: list[dict], kind: str, to_ref: str | None = None) -> dict:
        context = self.context
        units_changed = {}
        for unit in sorted(context.community.units, key=lambda u: (u == units_module.PLATFORM, u)):
            prefixes = context.prefixes(unit)
            changes = versions.changes(context.versions_at(start, prefixes), context.versions_at(to, prefixes))
            in_steps = [s["number"] for s in included if unit in s["units"]]
            if changes or in_steps:
                units_changed[unit] = {"title": context.catalog.units[unit].title,
                                       "changes": changes, "steps": in_steps}
        # Protection is judged from where the community stands now: a hook that
        # arrives within this plan cannot cover a migration in the same sync.
        if kind == "undo":
            migrations = [m for unit in context.community.units
                          for m in path_module._migrations(context, to, start, unit)]
        else:
            migrations = [m for unit in context.community.units
                          for m in path_module._migrations(context, start, to, unit)]
        databases = self._databases_for({m["unit"] for m in migrations})
        manual = [dict(m, step=s["number"]) for s in included for m in s["manual"]]
        commits = [c for s in included for c in s["commits"]]
        plan = {
            "kind": kind, "from": start, "to": to, "to_ref": to_ref or to,
            "steps": [s["number"] for s in included],
            "units": units_changed,
            "migrations": migrations,
            "unprotected": [m for m in migrations if not m["protected"]],
            "databases": databases,
            "manual": manual,
            "stops": path_module._stops(context, start, to) if kind == "forward" else [],
            "undo": "restore" if migrations else "revert",
            "commits": len(commits),
            "titles": [c["subject"] for c in commits if c["units"]],
            "blocked": None,
        }
        plan["message"] = self._message(plan, commits)
        self.planned = plan
        return plan

    def _databases_for(self, units: set[str]) -> list[dict]:
        if not units:
            return []
        applications = self.cluster.applications() or []
        catalog = self.context.catalog
        namespaces = {a["namespace"] for a in applications
                      if catalog.unit_of_application(a["name"]) in units and a["namespace"]}
        found = []
        for database in self.cluster.databases() or []:
            if database["namespace"] in namespaces:
                found.append(database)
        return found

    def _message(self, plan: dict, commits: list[dict]) -> str:
        short = plan["to"][:7]
        if plan["kind"] == "undo":
            head = f"Undo: back to SmallWorlds base {short}"
        elif len(plan["steps"]) == 1:
            head = f"Update to Ring 0's verified {short}: step {plan['steps'][0]}"
        else:
            head = f"Update to Ring 0's verified {short}: steps {plan['steps'][0]}–{plan['steps'][-1]}"
        lines = [head, ""]
        for unit in plan["units"].values():
            moved = [f"{c['name']} {c['from'] or '–'} → {c['to'] or '–'}" for c in unit["changes"] if not c["helper"]]
            if moved:
                lines.append(f"{unit['title']}: " + "; ".join(moved[:6]))
        relevant = [c for c in commits if c["units"]]
        if relevant:
            lines += ["", "Changes on main:"]
            lines += [f"- {c['subject']}" for c in relevant[:40]]
            if len(relevant) > 40:
                lines.append(f"- … and {len(relevant) - 40} more")
        lines += ["", f"{TRAILER_PIN}: {plan['to_ref']}",
                  f"{TRAILER_FROM}: {self.path['ref'] if self.path else plan['from']}",
                  "Updated-by: smallworlds-update (admin-tools/update-desk)"]
        return "\n".join(lines)

    # -- actions ---------------------------------------------------------------
    def write(self, to_ref: str) -> dict:
        with self.lock:
            if not self.planned or self.planned["to_ref"] != to_ref:
                raise DeskError("plan the step first")
            if self.planned.get("blocked"):
                raise DeskError(self.planned["blocked"])
            status = self.overlay.status()
            if status["branch"] != "main":
                raise DeskError(f"the overlay checkout is on {status['branch']!r}; switch to main")
            if status["dirty"]:
                raise DeskError("the overlay has uncommitted changes: " + ", ".join(status["dirty"][:8]))
            if status["behind"]:
                raise DeskError(f"the overlay is {status['behind']} commit(s) behind origin/main: git pull first")
            overlay = pins.read(self.overlay.path)
            texts = {}
            for path in pins.kustomization_files(self.overlay.path):
                with open(path) as handle:
                    texts[os.path.relpath(path, self.overlay.path)] = handle.read()
            to_sha = self.start_of(to_ref)
            applications = community_module.applications_at(self.base, to_sha, overlay)
            base_apps = {}
            for _, file in self.base.files(to_sha, ["infrastructure/kubernetes/apps/"]):
                stem = file.rsplit("/", 1)[1].rsplit(".", 1)[0]
                if stem in applications:
                    base_apps[stem] = self.base.show(to_sha, file) or ""
            tracking = pins.head_tracking(base_apps)
            changed = pins.rewrite(texts, to_ref, tracking, overlay)
            if not changed:
                raise DeskError("the overlay already names that commit")
            pins.write(self.overlay.path, changed)
            files = sorted(changed)
            try:
                rendered = pins.render(self.overlay.path)
                offenders = pins.never_head(rendered, to_ref)
                if offenders:
                    raise DeskError("after the change, these base Applications would not read the "
                                    "chosen commit: " + "; ".join(offenders))
            except (DeskError, pins.PinError, OSError) as error:
                self.overlay.discard(files)
                self._record("write", False, output=str(error))
                raise DeskError(f"{error} (the change was undone)") from error
            self._record("write", True, output=f"{len(files)} file(s) now name {to_ref[:12]}")
            return {"files": files, "diff": self.overlay.diff(files)}

    def discard(self) -> dict:
        with self.lock:
            working = pins.read(self.overlay.path)
            files = sorted(set().union(*working.refs.values())) if working.refs else []
            dirty = [f for f in self.overlay.status()["dirty"] if f in files]
            if dirty:
                self.overlay.discard(dirty)
            self._record("discard", True, f"git checkout -- {' '.join(dirty)}")
            self.path_key = None
            return {"discarded": dirty}

    def commit(self) -> dict:
        with self.lock:
            if not self.planned:
                raise DeskError("nothing planned")
            working = pins.read(self.overlay.path)
            if working.ref != self.planned["to_ref"]:
                raise DeskError("the overlay's files do not name the planned commit any more")
            dirty = [f for f in self.overlay.status()["dirty"]
                     if f in set().union(*working.refs.values())]
            if not dirty:
                raise DeskError("nothing to commit")
            sha = self.overlay.commit(dirty, self.planned["message"])
            self._record("commit", True, f"git commit -- {' '.join(dirty)}", sha)
            return {"commit": sha}

    def push(self) -> dict:
        with self.lock:
            status = self.overlay.status()
            if status["branch"] != "main" or status["ahead"] == 0:
                raise DeskError("nothing to push")
            if status["behind"]:
                raise DeskError("origin/main has commits this checkout does not: pull first")
            try:
                sha = self.overlay.push()
            except OverlayError as error:
                self._record("push", False, "git push origin HEAD:main", str(error))
                raise DeskError(str(error)) from error
            self._record("push", True, "git push origin HEAD:main", sha or "")
            return {"origin": sha}

    def move_pin(self) -> dict:
        with self.lock:
            origin = self.overlay.status()["origin"]
            if not origin:
                raise DeskError("origin/main is unknown")
            result = self.cluster.move_root_pin(origin)
            self._record("move root pin", result["ok"], result["command"], result["output"])
            if not result["ok"]:
                raise DeskError(result["output"] or "kubectl patch failed")
            refreshed = self.cluster.refresh([cluster_module.ROOT_APP])
            self._record("refresh root", refreshed["ok"], refreshed["command"], refreshed["output"])
            return result

    def refresh_applications(self) -> dict:
        with self.lock:
            names = [a["name"] for a in self.cluster.applications() or []]
            result = self.cluster.refresh(names)
            self._record("hard refresh", result["ok"], f"(hard refresh of {len(names)} Applications)",
                         result["output"])
            return result

    def backup(self, namespace: str, name: str) -> dict:
        with self.lock:
            database = next((d for d in self.cluster.databases() or []
                             if d["namespace"] == namespace and d["name"] == name), None)
            if not database:
                raise DeskError(f"no database {namespace}/{name}")
            step = (self.planned or {}).get("to") or "manual"
            result = self.cluster.take_backup(namespace, name, database["plugin"], step)
            self._record(f"backup {namespace}/{name}", result["ok"], result["command"], result["output"])
            if not result["ok"]:
                raise DeskError(result["output"])
            return result

    def last_update(self) -> dict | None:
        """The overlay's last commit, when this Desk wrote it: what it moved, and
        what the Operator still has to do by hand after it."""
        trailers = self.overlay.trailers("HEAD")
        to_ref, from_ref = trailers.get(TRAILER_PIN), trailers.get(TRAILER_FROM)
        if not to_ref or not from_ref or not self.context:
            return None
        try:
            start, end = self.start_of(from_ref), self.start_of(to_ref)
        except DeskError:
            return None
        commits = {c.sha for c in self.base.first_parent_after(start)} if start != end else set()
        commits -= {c.sha for c in self.base.first_parent_after(end)}
        after = [{"title": e.get("title", ""), "detail": e.get("detail", "")}
                 for e in self.context.operator_steps
                 if e.get("commit") in commits and e.get("when") == "after" and self.context.applies(e)]
        date = self.overlay.git("log", "-1", "--format=%cI", "HEAD").strip()
        age = datetime.now(timezone.utc) - datetime.fromisoformat(date)
        return {"subject": self.overlay.message("HEAD"), "from": from_ref, "to": to_ref,
                "after": after, "recent": age.days < 3}

    # -- everything the page shows ---------------------------------------------
    def state(self) -> dict:
        with self.lock:
            self._ensure_path()
            progress = self.progress()
            path = dict(self.path) if self.path else None
            if path:
                overlay = path.pop("overlay")
                path["routed"] = sorted(overlay.routed)
                path["community"] = {"units": sorted(self.context.community.units),
                                     "domain": self.context.community.domain,
                                     "applications": sorted(self.context.community.applications)}
            return {
                "path": path,
                "path_error": self.path_error,
                "progress": progress,
                "ring0": self.verdicts.now(),
                "verdict_error": self.verdicts.lookup_error,
                "fetch_error": self.fetch_error,
                "backups": self.cluster.backups() if progress.get("applications") else None,
                "planned": self.planned,
                "last_update": self.last_update() if progress.get("stage") == "done" else None,
                "path_start_date": self.base.commit(path["start"]).date if path else None,
                "log": list(reversed(self.log)),
                "generated": time.time(),
                "base_head": self.base.head(),
            }
