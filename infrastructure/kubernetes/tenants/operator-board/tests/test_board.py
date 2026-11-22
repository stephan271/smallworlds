"""Tests for the Operator Board (docs/adr/0068). Standard library only; no cluster.

    python3 infrastructure/kubernetes/tenants/operator-board/tests/test_board.py
"""
import ast
import importlib.util
import inspect
import json
import logging
import os
import re
import sys
import threading
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

TENANT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
K8S = os.path.dirname(os.path.dirname(TENANT))
FOLLOWER_SRC = os.path.join(K8S, "tenants", "canary-follower", "src")
sys.path.insert(0, os.path.join(TENANT, "src"))
os.environ.setdefault("STATIC_DIR", os.path.join(TENANT, "static"))

import argo  # noqa: E402
import config  # noqa: E402
import links  # noqa: E402
import main  # noqa: E402
import state  # noqa: E402
import web  # noqa: E402

logging.disable(logging.WARNING)

NOW = 1_800_000_000.0  # 2027-01-15T08:00:00Z
BASE = "https://github.com/stephan271/smallworlds.git"
SHA = "a" * 40
OTHER = "b" * 40


def _load(name: str, path: str):
    """A Canary Follower module, under its own name so it cannot shadow ours."""
    if FOLLOWER_SRC not in sys.path:
        sys.path.append(FOLLOWER_SRC)  # ringstate imports the follower's github module
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def app(name, sync="Synced", health="Healthy", revision=SHA, **status):
    body = {"metadata": {"name": name, "namespace": "argocd",
                         "annotations": {"argocd.argoproj.io/sync-wave": "0"}},
            "spec": {"source": {"repoURL": BASE, "path": f"infrastructure/kubernetes/tenants/{name}"}},
            "status": {"sync": {"status": sync, "revision": revision}, "health": {"status": health}}}
    body["status"].update(status)
    return body


def ingress(ingress_name, host, /, **annotations):
    return {"metadata": {"name": ingress_name, "annotations": {f"smallworlds.network/board-{k}": v
                                                       for k, v in annotations.items()}},
            "spec": {"rules": [{"host": host}]}}


ARGOCD_INGRESS = ingress("argocd-server-private", "deploy.example.org", id="argocd", name="Argo CD",
                         group="Operate", order="10")
GRAFANA_INGRESS = ingress("grafana-private", "monitoring.example.org", id="grafana", name="Grafana",
                          order="20")


def am_alert(alertname, /, severity="warning", silenced=False, inhibited=False, **labels):
    """One alert as Alertmanager's /api/v2/alerts returns it."""
    return {"labels": {"alertname": alertname, "severity": severity, **labels},
            "annotations": {"summary": f"{alertname} summary"},
            "startsAt": "2027-01-15T07:00:00Z",
            "status": {"state": "suppressed" if silenced or inhibited else "active",
                       "silencedBy": ["s1"] if silenced else [], "inhibitedBy": ["i1"] if inhibited else [],
                       "mutedBy": []}}


WATCHDOG = am_alert("Watchdog", severity="none")


def ago(hours):
    return state.iso(NOW - hours * 3600)


NODE = {"metadata": {"name": "node-1"},
        "status": {"nodeInfo": {"kubeletVersion": "v1.36.5+k3s1"},
                   "conditions": [{"type": "Ready", "status": "True"},
                                  {"type": "DiskPressure", "status": "False"}]}}


def metrics(root_free=65.0, days_left=-1.0, pv_free=64.0, data_free=None, sent=2.07, failed=0.0, reload_ok=1.0):
    free = [{"metric": {"mountpoint": "/"}, "value": root_free}]
    if data_free is not None:
        free.append({"metric": {"mountpoint": "/mnt/smallworlds-data"}, "value": data_free})
    return {"free_percent": free, "days_left": [{"metric": {"mountpoint": "/"}, "value": days_left}],
            "pv_free_percent": [{"metric": {}, "value": pv_free}],
            "uptime_seconds": [{"metric": {}, "value": 12 * 86400}],
            **{name: [{"metric": {}, "value": value}] for name, value in
               (("webhook_sent_15m", sent), ("webhook_failed_1h", failed), ("alertmanager_reload_ok", reload_ok))
               if value is not None}}


# As Ring 1's Alertmanager lists them (2026-11-22).
RECEIVERS = ["monitoring/smallworlds-alerts/discard", "monitoring/smallworlds-alerts/push",
             "monitoring/smallworlds-heartbeat/heartbeat", "null"]


def cnpg(namespace, name="database", server=None, store="postgres-backups", conditions=()):
    parameters = {"barmanObjectName": store}
    if server:
        parameters["serverName"] = server
    return {"metadata": {"namespace": namespace, "name": name},
            "spec": {"plugins": [{"name": "barman-cloud.cloudnative-pg.io", "enabled": True,
                                  "parameters": parameters}]},
            "status": {"conditions": list(conditions)}}


def objectstore(namespace, windows, name="postgres-backups"):
    return {"metadata": {"namespace": namespace, "name": name},
            "status": {"serverRecoveryWindow": {s: {"lastSuccessfulBackupTime": t} for s, t in windows.items()}}}


def cronjob(namespace, name, schedule="0 1 * * *", last_ok=None, last_scheduled=None, suspend=False):
    return {"metadata": {"namespace": namespace, "name": name},
            "spec": {"schedule": schedule, "suspend": suspend},
            "status": {k: v for k, v in (("lastSuccessfulTime", last_ok), ("lastScheduleTime", last_scheduled)) if v}}


def ring0_doc(commits=None, now_state="up-to-date", updated_hours=0.1, stuck=None):
    """ring0.json, newest first, in the follower's shape. By default SHA is the
    newest verified commit: what build() runs unless told otherwise."""
    commits = commits if commits is not None else [
        {"commit": SHA, "title": "Update Helm chart velero", "date": ago(2), "verdict": {"state": "success"}},
        {"commit": "c" * 40, "title": "a branch commit", "date": ago(3), "verdict": None},
        {"commit": OTHER, "title": "Update nextcloud", "date": ago(5), "verdict": {"state": "success"}}]
    return {"schema": 1, "updated": ago(updated_hours), "repository": "stephan271/smallworlds",
            "head": {"commit": commits[0]["commit"] if commits else SHA, "state": "success", "date": ago(2)},
            "now": {"state": now_state, "commit": SHA, "step": "checks" if now_state == "updating" else None,
                    "pull": {"title": "Update immich"} if now_state == "updating" else None, "stuck": stuck},
            "commits": commits}


