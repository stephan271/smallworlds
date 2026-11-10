"""Tests for the Canary Follower, against fakes of the Kubernetes API (acting as
the Argo CD controller), GitHub and the clock. No network.

    python3 -m unittest discover -s infrastructure/kubernetes/tenants/canary-follower/tests
"""
import pathlib
import re
import sys
import unittest

TENANT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(TENANT / "src"))

import checks  # noqa: E402
import settings as settings_module  # noqa: E402
from argocd import REFRESH, ArgoCD  # noqa: E402
from follower import NOT_YET, Follower  # noqa: E402
from github import GitHub  # noqa: E402

BASE = "https://github.com/stephan271/smallworlds.git"
OVERLAY = "https://github.com/stephan271/my-canary-installation.git"
APPS = "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications"


class Clock:
    def __init__(self):
        self.now = 0.0

    def __call__(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class FakeArgoKube:
    """Applications as the Argo CD controller keeps them: a refresh annotation
    disappears on the next read, and a sync operation completes on the next
    read with the app at `deploys` (or what the test set)."""

    def __init__(self, sha, names=("smallworlds-root", "keycloak", "gallery"), routed=("gallery",)):
        self.deploys = sha
        self.busy = {}
        self.calls = []
        self.health = {}
        self.apps = {}
        for name in names:
            repo = OVERLAY if name in routed or name == "smallworlds-root" else BASE
            self.apps[name] = {"metadata": {"name": name, "annotations": {}},
                               "spec": {"source": {"repoURL": repo}},
                               "status": {"sync": {"status": "OutOfSync", "revision": "old"},
                                          "health": {"status": "Healthy"}}}
        self.ingresses = {"items": [
            {"metadata": {"labels": {}}, "spec": {"rules": [{"host": "photos.canary.x"}]}},
            {"metadata": {"labels": {checks.ACME_SOLVER_LABEL: "true"}},
             "spec": {"rules": [{"host": "acme.canary.x"}]}},
            {"metadata": {}, "spec": {"rules": [{"host": "files.canary.x"}, {}]}},
        ]}

    def _settle(self, app):
        name = app["metadata"]["name"]
        app["metadata"]["annotations"].pop(REFRESH, None)
        if self.busy.get(name, 0) > 0:
            # An earlier sync still retrying: it ends after `busy` more reads.
            self.busy[name] -= 1
            app["status"]["operationState"] = {"phase": "Running" if self.busy[name] else "Failed"}
            return
        if app.pop("operation", None) is not None:
            revision = OVERLAY_SHA if app["spec"]["source"]["repoURL"] == OVERLAY else self.deploys
            app["status"]["sync"] = {"status": "Synced", "revision": revision}
            app["status"]["health"] = {"status": self.health.get(name, "Healthy")}

    def get(self, path):
        self.calls.append(("GET", path))
        if path == "/apis/networking.k8s.io/v1/ingresses":
            return self.ingresses
        if path == APPS:
            for app in self.apps.values():
                self._settle(app)
            return {"items": list(self.apps.values())}
        app = self.apps[path.rsplit("/", 1)[1]]
        self._settle(app)
        return app

    def merge_patch(self, path, body):
        self.calls.append(("PATCH", path, body))
        app = self.apps[path.rsplit("/", 1)[1]]
        if "metadata" in body:
            app["metadata"]["annotations"].update(body["metadata"]["annotations"])
        if "operation" in body:
            app["operation"] = body["operation"]
        return app


OVERLAY_SHA = "overlay0000000000"


class FakeGitHub:
    def __init__(self, head, statuses=None):
        self.head = head
        self.statuses = dict(statuses or {})
        self.posted = []

    def head_sha(self, branch):
        return self.head

    def status(self, sha):
        return self.statuses.get(sha)

    def set_status(self, sha, state, description):
        self.posted.append((sha, state, description))
        self.statuses[sha] = state


def make(sha="a" * 40, smoke_result=(), quiet_result=(), statuses=None, **kube_args):
    clock = Clock()
    kube = FakeArgoKube(sha, **kube_args)
    gh = FakeGitHub(sha, statuses)
    s = settings_module.Settings(quiet_seconds=1800)
    seen = {}

    def smoke(hosts, attempts, retry):
        seen["hosts"] = hosts
        return list(smoke_result)

    def quiet(url, seconds, interval):
        seen["quiet"] = seconds
        return list(quiet_result)

    follower = Follower(s, gh, ArgoCD(kube, "argocd", "smallworlds-root", clock, clock.sleep),
                        kube, smoke=smoke, quiet=quiet)
    return follower, gh, kube, seen


class FollowerTest(unittest.TestCase):
    def test_new_head_is_followed_and_verified(self):
        follower, gh, kube, seen = make()
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertEqual([s for _, s, _ in gh.posted], ["pending", "success"])
        self.assertIn("converged", outcome.description)
        self.assertIn("smoke 2 hosts", outcome.description)
        self.assertIn("quiet 30 min", outcome.description)
        self.assertIn(NOT_YET, outcome.description, "the status must not claim e2e")
        self.assertEqual(["files.canary.x", "photos.canary.x"], seen["hosts"])

    def test_the_same_head_is_not_verified_twice(self):
        follower, gh, kube, _ = make()
        follower.tick()
        self.assertIsNone(follower.tick())
        self.assertEqual(2, len(gh.posted))

    def test_a_restarted_pod_skips_a_commit_with_a_verdict(self):
        sha = "b" * 40
        follower, gh, kube, _ = make(sha, statuses={sha: "failure"})
        self.assertIsNone(follower.tick())
        self.assertEqual([], gh.posted)
        self.assertFalse(any(c[0] == "PATCH" for c in kube.calls), "nothing may be synced")

    def test_a_restarted_pod_finishes_a_pending_commit(self):
        sha = "c" * 40
        follower, gh, kube, _ = make(sha, statuses={sha: "pending"})
        self.assertEqual("success", follower.tick().state)

    def test_root_is_refreshed_and_synced_before_the_others(self):
        follower, gh, kube, _ = make()
        follower.tick()
        patches = [(c[1].rsplit("/", 1)[1], "operation" in c[2]) for c in kube.calls if c[0] == "PATCH"]
        self.assertEqual(("smallworlds-root", False), patches[0], "root: hard refresh first")
        self.assertEqual(("smallworlds-root", True), patches[1], "root: then a sync")
        self.assertEqual({"gallery", "keycloak"}, {n for n, _ in patches[2:]})
        refresh = next(c[2] for c in kube.calls if c[0] == "PATCH")
        self.assertEqual({"metadata": {"annotations": {REFRESH: "hard"}}}, refresh)
        sync = next(c[2] for c in kube.calls if c[0] == "PATCH" and "operation" in c[2])
        self.assertTrue(sync["operation"]["sync"]["prune"])

    def test_an_app_that_never_heals_fails_the_commit(self):
        follower, gh, kube, _ = make()
        kube.health["keycloak"] = "Degraded"
        outcome = follower.tick()
        self.assertEqual("failure", outcome.state)
        self.assertIn("keycloak: Synced/Degraded", outcome.description)

    def test_main_moving_on_mid_way_is_an_error_not_a_verdict(self):
        follower, gh, kube, _ = make()
        kube.deploys = "d" * 40
        outcome = follower.tick()
        self.assertEqual("error", outcome.state)
        self.assertIn("keycloak@dddddddddddd", outcome.description)
        self.assertNotIn("gallery", outcome.description, "routed apps run the overlay's revision")

    def test_an_app_busy_with_an_earlier_sync_is_synced_once_it_ends(self):
        follower, gh, kube, _ = make()
        kube.busy["keycloak"] = 40
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertIn("1 after their running sync", outcome.steps[0])
        syncs = [c[1].rsplit("/", 1)[1] for c in kube.calls if c[0] == "PATCH" and "operation" in c[2]]
        self.assertEqual("keycloak", syncs[-1], "synced last, after its own sync ended")
        self.assertEqual(1, syncs.count("keycloak"), "never written over the running one")

    def test_an_app_stuck_in_a_sync_fails_the_commit_and_names_it(self):
        follower, gh, kube, _ = make()
        kube.busy["stalwart"] = 10 ** 9
        kube.apps["stalwart"] = {"metadata": {"name": "stalwart", "annotations": {}},
                                 "spec": {"source": {"repoURL": BASE}},
                                 "status": {"sync": {"status": "OutOfSync"}, "health": {"status": "Healthy"}}}
        outcome = follower.tick()
        self.assertEqual("failure", outcome.state, "Ring 0's state, not a follower error")
        self.assertIn("stalwart: still running an earlier sync", outcome.description)

    def test_smoke_failure_names_the_host(self):
        follower, gh, kube, _ = make(smoke_result=["photos.canary.x: HTTP 502"])
        outcome = follower.tick()
        self.assertEqual("failure", outcome.state)
        self.assertIn("photos.canary.x: HTTP 502", outcome.description)

    def test_a_critical_alert_fails_the_commit(self):
        follower, gh, kube, _ = make(quiet_result=["CNPGBackupStale"])
        outcome = follower.tick()
        self.assertEqual("failure", outcome.state)
        self.assertIn("CNPGBackupStale", outcome.description)

    def test_a_follower_crash_is_reported_as_error(self):
        follower, gh, kube, _ = make()
        del kube.apps["smallworlds-root"]
        outcome = follower.tick()
        self.assertEqual("error", outcome.state)
        self.assertIn("Follower error", outcome.description)


class ChecksTest(unittest.TestCase):
    def test_smoke_retries_only_failed_hosts(self):
        answers = {"a": [None], "b": ["HTTP 503", None]}
        asked = []

        def probe(url):
            host = url.split("/")[2]
            asked.append(host)
            return answers[host].pop(0)

        self.assertEqual([], checks.smoke(["a", "b"], 3, 1, probe=probe, sleep=lambda s: None))
        self.assertEqual(["a", "b", "b"], asked)

    def test_smoke_reports_what_stays_broken(self):
        failed = checks.smoke(["a"], 2, 1, probe=lambda url: "HTTP 502", sleep=lambda s: None)
        self.assertEqual(["a: HTTP 502"], failed)

    def test_quiet_waits_the_whole_period(self):
        clock = Clock()
        polls = []
        result = checks.quiet("http://am", 1800, 60, fetch=lambda url: polls.append(1) or [],
                              clock=clock, sleep=clock.sleep)
        self.assertEqual([], result)
        self.assertGreaterEqual(clock.now, 1800)
        self.assertEqual(31, len(polls))

    def test_quiet_stops_at_the_first_critical_alert(self):
        clock = Clock()
        answers = [[], [], ["KubePodCrashLooping"]]
        result = checks.quiet("http://am", 1800, 60, fetch=lambda url: answers.pop(0),
                              clock=clock, sleep=clock.sleep)
        self.assertEqual(["KubePodCrashLooping"], result)
        self.assertEqual(120, clock.now)

    def test_an_unreadable_alertmanager_is_not_quiet(self):
        def fetch(url):
            raise OSError("refused")
        self.assertEqual(["Alertmanager unreadable (OSError)"],
                         checks.quiet("http://am", 60, 60, fetch=fetch))


class GitHubTest(unittest.TestCase):
    def test_status_is_posted_with_context_and_trimmed(self):
        import io
        import json
        sent = []

        class Response(io.BytesIO):
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

        def opener(request, timeout):
            sent.append(request)
            return Response(json.dumps({}).encode())

        gh = GitHub("https://api.github.com", "o/r", "tok", "ring0/verified", opener=opener)
        gh.set_status("f" * 40, "failure", "x" * 300)
        body = json.loads(sent[0].data)
        self.assertEqual("ring0/verified", body["context"])
        self.assertLessEqual(len(body["description"]), 140)
        self.assertTrue(sent[0].full_url.endswith("/repos/o/r/statuses/" + "f" * 40))
        self.assertEqual("Bearer tok", sent[0].get_header("Authorization"))

    def test_without_a_token_nothing_is_posted(self):
        def opener(request, timeout):
            raise AssertionError("no request expected")
        GitHub("https://api.github.com", "o/r", "", "ring0/verified", opener=opener) \
            .set_status("f" * 40, "success", "ok")


class PackagingTest(unittest.TestCase):
    def test_every_source_file_is_in_the_configmap(self):
        listed = set(re.findall(r"src/(\w+\.py)", (TENANT / "kustomization.yaml").read_text()))
        present = {p.name for p in (TENANT / "src").glob("*.py")}
        self.assertEqual(present, listed)

    def test_settings_read_integers_from_the_environment(self):
        s = settings_module.from_environment({"QUIET_SECONDS": "60", "BRANCH": "x"})
        self.assertEqual(60, s.quiet_seconds)
        self.assertEqual("x", s.branch)


if __name__ == "__main__":
    unittest.main()
