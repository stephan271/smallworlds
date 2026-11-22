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


def reading(data=None, error=None, fetched=NOW, last_good=NOW):
    return state.Reading(data=data, error=error, fetched_at=fetched,
                         last_good_at=None if data is None else last_good)


def build(apps=(), ingresses=(ARGOCD_INGRESS,), applications=None, ingress_reading=None):
    return state.build(
        applications or reading({"items": list(apps)}),
        ingress_reading or reading({"items": list(ingresses)}), NOW,
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
        self.assertEqual(["Argo CD Applications"], document["verdict"]["covers"])
        self.assertIn("alerts", document["verdict"]["not_yet"])
        self.assertEqual("2 of 2 settled", document["tiles"][0]["headline"])

    def test_progressing_is_amber_with_a_link_into_argo_cd(self):
        document = build([app("keycloak"), app("nextcloud", health="Progressing")])
        self.assertEqual("amber", document["verdict"]["colour"])
        self.assertEqual("1 thing has not settled", document["verdict"]["headline"])
        reason = document["verdict"]["reasons"][0]
        self.assertTrue(reason["text"].startswith("nextcloud: Synced / Progressing"))
        self.assertEqual("https://deploy.example.org/applications/argocd/nextcloud", reason["link"])
        self.assertEqual("nextcloud", document["applications"][0]["name"], "problems come first")

    def test_nothing_the_board_reads_by_itself_is_red(self):
        """Red comes only from Alertmanager (ADR 0068), which this slice does not read."""
        document = build([app("a", health="Degraded"), app("b", sync="OutOfSync", health="Missing"),
                          app("c", sync="Unknown", health="Unknown")])
        self.assertNotIn("red", colours(document))

    def test_a_source_that_never_answered_is_grey_never_green(self):
        document = build(applications=reading(None, error="HTTP 403 Forbidden"))
        self.assertEqual("grey", document["verdict"]["colour"])
        self.assertEqual("grey", document["tiles"][0]["colour"])
        self.assertEqual("Part of the cluster could not be read", document["verdict"]["headline"])
        self.assertIn("HTTP 403 Forbidden", document["verdict"]["reasons"][0]["text"])
        self.assertIn("Nothing has been read from it yet", document["verdict"]["reasons"][0]["text"])

    def test_a_source_that_stopped_keeps_its_last_answer_greyed_with_its_age(self):
        stale = state.Reading(data={"items": [app("keycloak")]}, error="unreachable (timed out)",
                              fetched_at=NOW, last_good_at=NOW - 600)
        document = build(applications=stale)
        self.assertEqual("grey", document["tiles"][0]["colour"])
        self.assertEqual(["keycloak"], [a["name"] for a in document["applications"]])
        self.assertIn("10 min old", document["verdict"]["reasons"][0]["text"])
        self.assertEqual("grey", document["verdict"]["colour"])

    def test_unread_sources_are_never_green(self):
        document = build(applications=state.Reading(), ingress_reading=state.Reading())
        self.assertEqual("grey", document["verdict"]["colour"])

    def test_amber_outranks_grey(self):
        document = build([app("nextcloud", health="Progressing")],
                         ingress_reading=reading(None, error="HTTP 500"))
        self.assertEqual("amber", document["verdict"]["colour"])
        self.assertEqual(["amber", "grey"], [r["colour"] for r in document["verdict"]["reasons"]])


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
        self.assertEqual({"__init__", "_token", "get"},
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
        self.answers = answers

    def get(self, path):
        answer = self.answers[path.rsplit("/", 1)[-1]]
        if isinstance(answer, Exception):
            raise answer
        return answer


class Collector(unittest.TestCase):
    def test_a_failing_source_is_recorded_and_the_document_still_built(self):
        collector = main.Collector(FakeKube({
            "applications": urllib.error.URLError("timed out"),
            "ingresses": {"items": [ARGOCD_INGRESS]}}), clock=lambda: NOW)
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
                                   clock=lambda: now[0])
        collector.cycle()
        now[0] += 3 * config.REFRESH_SECONDS + 31
        self.assertFalse(collector.healthy())

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