def backups_data(clusters=None, stores=None, cronjobs=None, velero_age=15):
    return {
        "clusters": clusters if clusters is not None else [cnpg("nextcloud", server="nextcloud-database")],
        "objectstores": stores if stores is not None else [objectstore("nextcloud", {"nextcloud-database": ago(15)})],
        "schedules": [{"metadata": {"name": "velero-daily-cluster-state"}, "spec": {}}],
        "velero_backups": [{"metadata": {"name": "v-1", "creationTimestamp": ago(velero_age),
                                         "labels": {"velero.io/schedule-name": "velero-daily-cluster-state"}},
                            "status": {"phase": "Completed", "completionTimestamp": ago(velero_age)}}],
        "cronjobs": cronjobs if cronjobs is not None else [
            cronjob("nextcloud", "pv-backup", last_ok=ago(16), last_scheduled=ago(16))],
    }


def reading(data=None, error=None, fetched=NOW, last_good=NOW):
    return state.Reading(data=data, error=error, fetched_at=fetched,
                         last_good_at=None if data is None else last_good)


def build(apps=(), ingresses=(ARGOCD_INGRESS, GRAFANA_INGRESS), applications=None, ingress_reading=None,
          alerts=(WATCHDOG,), alert_reading=None, nodes=None, prometheus=None, backups=None, rings=None,
          receivers=None):
    return state.build(
        {"applications": applications or reading({"items": list(apps)}),
         "ingresses": ingress_reading or reading({"items": list(ingresses)}),
         "alerts": alert_reading or reading(list(alerts)),
         "nodes": nodes or reading({"items": [NODE]}),
         "prometheus": prometheus or reading(metrics()),
         "backups": backups or reading(backups_data()),
         "rings": rings or reading({"ring0": ring0_doc(), "compare": None, "compare_error": None}),
         "receivers": receivers or reading(list(RECEIVERS))}, NOW,
        base_repo_url=BASE, root_app="smallworlds-root",
        ring0_board_url="https://stephan271.github.io/smallworlds/", refresh_seconds=30,
        slow_minutes=10)


def colours(document):
    return ({document["verdict"]["colour"]} | {t["colour"] for t in document["tiles"]}
            | {a["colour"] for a in document["applications"]}
            | {r["colour"] for r in document["verdict"]["reasons"]})


class Verdict(unittest.TestCase):
    def test_everything_settled_is_green_and_says_what_it_covers(self):
        document = build([app("keycloak"), app("nextcloud")])
        self.assertEqual("green", document["verdict"]["colour"])
        self.assertEqual("Nothing needs attention", document["verdict"]["headline"])
        self.assertEqual(["Alertmanager's alerts", "Argo CD Applications", "the node", "backups", "updates",
                          "alert delivery"], document["verdict"]["covers"])
        self.assertEqual(["certificates"], document["verdict"]["not_yet"])
        tiles = {t["id"]: t for t in document["tiles"]}
        self.assertEqual("2 of 2 settled", tiles["applications"]["headline"])
        self.assertEqual(("green", "none firing"), (tiles["alerts"]["colour"], tiles["alerts"]["headline"]))

    def test_progressing_is_amber_with_a_link_into_argo_cd(self):
        document = build([app("keycloak"), app("nextcloud", health="Progressing")])
        self.assertEqual("amber", document["verdict"]["colour"])
        self.assertEqual("1 thing needs a look", document["verdict"]["headline"])
        reason = document["verdict"]["reasons"][0]
        self.assertTrue(reason["text"].startswith("nextcloud: Synced / Progressing"))
        self.assertEqual("https://deploy.example.org/applications/argocd/nextcloud", reason["link"])
        self.assertEqual("nextcloud", document["applications"][0]["name"], "problems come first")

    def test_nothing_the_board_reads_by_itself_is_red(self):
        """Red comes only from Alertmanager (ADR 0068)."""
        document = build([app("a", health="Degraded"), app("b", sync="OutOfSync", health="Missing"),
                          app("c", sync="Unknown", health="Unknown")])
        self.assertNotIn("red", colours(document))

    def test_a_source_that_never_answered_is_grey_never_green(self):
        document = build(applications=reading(None, error="HTTP 403 Forbidden"))
        self.assertEqual("grey", document["verdict"]["colour"])
        self.assertEqual("grey", {t["id"]: t for t in document["tiles"]}["applications"]["colour"])
        self.assertEqual("Part of the cluster could not be read", document["verdict"]["headline"])
        self.assertIn("HTTP 403 Forbidden", document["verdict"]["reasons"][0]["text"])
        self.assertIn("Nothing has been read from it yet", document["verdict"]["reasons"][0]["text"])

    def test_a_source_that_stopped_keeps_its_last_answer_greyed_with_its_age(self):
        stale = state.Reading(data={"items": [app("keycloak")]}, error="unreachable (timed out)",
                              fetched_at=NOW, last_good_at=NOW - 600)
        document = build(applications=stale)
        self.assertEqual("grey", {t["id"]: t for t in document["tiles"]}["applications"]["colour"])
        self.assertEqual(["keycloak"], [a["name"] for a in document["applications"]])
        self.assertIn("10 min old", document["verdict"]["reasons"][0]["text"])
        self.assertEqual("grey", document["verdict"]["colour"])

    def test_unread_sources_are_never_green(self):
        document = build(applications=state.Reading(), ingress_reading=state.Reading(),
                         alert_reading=state.Reading(), nodes=state.Reading(), prometheus=state.Reading(),
                         backups=state.Reading(), rings=state.Reading(), receivers=state.Reading())
        self.assertEqual("grey", document["verdict"]["colour"])

    def test_amber_outranks_grey(self):
        document = build([app("nextcloud", health="Progressing")],
                         ingress_reading=reading(None, error="HTTP 500"))
        self.assertEqual("amber", document["verdict"]["colour"])
        self.assertEqual(["amber", "grey"], [r["colour"] for r in document["verdict"]["reasons"]])


class Alerts(unittest.TestCase):
    def test_a_firing_alert_is_red_with_a_link_into_grafana(self):
        document = build([app("keycloak")], alerts=[WATCHDOG, am_alert("CNPGBackupStale", "critical",
                                                                       cluster="gallery-db", namespace="immich")])
        v = document["verdict"]
        self.assertEqual(("red", "1 alert firing"), (v["colour"], v["headline"]))
        self.assertEqual("CNPGBackupStale (gallery-db, critical): CNPGBackupStale summary", v["reasons"][0]["text"])
        self.assertEqual("https://monitoring.example.org/alerting/list?search=CNPGBackupStale", v["reasons"][0]["link"])
        tiles = {t["id"]: t for t in document["tiles"]}
        self.assertEqual(("red", "1 firing", "1 critical"),
                         (tiles["alerts"]["colour"], tiles["alerts"]["headline"], tiles["alerts"]["detail"]))
        self.assertEqual("backups", document["alerts"]["firing"][0]["tile"])
        self.assertEqual("green", tiles["applications"]["colour"], "a backup alert is not an application's")

    def test_an_application_alert_makes_its_card_and_tile_red(self):
        document = build([app("nextcloud", health="Degraded"), app("keycloak")],
                         alerts=[WATCHDOG, am_alert("ApplicationNotHealthy", name="nextcloud")])
        cards = {c["name"]: c for c in document["applications"]}
        self.assertEqual(("red", ["ApplicationNotHealthy"]), (cards["nextcloud"]["colour"], cards["nextcloud"]["alerts"]))
        self.assertEqual("green", cards["keycloak"]["colour"])
        self.assertEqual("red", {t["id"]: t for t in document["tiles"]}["applications"]["colour"])
        self.assertEqual("nextcloud", document["applications"][0]["name"], "red comes first")
        self.assertEqual("1 alert firing", document["verdict"]["headline"], "one problem, named once")
        self.assertEqual(1, len(document["verdict"]["reasons"]))
        both = build([app("nextcloud", health="Degraded"), app("keycloak", health="Progressing")],
                     alerts=[WATCHDOG, am_alert("ApplicationNotHealthy", name="nextcloud")])
        self.assertEqual("1 alert firing, and 1 more needs a look", both["verdict"]["headline"])

    def test_an_alert_in_an_applications_namespace_is_that_applications(self):
        immich = app("immich")
        immich["spec"]["destination"] = {"namespace": "immich"}
        card = build([immich], alerts=[WATCHDOG, am_alert("KubePodCrashLooping", namespace="immich",
                                                          pod="immich-server-0")])["applications"][0]
        self.assertEqual(("red", ["KubePodCrashLooping"]), (card["colour"], card["alerts"]))

    def test_silenced_and_inhibited_alerts_are_shown_but_never_counted(self):
        document = build([app("a")], alerts=[WATCHDOG, am_alert("NodeDiskSpaceEarlyWarning", silenced=True),
                                             am_alert("KubeJobFailed", inhibited=True)])
        self.assertEqual("green", document["verdict"]["colour"])
        self.assertEqual(2, len(document["verdict"]["suppressed"]))
        self.assertEqual("2 silenced or inhibited", {t["id"]: t for t in document["tiles"]}["alerts"]["detail"])

    def test_watchdog_and_info_alerts_never_count(self):
        document = build([app("a")], alerts=[WATCHDOG, am_alert("InfoInhibitor", severity="info")])
        self.assertEqual("green", document["verdict"]["colour"])
        self.assertEqual([], document["alerts"]["firing"])

    def test_a_missing_watchdog_is_amber(self):
        document = build([app("a")], alerts=[])
        tiles = {t["id"]: t for t in document["tiles"]}
        self.assertEqual("amber", tiles["alerts"]["colour"])
        self.assertEqual("amber", document["verdict"]["colour"])
        self.assertIn("Watchdog is not firing", document["verdict"]["reasons"][0]["text"])

    def test_alertmanager_down_is_grey_never_green(self):
        document = build([app("a")], alert_reading=reading(None, error="unreachable (timed out)"))
        self.assertEqual("grey", {t["id"]: t for t in document["tiles"]}["alerts"]["colour"])
        self.assertEqual("grey", document["verdict"]["colour"])
        self.assertIn("Alertmanager did not answer", document["verdict"]["reasons"][0]["text"])

    def test_alerts_known_before_alertmanager_stopped_stay_red(self):
        """Hiding a known alert because Alertmanager blinked would be worse than a stale red."""
        stale = state.Reading(data=[WATCHDOG, am_alert("NodeDiskSpaceCritical", "critical")],
                              error="HTTP 503 Service Unavailable", fetched_at=NOW, last_good_at=NOW - 120)
        document = build([app("a")], alert_reading=stale)
        self.assertEqual("red", document["verdict"]["colour"])
        self.assertEqual("grey", {t["id"]: t for t in document["tiles"]}["alerts"]["colour"])

    def test_every_rule_of_ours_lands_on_a_tile_on_purpose(self):
        """A new rule must be placed: on a tile by its name, or in ON_ALERTS_TILE."""
        names = set()
        for directory, _, files in os.walk(K8S):
            if "/charts/" in directory + "/":
                continue
            for name in files:
                if name.endswith(".yaml"):
                    with open(os.path.join(directory, name)) as handle:
                        names |= set(re.findall(r"^\s*- alert:\s*(\S+)", handle.read(), re.M))
        self.assertGreaterEqual(len(names), 14)
        expected = {"ApplicationNotHealthy": "applications", "CNPGBackupStale": "backups",
                    "NodeDiskSpaceCritical": "node", "PersistentVolumeSpaceHigh": "node",
                    "PodArchiveDeviceStale": "backups", "CanaryCommitFailed": "updates",
                    "Ring0NeedsPerson": "updates", "VeleroBackupStale": "backups"}
        for name in sorted(names):
            tile = state.tile_of(name)
            self.assertTrue(tile != "alerts" or name in state.ON_ALERTS_TILE,
                            f"{name} lands on no tile: add its prefix to ALERT_TILES or it to ON_ALERTS_TILE")
            if name in expected:
                self.assertEqual(expected[name], tile, name)
        self.assertEqual("node", state.tile_of("TargetDown", {"job": "node-exporter"}))
        self.assertEqual("alerts", state.tile_of("TargetDown", {"job": "kube-state-metrics"}))


class Node(unittest.TestCase):
    def tile(self, document):
        return {t["id"]: t for t in document["tiles"]}["node"]

    def test_a_ready_node_with_room_is_green(self):
        tile = self.tile(build([app("a")]))
        self.assertEqual(("green", "Ready"), (tile["colour"], tile["headline"]))
        self.assertEqual("v1.36.5+k3s1 · up 12 days", tile["detail"])
        self.assertEqual(["/ 65% free", "fullest volume 64% free"], [i["text"] for i in tile["items"]])

    def test_low_space_and_a_fast_fill_are_amber_before_the_alerts(self):
        document = build([app("a")], prometheus=reading(metrics(root_free=22, data_free=80)))
        self.assertEqual("amber", self.tile(document)["colour"])
        self.assertIn("Disk / 22% free", [r["text"] for r in document["verdict"]["reasons"]])
        filling = self.tile(build([app("a")], prometheus=reading(metrics(days_left=5.2))))
        self.assertEqual(("amber", "/ 65% free, full in 5 days at this rate"),
                         (filling["colour"], filling["items"][0]["text"]))
        self.assertEqual("amber", self.tile(build([app("a")], prometheus=reading(metrics(pv_free=12))))["colour"])

    def test_not_ready_or_under_pressure_is_amber(self):
        sick = json.loads(json.dumps(NODE))
        sick["status"]["conditions"] = [{"type": "Ready", "status": "False", "message": "kubelet stopped posting"},
                                        {"type": "DiskPressure", "status": "True"}]
        document = build([app("a")], nodes=reading({"items": [sick]}))
        self.assertEqual(("amber", "not Ready"), (self.tile(document)["colour"], self.tile(document)["headline"]))
        texts = [r["text"] for r in document["verdict"]["reasons"]]
        self.assertIn("Node node-1 is not Ready: kubelet stopped posting", texts)
        self.assertIn("Node node-1 reports DiskPressure", texts)

    def test_a_node_alert_is_red(self):
        document = build([app("a")], alerts=[WATCHDOG, am_alert("NodeDiskSpaceCritical", "critical")])
        self.assertEqual("red", self.tile(document)["colour"])

    def test_prometheus_silent_means_grey_never_green(self):
        document = build([app("a")], prometheus=reading(None, error="unreachable (timed out)"))
        self.assertEqual("grey", self.tile(document)["colour"])
        self.assertIn("Prometheus did not answer", " ".join(r["text"] for r in document["verdict"]["reasons"]))


class Backups(unittest.TestCase):
    def tile(self, document):
        return {t["id"]: t for t in document["tiles"]}["backups"]

    def test_recent_backups_are_green(self):
        tile = self.tile(build([app("a")]))
        self.assertEqual(("green", "3 recent"), (tile["colour"], tile["headline"]))
        self.assertEqual(["database nextcloud/database: 15 h ago",
                          "cluster state (velero-daily-cluster-state): 15 h ago",
                          "nextcloud/pv-backup: 16 h ago"], [i["text"] for i in tile["items"]])

    def test_the_server_a_cluster_writes_to_is_read_not_an_old_one(self):
        """Ring 0's Immich ObjectStore still lists immich-database from before PostgreSQL 18."""
        data = backups_data(clusters=[cnpg("immich", server="immich-database-pg18"), cnpg("keycloak", "keycloak-db",
                                                                                           store="pgb-keycloak")],
                            stores=[objectstore("immich", {"immich-database": ago(130), "immich-database-pg18": ago(15)}),
                                    objectstore("keycloak", {"keycloak-db": ago(2)}, name="pgb-keycloak")])
        items = [i["text"] for i in self.tile(build([app("a")], backups=reading(data)))["items"]]
        self.assertIn("database immich/database: 15 h ago", items)
        self.assertIn("database keycloak/keycloak-db: 2 h ago", items, "serverName defaults to the Cluster's name")

    def test_stale_or_failing_backups_are_amber_with_a_link_to_the_docs(self):
        data = backups_data(stores=[objectstore("nextcloud", {"nextcloud-database": ago(30)})], velero_age=40)
        document = build([app("a")], backups=reading(data))
        tile = self.tile(document)
        self.assertEqual(("amber", "2 not recent"), (tile["colour"], tile["headline"]))
        reason = next(r for r in document["verdict"]["reasons"] if r["tile"] == "backups")
        self.assertTrue(reason["link"].endswith(f"/blob/{SHA}/doc/storage-and-backup.md"))
        failing = backups_data(clusters=[cnpg("nextcloud", server="nextcloud-database", conditions=[
            {"type": "ContinuousArchiving", "status": "False", "message": "WAL archive failed: 403"}])])
        item = self.tile(build([app("a")], backups=reading(failing)))["items"][0]
        self.assertEqual(("amber", "database nextcloud/database: 15 h ago — ContinuousArchiving: WAL archive failed: 403"),
                         (item["colour"], item["text"]))

    def test_no_backup_ever_is_amber(self):
        data = backups_data(stores=[])
        self.assertEqual("database nextcloud/database: no successful backup recorded",
                         self.tile(build([app("a")], backups=reading(data)))["items"][0]["text"])

    def test_a_missed_cronjob_run_is_amber_a_suspended_one_is_a_fact(self):
        data = backups_data(cronjobs=[
            cronjob("forgejo", "pv-backup", last_ok=ago(40), last_scheduled=ago(16)),
            cronjob("backup-replicator", "backup-replicator", last_ok=ago(1), suspend=True),
            cronjob("ddns", "ddns", schedule="*/5 * * * *", last_ok=ago(30))])
        tile = self.tile(build([app("a")], backups=reading(data)))
        items = {i["text"]: i for i in tile["items"]}
        self.assertEqual("amber", items["forgejo/pv-backup: the last run did not succeed (last success 40 h ago)"]["colour"])
        suspended = items["backup-replicator/backup-replicator: suspended, last run 1 h ago"]
        self.assertTrue(suspended["neutral"])
        self.assertNotIn("ddns", " ".join(items), "only backup CronJobs")
        self.assertEqual("1 not recent", tile["headline"])

    def test_a_running_job_is_not_a_missed_one(self):
        data = backups_data(cronjobs=[cronjob("nextcloud", "pv-backup", last_ok=ago(24.5), last_scheduled=ago(0.5))])
        self.assertEqual("green", self.tile(build([app("a")], backups=reading(data)))["colour"])

    def test_a_backup_alert_is_red_and_unread_is_grey(self):
        red = build([app("a")], alerts=[WATCHDOG, am_alert("VeleroBackupStale", schedule="daily")])
        self.assertEqual("red", self.tile(red)["colour"])
        grey = build([app("a")], backups=reading(None, error="HTTP 403 Forbidden"))
        self.assertEqual(("grey", "not read"), (self.tile(grey)["colour"], self.tile(grey)["headline"]))

    def test_cnpg_six_field_schedules_count_as_daily(self):
        self.assertTrue(state._daily("0 0 2 * * *"))
        self.assertTrue(state._daily("45 0 * * *"))
        self.assertFalse(state._daily("*/5 * * * *"))
        self.assertFalse(state._daily("0 4 * * 0"))


class Updates(unittest.TestCase):
    def tile(self, document):
        return {t["id"]: t for t in document["tiles"]}["updates"]

    def rings(self, ring0=None, compare=None, error=None):
        return reading({"ring0": ring0 if ring0 is not None else ring0_doc(), "compare": compare,
                        "compare_error": error})

    def test_running_the_newest_verified_commit_is_green(self):
        tile = self.tile(build([app("a")]))
        self.assertEqual(("green", "up to date with Ring 0"), (tile["colour"], tile["headline"]))
        self.assertEqual([f"runs {SHA[:7]} — Update Helm chart velero",
                          f"newest verified {SHA[:7]} — Update Helm chart velero, 2 h ago",
                          "Ring 0 is up to date"], [i["text"] for i in tile["items"]])

    def test_verified_steps_behind_count_only_verdicts_not_branch_commits(self):
        d = "d" * 40
        ring0 = ring0_doc([
            {"commit": SHA, "title": "newest", "date": ago(1), "verdict": {"state": "success"}},
            {"commit": "c" * 40, "title": "branch commit", "date": ago(2), "verdict": None},
            {"commit": "e" * 40, "title": "rejected", "date": ago(3), "verdict": {"state": "failure"}},
            {"commit": "f" * 40, "title": "also verified", "date": ago(4), "verdict": {"state": "success"}},
            {"commit": d, "title": "what this cluster runs", "date": ago(9), "verdict": {"state": "success"}}])
        document = build([app("a", revision=d)], rings=self.rings(ring0))
        self.assertEqual(("amber", "2 verified commits behind"), (self.tile(document)["colour"], self.tile(document)["headline"]))
        reason = next(r for r in document["verdict"]["reasons"] if r["tile"] == "updates")
        self.assertTrue(reason["text"].startswith("Updates: 2 verified commits behind"))
        self.assertEqual("https://stephan271.github.io/smallworlds/", reason["link"])

    def test_older_than_the_list_uses_the_compare(self):
        old = "0" * 40
        document = build([app("a", revision=old)], rings=self.rings(compare={"status": "ahead", "ahead_by": 46}))
        self.assertEqual(("amber", "46 commits behind"), (self.tile(document)["colour"], self.tile(document)["headline"]))
        unknown = build([app("a", revision=old)], rings=self.rings(error="HTTP 403 rate limit exceeded"))
        self.assertEqual("more than 25 commits behind", self.tile(unknown)["headline"])
        same = build([app("a", revision=old)], rings=self.rings(compare={"status": "identical", "ahead_by": 0}))
        self.assertEqual("green", self.tile(same)["colour"])

    def test_an_unverified_or_rejected_commit_is_said_so(self):
        newer = "9" * 40
        ring0 = ring0_doc([{"commit": newer, "title": "being checked", "date": ago(0.2), "verdict": None}]
                          + ring0_doc()["commits"])
        tile = self.tile(build([app("a", revision=newer)], rings=self.rings(ring0)))
        self.assertEqual("runs a commit Ring 0 has not verified", tile["headline"])
        ring0["commits"][0]["verdict"] = {"state": "failure"}
        self.assertEqual("runs a commit Ring 0 rejected",
                         self.tile(build([app("a", revision=newer)], rings=self.rings(ring0)))["headline"])

    def test_a_stale_ring_state_is_grey(self):
        document = build([app("a")], rings=self.rings(ring0_doc(updated_hours=3)))
        self.assertEqual("grey", self.tile(document)["colour"])
        self.assertIn("Ring 0 has not reported since", " ".join(r["text"] for r in document["verdict"]["reasons"]))

    def test_turned_off_or_unread_is_grey(self):
        off = build([app("a")], rings=reading({"disabled": True}))
        self.assertEqual(("grey", "unknown"), (self.tile(off)["colour"], self.tile(off)["headline"]))
        unread = build([app("a")], rings=reading(None, error="unreachable (Name or service not known)"))
        self.assertEqual("grey", self.tile(unread)["colour"])
        self.assertEqual("grey", unread["verdict"]["colour"])

    def test_mixed_base_commits_need_a_look(self):
        document = build([app("a"), app("b", revision=OTHER)])
        self.assertIn(f"Applications run different base commits: b@{OTHER[:12]}",
                      [r["text"] for r in document["verdict"]["reasons"]])

    def test_on_ring_0_the_tile_shows_ring_0(self):
        tile = self.tile(build([app("a"), app("canary-follower")]))
        self.assertEqual(("green", "this is Ring 0"), (tile["colour"], tile["headline"]))
        busy = self.tile(build([app("canary-follower")], rings=self.rings(ring0_doc(now_state="updating"))))
        self.assertEqual(("green", "Ring 0 is checking Update immich (checks)"), (busy["colour"], busy["items"][0]["text"]))
        stuck = build([app("canary-follower")], rings=self.rings(ring0_doc(
            now_state="stuck", stuck={"condition": "S1", "summary": "main's head was rejected"})))
        self.assertEqual("amber", self.tile(stuck)["colour"])
        self.assertIn("Ring 0 is stuck: main's head was rejected", [r["text"] for r in stuck["verdict"]["reasons"]])

    def test_a_canary_alert_is_red(self):
        document = build([app("canary-follower")], alerts=[WATCHDOG, am_alert("CanaryCommitFailed")])
        self.assertEqual("red", self.tile(document)["colour"])


class RingsCollector(unittest.TestCase):
    def collector(self, apps, compare, clock):
        return main.Collector(FakeKube({"applications": {"items": apps}, "ingresses": {"items": []}}),
                              clock=clock, fetch_alerts=lambda url, timeout: [WATCHDOG], fetch_metrics=no_metrics,
                              fetch_ring0=lambda url, timeout: ring0_doc(), compare=compare,
                              fetch_receivers=lambda url, timeout: list(RECEIVERS))

    def test_compare_only_for_an_unlisted_commit_and_at_most_hourly(self):
        calls = []

        def compare(api, repository, base, head, timeout):
            calls.append((repository, base, head))
            return {"status": "ahead", "ahead_by": 46}
        now = [NOW]
        listed = self.collector([app("a")], compare, lambda: now[0])
        listed.cycle()
        self.assertEqual([], calls, "a listed commit needs no compare")
        old = "0" * 40
        unlisted = self.collector([app("a", revision=old)], compare, lambda: now[0])
        unlisted.cycle()
        unlisted.cycle()
        self.assertEqual([("stephan271/smallworlds", old, SHA)], calls)
        self.assertEqual("46 commits behind",
                         {t["id"]: t for t in unlisted.document()["tiles"]}["updates"]["headline"])
        now[0] += config.COMPARE_SECONDS
        unlisted.cycle()
        self.assertEqual(2, len(calls))

    def test_a_failing_compare_does_not_fail_the_reading(self):
        def compare(*args):
            raise urllib.error.HTTPError("https://api.github.com/x", 403, "rate limit exceeded", {}, None)
        collector = self.collector([app("a", revision="0" * 40)], compare, lambda: NOW)
        collector.cycle()
        document = collector.document()
        self.assertTrue(document["sources"]["rings"]["ok"])
        self.assertEqual("more than 25 commits behind",
                         {t["id"]: t for t in document["tiles"]}["updates"]["headline"])


class Delivery(unittest.TestCase):
    def tile(self, document):
        return {t["id"]: t for t in document["tiles"]}["delivery"]

    def test_both_receivers_and_a_recent_send_are_working(self):
        tile = self.tile(build([app("a")]))
        self.assertEqual(("green", "working"), (tile["colour"], tile["headline"]))
        self.assertEqual(["push to your phone: configured", "heartbeat: configured",
                          "sent in the last 15 min: 2", "failed in the last hour: none"],
                         [i["text"] for i in tile["items"]])

    def test_a_missing_receiver_is_amber_and_says_which_secret(self):
        document = build([app("a")], receivers=reading(["monitoring/smallworlds-alerts/discard", "null"]),
                         prometheus=reading(metrics(sent=0)))
        tile = self.tile(document)
        self.assertEqual(("amber", "not configured"), (tile["colour"], tile["headline"]))
        reason = next(r for r in document["verdict"]["reasons"] if r["tile"] == "delivery")
        self.assertIn("the push and the heartbeat are not configured", reason["text"])
        self.assertIn("monitoring/smallworlds-notify", reason["text"])
        self.assertTrue(reason["link"].endswith(f"/blob/{SHA}/doc/alerting.md"))
        # Nothing sent is expected without a heartbeat: only one reason.
        self.assertEqual(1, sum(1 for r in document["verdict"]["reasons"] if r["tile"] == "delivery"))

    def test_a_silent_heartbeat_is_amber(self):
        tile = self.tile(build([app("a")], prometheus=reading(metrics(sent=0))))
        self.assertEqual(("amber", "heartbeat silent"), (tile["colour"], tile["headline"]))

    def test_failures_and_a_failed_reload_are_amber(self):
        failing = build([app("a")], prometheus=reading(metrics(failed=3)))
        self.assertEqual(("amber", "failing"), (self.tile(failing)["colour"], self.tile(failing)["headline"]))
        self.assertIn("Alert delivery: 3 notifications failed in the last hour (a wrong or expired push or heartbeat URL).",
                      [r["text"] for r in failing["verdict"]["reasons"]])
        reload = self.tile(build([app("a")], prometheus=reading(metrics(reload_ok=0))))
        self.assertEqual("amber", reload["colour"])

    def test_an_alertmanager_alert_is_red(self):
        document = build([app("a")], alerts=[WATCHDOG, am_alert("AlertmanagerFailedToSendAlerts")])
        self.assertEqual("red", self.tile(document)["colour"])
        self.assertEqual("delivery", document["alerts"]["firing"][0]["tile"])

    def test_unread_is_grey_and_no_url_ever_appears(self):
        grey = build([app("a")], receivers=reading(None, error="HTTP 503 Service Unavailable"))
        self.assertEqual("grey", self.tile(grey)["colour"])
        self.assertEqual("grey", self.tile(build([app("a")], prometheus=reading(metrics(sent=None))))["colour"])
        text = json.dumps(build([app("a")]))
        for credential in ("ntfy.sh", "hc-ping.com", "alerts-url", "heartbeat-url"):
            self.assertNotIn(credential, text)


class Applications(unittest.TestCase):
    def test_a_failed_sync_hook_on_a_healthy_app_is_amber_with_the_reason(self):
        failed = app("stalwart", operationState={
            "phase": "Failed", "message": "one or more synchronization tasks completed unsuccessfully",
            "syncResult": {"resources": [{"kind": "Job", "name": "stalwart-init", "hookType": "Sync",
                                          "hookPhase": "Failed", "message": "Job has reached the specified backoff limit"}]}})
        card = build([failed])["applications"][0]
        self.assertEqual("amber", card["colour"])
        self.assertEqual("sync Failed: Job stalwart-init: Job has reached the specified backoff limit",
                         card["why"])

    def test_an_unreachable_upstream_is_named(self):
        broken = app("forgejo", sync="Unknown", health="Healthy", conditions=[{
            "type": "ComparisonError",
            "message": "Manifest generation error: https://code.forgejo.org/forgejo-helm response status code 503"}])
        card = build([broken])["applications"][0]
        self.assertEqual("amber", card["colour"])
        self.assertEqual("upstream unreachable: code.forgejo.org: response status code 503", card["why"])

    def test_an_unhealthy_resource_is_named(self):
        sick = app("immich", health="Degraded", resources=[
            {"kind": "Deployment", "name": "immich-server", "health": {"status": "Degraded",
                                                                     "message": "Deployment exceeded its progress deadline"}}])
        self.assertEqual("Deployment immich-server: Degraded (Deployment exceeded its progress deadline)",
                         build([sick])["applications"][0]["why"])

    def test_a_slow_operation_is_amber_after_ten_minutes(self):
        def running(minutes):
            return app("nextcloud", operation={"sync": {}}, operationState={
                "phase": "Running", "startedAt": state.iso(NOW - minutes * 60)})
        self.assertEqual("green", build([running(9)])["applications"][0]["colour"])
        card = build([running(12)])["applications"][0]
        self.assertEqual(("amber", 12), (card["colour"], card["running_minutes"]))

    def test_messages_are_cut_to_300_characters(self):
        long = app("x", health="Degraded")
        long["status"]["health"]["message"] = "y" * 1000
        self.assertEqual(300, len(build([long])["applications"][0]["why"]))

    def test_groups_follow_the_follower(self):
        groups = {a["name"]: a["group"] for a in build(
            [app("nextcloud"), app("traefik"), app("smallworlds-root")])["applications"]}
        self.assertEqual({"nextcloud": "apps", "traefik": "platform", "smallworlds-root": "machinery"}, groups)

    def test_base_revision_and_mixed(self):
        document = build([app("a"), app("b"), app("c", revision=OTHER)])
        self.assertEqual(SHA, document["base"]["revision"])
        self.assertEqual([f"c@{OTHER[:12]}"], document["base"]["mixed"])

    def test_no_argo_cd_link_without_its_ingress(self):
        self.assertIsNone(build([app("a")], ingresses=())["applications"][0]["link"])


class Links(unittest.TestCase):
    def test_found_on_ingresses_with_their_own_host(self):
        found = links.from_ingresses([
            ARGOCD_INGRESS,
            ingress("keycloak-admin-private", "identity.example.org", id="keycloak", name="Keycloak admin",
                    path="/admin/master/console/"),
            ingress("join-desk-private", "join.example.org", name="Invitation Desk", group="Members",
                    path="desk/"),
            ingress("grafana-private", "monitoring.example.org", id="grafana", name="Grafana", order="20"),
            ingress("unannotated", "files.example.org"),
            ingress("odd", "odd.example.org", name="Odd", group="Nonsense"),
        ])
        urls = {l["name"]: (l["group"], l["url"]) for l in found}
        self.assertEqual(("Operate", "https://deploy.example.org/"), urls["Argo CD"])
        self.assertEqual(("Operate", "https://identity.example.org/admin/master/console/"), urls["Keycloak admin"])
        self.assertEqual(("Members", "https://join.example.org/desk/"), urls["Invitation Desk"])
        self.assertEqual(("Operate", "https://monitoring.example.org/explore"), urls["Logs (Grafana Explore)"])
        self.assertEqual("Operate", urls["Odd"][0], "an unknown group falls back to Operate")
        self.assertNotIn("unannotated", json.dumps(found))

    def test_github_web(self):
        for url in ("https://github.com/o/r.git", "https://github.com/o/r", "git@github.com:o/r.git",
                    "ssh://git@github.com/o/r.git"):
            self.assertEqual("https://github.com/o/r", links.github_web(url), url)
        self.assertIsNone(links.github_web("https://forgejo.example.org/o/r.git"))
        self.assertIsNone(links.github_web(None))

    def test_fixed_links_follow_the_running_commit_and_hold_no_credential(self):
        fixed = links.fixed(BASE, SHA, "git@github.com:me/my-overlay.git", "https://ring0.example/")
        by_id = {l["id"]: l for l in fixed}
        self.assertTrue(by_id["alerting"]["url"].endswith(f"/blob/{SHA}/doc/alerting.md"))
        self.assertEqual("https://github.com/me/my-overlay", by_id["overlay"]["url"])
        self.assertIsNone(by_id["ntfy"]["url"], "the ntfy topic is a credential")
        self.assertIn("smallworlds-update", by_id["update-desk"]["command"])
        self.assertNotIn("ntfy.sh", json.dumps(fixed))
        self.assertNotIn("hc-ping.com", json.dumps(fixed))

    def test_the_overlay_link_comes_from_the_root_application(self):
        root = app("smallworlds-root")
        root["spec"]["source"]["repoURL"] = "https://github.com/me/my-overlay.git"
        found = {l["id"]: l for l in build([root])["links"]}
        self.assertEqual("https://github.com/me/my-overlay", found["overlay"]["url"])

    def test_operator_interfaces_carry_board_annotations(self):
        """Each operator router on the `operators` entrypoint names itself for the hub."""
        for path, tool in (("apps/argocd-ingress.yaml", "argocd"),
                           ("apps/monitoring-private-middleware.yaml", "grafana"),
                           ("tenants/keycloak/keycloak-admin-ingress.yaml", "keycloak"),
                           ("tenants/join/join-desk-ingress.yaml", "desk")):
            with open(os.path.join(K8S, path)) as handle:
                self.assertIn(f"smallworlds.network/board-id: {tool}\n", handle.read(), path)


class Parity(unittest.TestCase):
    """The Board's copies of the follower's rules stay the follower's (design §4.2)."""

    def test_argo_cd_wording_is_the_followers(self):
        follower = _load("follower_argocd", os.path.join(FOLLOWER_SRC, "argocd.py"))
        tree = ast.parse(inspect.getsource(argo))
        names = [n.name for n in tree.body if isinstance(n, ast.FunctionDef)]
        constants = [t.id for n in tree.body if isinstance(n, ast.Assign) for t in n.targets]
        self.assertGreaterEqual(len(names), 9)
        for name in names:
            self.assertEqual(inspect.getsource(getattr(follower, name)), inspect.getsource(getattr(argo, name)),
                             f"argo.{name} differs from the follower's; copy it again")
        for name in constants:
            self.assertEqual(getattr(follower, name), getattr(argo, name), name)

    def test_groups_are_the_followers(self):
        ringstate = _load("follower_ringstate", os.path.join(FOLLOWER_SRC, "ringstate.py"))
        self.assertEqual(ringstate.COMMUNITY_APPS, state.COMMUNITY_APPS)
        self.assertEqual(ringstate.MACHINERY, state.MACHINERY)


class ReadOnly(unittest.TestCase):
    """The Board changes nothing (ADR 0068)."""

    def test_rbac_reads_only_and_never_secrets(self):
        with open(os.path.join(TENANT, "rbac.yaml")) as handle:
            text = handle.read()
        verbs = set(re.findall(r'"(\w+)"', " ".join(re.findall(r"verbs:\s*\[([^\]]*)\]", text))))
        resources = set(re.findall(r'"([\w/*.-]+)"', " ".join(re.findall(r"resources:\s*\[([^\]]*)\]", text))))
        self.assertTrue(verbs, "no verbs found: the test no longer reads rbac.yaml")
        self.assertLessEqual(verbs, {"get", "list", "watch"})
        self.assertFalse(resources & {"secrets", "pods/log", "pods/exec", "pods/attach", "pods/portforward",
                                      "configmaps", "events", "*"}, resources)
        self.assertNotIn("nonResourceURLs", text)
        self.assertNotRegex(text, r"(?m)^\s*-?\s*verbs:\s*\[?\s*-")

    def test_the_kubernetes_client_has_no_method_that_writes(self):
        with open(os.path.join(TENANT, "src", "kube.py")) as handle:
            source = handle.read()
        tree = ast.parse(source)
        kube = next(n for n in tree.body if isinstance(n, ast.ClassDef) and n.name == "Kube")
        self.assertEqual({"__init__", "_token", "get", "get_or_empty"},
                         {n.name for n in kube.body if isinstance(n, ast.FunctionDef)})
        self.assertEqual(['"GET"'], re.findall(r'method=("\w+")', source))


class Packaging(unittest.TestCase):
    def test_every_source_and_static_file_is_in_the_configmaps(self):
        """A file missing from configMapGenerator is an import error in the pod,
        found only after a deploy. ConfigMap keys are flat, so neither may nest."""
        with open(os.path.join(TENANT, "kustomization.yaml")) as handle:
            kustomization = handle.read()
        for directory, suffixes in (("src", (".py",)), ("static", (".html", ".js", ".css"))):
            for name in sorted(os.listdir(os.path.join(TENANT, directory))):
                if name.endswith(suffixes):
                    self.assertIn(f"- {directory}/{name}\n", kustomization, name)

    def test_the_application_is_wave_0_and_tracks_head(self):
        with open(os.path.join(K8S, "apps", "operator-board.yaml")) as handle:
            text = handle.read()
        self.assertIn('argocd.argoproj.io/sync-wave: "0"', text)
        self.assertIn("targetRevision: HEAD", text)  # a release names its tag (CLAUDE.md)
        with open(os.path.join(K8S, "kustomization.yaml")) as handle:
            self.assertIn("  - apps/operator-board.yaml\n", handle.read())


class FakeKube:
    def __init__(self, answers):
        self.answers = {"nodes": {"items": [NODE]}, "clusters": {"items": []}, "objectstores": {"items": []},
                        "schedules": {"items": []}, "backups": {"items": []}, "cronjobs": {"items": []},
                        **answers}

    def get(self, path):
        answer = self.answers[path.rsplit("/", 1)[-1]]
        if isinstance(answer, Exception):
            raise answer
        return answer

    get_or_empty = get


def no_metrics(url, timeout):
    return metrics()


class Collector(unittest.TestCase):
    def test_a_failing_source_is_recorded_and_the_document_still_built(self):
        collector = main.Collector(FakeKube({
            "applications": urllib.error.URLError("timed out"),
            "ingresses": {"items": [ARGOCD_INGRESS]}}), clock=lambda: NOW,
            fetch_alerts=lambda url, timeout: [WATCHDOG],
            fetch_metrics=no_metrics, fetch_ring0=lambda url, timeout: ring0_doc(),
            fetch_receivers=lambda url, timeout: list(RECEIVERS))
        self.assertFalse(collector.healthy())
        self.assertIsNone(collector.document())
        collector.cycle()
        document = collector.document()
        self.assertEqual("unreachable (timed out)", document["sources"]["applications"]["error"])
        self.assertTrue(document["sources"]["ingresses"]["ok"])
        self.assertEqual("grey", document["verdict"]["colour"])
        self.assertTrue(collector.healthy())

    def test_unhealthy_once_the_refresh_stops(self):
        now = [NOW]
        collector = main.Collector(FakeKube({"applications": {"items": []}, "ingresses": {"items": []}}),
                                   clock=lambda: now[0], fetch_alerts=lambda url, timeout: [WATCHDOG],
            fetch_metrics=no_metrics, fetch_ring0=lambda url, timeout: ring0_doc(),
            fetch_receivers=lambda url, timeout: list(RECEIVERS))
        collector.cycle()
        now[0] += 3 * config.REFRESH_SECONDS + 31
        self.assertFalse(collector.healthy())

    def test_alertmanager_failing_does_not_stop_the_others(self):
        def broken(url, timeout):
            raise urllib.error.URLError("connection refused")
        collector = main.Collector(FakeKube({"applications": {"items": [app("a")]}, "ingresses": {"items": []}}),
                                   clock=lambda: NOW, fetch_alerts=broken, fetch_metrics=no_metrics, fetch_ring0=lambda url, timeout: ring0_doc(),
            fetch_receivers=lambda url, timeout: list(RECEIVERS))
        collector.cycle()
        sources = collector.document()["sources"]
        self.assertEqual((False, True), (sources["alerts"]["ok"], sources["applications"]["ok"]))

    def test_describe_never_returns_a_traceback(self):
        error = urllib.error.HTTPError("https://kubernetes.default.svc/x", 403, "Forbidden", {}, None)
        with error:
            self.assertEqual("HTTP 403 Forbidden", main.describe(error))
        self.assertLessEqual(len(main.describe(ValueError("x" * 1000))), 220)


class Web(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        class Static:
            document_value = None
            healthy_value = False

            def document(self):
                return self.document_value

            def healthy(self):
                return self.healthy_value

        cls.collector = Static()
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), web.make_handler(cls.collector))
        cls.base = f"http://127.0.0.1:{cls.server.server_address[1]}"
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()

    def call(self, path, method="GET"):
        request = urllib.request.Request(self.base + path, method=method,
                                         data=b"{}" if method in ("POST", "PUT", "PATCH") else None)
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                return response.status, dict(response.headers), response.read()
        except urllib.error.HTTPError as error:
            with error:
                return error.code, dict(error.headers), error.read()

    def test_before_the_first_refresh(self):
        self.collector.document_value, self.collector.healthy_value = None, False
        self.assertEqual(503, self.call("/api/state")[0])
        self.assertEqual(503, self.call("/healthz")[0])

    def test_page_state_and_headers(self):
        self.collector.document_value, self.collector.healthy_value = build([app("a")]), True
        status, headers, body = self.call("/")
        self.assertEqual(200, status)
        self.assertIn(b"Operator Board", body)
        self.assertIn("default-src 'none'", headers["Content-Security-Policy"])
        status, headers, body = self.call("/api/state")
        self.assertEqual((200, "green"), (status, json.loads(body)["verdict"]["colour"]))
        self.assertEqual("no-store", headers["Cache-Control"])
        self.assertEqual(200, self.call("/static/board.js")[0])
        self.assertEqual(200, self.call("/healthz")[0])

    def test_nothing_but_reading(self):
        for method in ("POST", "PUT", "PATCH", "DELETE"):
            self.assertEqual(405, self.call("/api/state", method)[0], method)

    def test_no_path_outside_static(self):
        for path in ("/static/../src/main.py", "/static/%2e%2e/src/main.py", "/static/main.py", "/src/main.py"):
            self.assertEqual(404, self.call(path)[0], path)


if __name__ == "__main__":
    unittest.main(verbosity=1)
