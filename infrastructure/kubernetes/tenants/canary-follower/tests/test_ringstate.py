"""Tests for ring0.json (src/ringstate.py), against fakes of GitHub and the
Kubernetes API. No network.

    python3 -m unittest discover -s infrastructure/kubernetes/tenants/canary-follower/tests
"""
import json
import pathlib
import sys
import unittest

TENANT = pathlib.Path(__file__).resolve().parent.parent
REPO = TENANT.parents[3]
sys.path.insert(0, str(TENANT / "src"))

import settings as settings_module  # noqa: E402
import ringstate  # noqa: E402
from ringstate import ROOT_RESOURCES, RingState, application, cards_for  # noqa: E402

BASE = "https://github.com/stephan271/smallworlds.git"
HEAD = "h" * 40
OLDER = "o" * 40


def commit(sha, message, login="renovate[bot]"):
    return {"sha": sha, "author": {"login": login},
            "commit": {"message": message, "author": {"name": "x"},
                       "committer": {"date": "2026-11-14T08:00:00Z"}}}


class FakeGitHub:
    def __init__(self):
        self.history = [commit(HEAD, "Update gallery (#58)"),
                        commit(OLDER, "Merge pull request #71 from someone/branch\n\nStop a false alarm",
                               "stephan271")]
        self.paths = {HEAD: ["infrastructure/kubernetes/tenants/gallery/kustomization.yaml"],
                      OLDER: ["infrastructure/kubernetes/apps/alertmanager-config.yaml", "README.md"]}
        self.verdicts = {HEAD: {"state": "success", "description": "Ring 0: quiet 20 min",
                                "date": "2026-11-14T09:00:00Z"},
                         OLDER: {"state": "failure", "description": "Critical alert: X",
                                 "date": "2026-11-14T07:00:00Z"}}
        self.pulls = []
        self.files = {}
        self.calls = []
        self.writes = []
        self.branch = True
        self.orphans = []

    def commits(self, branch, count):
        return self.history

    def head_sha(self, branch):
        return self.history[0]["sha"]

    def changed_paths(self, sha):
        self.calls.append(("paths", sha))
        return self.paths[sha]

    def verdict(self, sha):
        self.calls.append(("verdict", sha))
        return self.verdicts.get(sha)

    def open_pulls(self, base):
        return self.pulls

    def pull_files(self, number):
        self.calls.append(("files", number))
        return [{"filename": f} for f in self.files[number]]

    def file_sha(self, path, branch):
        return "blob0" if self.branch else None

    def branch_exists(self, branch):
        return self.branch

    def put_file(self, path, branch, text, message, sha):
        self.writes.append((path, branch, json.loads(text), message, sha))
        return f"blob{len(self.writes)}"

    def create_orphan_branch(self, branch, files, message):
        self.orphans.append((branch, sorted(files)))
        self.branch = True


def argo_app(name, chart=None, repo=BASE, health="Healthy", phase="Succeeded", message=None,
             images=("img:1",), wave="1"):
    source = {"repoURL": "https://charts.example" if chart else repo, "targetRevision": "1.2.3" if chart else "HEAD"}
    if chart:
        source["chart"] = chart
    return {"metadata": {"name": name, "annotations": {"argocd.argoproj.io/sync-wave": wave}},
            "spec": {"source": source},
            "status": {"health": {"status": health}, "sync": {"status": "Synced", "revision": "r" * 40},
                       "summary": {"images": list(images)},
                       "operationState": {"phase": phase, "finishedAt": "2026-11-14T08:10:00Z",
                                          "message": message}}}


class FakeKube:
    def __init__(self, apps):
        self.apps = apps

    def get(self, path):
        assert path.endswith("/applications"), path
        return {"items": self.apps}


class Follower:
    def __init__(self):
        self.current = None
        self.last_action = None
        self.queue = None
        self.errors = {}


class Clock:
    def __init__(self):
        self.now = 1_800_000_000.0

    def __call__(self):
        return self.now


def make(apps=None):
    gh = FakeGitHub()
    kube = FakeKube(apps if apps is not None else [argo_app("immich"), argo_app("traefik", chart="traefik", wave="-10")])
    follower = Follower()
    clock = Clock()
    s = settings_module.Settings()
    return RingState(s, gh, kube, follower, wallclock=clock), gh, follower, clock


class CardsTest(unittest.TestCase):
    def test_paths_name_the_application_that_runs_them(self):
        self.assertEqual(["immich"], cards_for(["infrastructure/kubernetes/tenants/gallery/x.yaml"]))
        self.assertEqual(["traefik"], cards_for(["infrastructure/kubernetes/apps/traefik.yaml"]))
        self.assertEqual(["smallworlds-root"], cards_for(["infrastructure/kubernetes/apps/update-alerts.yaml",
                                                          "infrastructure/kubernetes/bases/garage-init-job/job.yaml"]))
        self.assertEqual(["canary-follower"], cards_for(["e2e-tests/tests/02-nextcloud.spec.ts",
                                                         "admin-tools/canary/seed.py"]))
        self.assertEqual(["repository"], cards_for(["README.md", "admin-tools/bump-version.sh"]))

    def test_root_resources_are_exactly_the_files_in_apps_that_are_no_application(self):
        apps = REPO / "infrastructure/kubernetes/apps"
        not_applications = set()
        for path in apps.glob("*.yaml"):
            kinds = {line.split(":", 1)[1].strip() for line in path.read_text().splitlines()
                     if line.startswith("kind:")}
            if "Application" not in kinds:
                not_applications.add(path.stem)
        self.assertEqual(not_applications, set(ROOT_RESOURCES))


class ApplicationTest(unittest.TestCase):
    def test_a_chart_application_reports_its_chart_version(self):
        app = application(argo_app("traefik", chart="traefik", wave="-10"), BASE)
        self.assertEqual(("chart", "traefik", "1.2.3", None, -10, "platform"),
                         (app["source"], app["chart"], app["version"], app["revision"], app["wave"], app["group"]))

    def test_an_overlay_application_reports_its_revision_and_images(self):
        app = application(argo_app("immich", repo="https://github.com/x/overlay.git", images=("b:2", "a:1")), BASE)
        self.assertEqual(("overlay", "r" * 40, ["a:1", "b:2"], "apps"),
                         (app["source"], app["revision"], app["images"], app["group"]))
        self.assertIsNone(app["operation"]["message"], "a successful sync's message is noise")

    def test_a_failed_sync_message_is_kept_and_cut(self):
        app = application(argo_app("forgejo", phase="Failed", message="x" * 1000), BASE)
        self.assertEqual(300, len(app["operation"]["message"]))

    def test_machinery_is_its_own_group(self):
        self.assertEqual("machinery", application(argo_app("canary-follower"), BASE)["group"])


class SnapshotTest(unittest.TestCase):
    def test_commits_carry_cards_verdicts_and_their_pull_request(self):
        state, gh, _, _ = make()
        doc = state.snapshot()
        head, older = doc["commits"]
        self.assertEqual((58, ["immich"], "success"), (head["pull"], head["cards"], head["verdict"]["state"]))
        self.assertEqual((71, "Stop a false alarm", ["repository", "smallworlds-root"]),
                         (older["pull"], older["title"], older["cards"]))
        self.assertEqual({"commit": HEAD, "state": "success", "description": "Ring 0: quiet 20 min",
                          "date": "2026-11-14T09:00:00Z"}, doc["head"])
        self.assertEqual(["traefik", "immich"], [a["name"] for a in doc["applications"]], "by sync wave")

    def test_final_verdicts_and_changed_files_are_read_once(self):
        state, gh, _, _ = make()
        state.snapshot()
        gh.calls.clear()
        state.snapshot()
        self.assertEqual([], gh.calls)

    def test_a_pending_verdict_is_read_again(self):
        state, gh, _, _ = make()
        gh.verdicts[HEAD] = {"state": "pending", "description": "", "date": "2026-11-14T09:00:00Z"}
        state.snapshot()
        gh.calls.clear()
        state.snapshot()
        self.assertEqual([("verdict", HEAD)], gh.calls)

    def test_pulls_are_queued_rejected_or_neither(self):
        state, gh, _, _ = make()
        gh.pulls = [
            {"number": 57, "title": "Update nextcloud", "user": {"login": "renovate[bot]"},
             "created_at": "c", "head": {"sha": "p57"}, "labels": [{"name": "ring0-queue"}]},
            {"number": 63, "title": "Update rclone", "user": {"login": "renovate[bot]"},
             "created_at": "c", "head": {"sha": "p63"}, "labels": []},
            {"number": 65, "title": "Update kps", "user": {"login": "renovate[bot]"}, "created_at": "c",
             "head": {"sha": "p65"}, "labels": [{"name": "ring0-queue"}, {"name": "ring0-rejected-abc"}]},
        ]
        gh.files = {57: ["infrastructure/kubernetes/tenants/nextcloud/k.yaml"],
                    63: ["infrastructure/kubernetes/tenants/backup-replicator/k.yaml"],
                    65: ["infrastructure/kubernetes/apps/kube-prometheus-stack.yaml"]}
        doc = state.snapshot()
        self.assertEqual([(57, True, False), (63, False, False), (65, True, True)],
                         [(p["number"], p["queued"], p["rejected"]) for p in doc["pulls"]])
        self.assertEqual([57], [q["number"] for q in doc["now"]["queued"]], "a rejected update is not on its way")


class NowTest(unittest.TestCase):
    def test_green_with_nothing_queued_is_up_to_date(self):
        state, _, _, _ = make()
        self.assertEqual("up-to-date", state.snapshot()["now"]["state"])

    def test_a_running_verification_is_updating_with_its_step_and_pull(self):
        state, _, follower, _ = make()
        follower.current = {"commit": HEAD, "step": "checks", "step_since": "s", "started": "t", "done": []}
        now = state.snapshot()["now"]
        self.assertEqual(("updating", "checks", "s", "t"), (now["state"], now["step"], now["step_since"], now["started"]))
        self.assertEqual({"number": 58, "title": "Update gallery (#58)", "cards": ["immich"]}, now["pull"])

    def test_a_rejected_head_is_stuck(self):
        state, gh, _, _ = make()
        gh.verdicts[HEAD] = {"state": "failure", "description": "Smoke failed", "date": "d"}
        now = state.snapshot()["now"]
        self.assertEqual(("stuck", {"condition": "S1", "since": "d", "summary": "Smoke failed"}),
                         (now["state"], now["stuck"]))

    def test_an_error_is_retried_until_its_retries_are_used_up(self):
        state, gh, follower, _ = make()
        gh.verdicts[HEAD] = {"state": "error", "description": "Checks could not run", "date": "d"}
        follower.errors[HEAD] = (1, 0.0)
        self.assertEqual(("updating", "retry"), tuple(state.snapshot()["now"][k] for k in ("state", "step")))
        follower.errors[HEAD] = (4, 0.0)
        self.assertEqual("S2", state.snapshot()["now"]["stuck"]["condition"])


class PublishTest(unittest.TestCase):
    def test_written_once_then_only_on_a_change_or_the_heartbeat(self):
        state, gh, follower, clock = make()
        self.assertTrue(state.publish())
        path, branch, doc, message, sha = gh.writes[0]
        self.assertEqual(("ring0.json", "ring-state", "blob0", 1), (path, branch, sha, doc["schema"]))
        self.assertIn("up-to-date", message)
        clock.now += 300
        self.assertFalse(state.publish(), "only the timestamp changed")
        follower.current = {"commit": HEAD, "step": "sync", "step_since": "s", "started": "t", "done": []}
        self.assertTrue(state.publish())
        self.assertEqual("blob1", gh.writes[1][4], "the next write names the blob it replaces")
        clock.now += 3600
        self.assertTrue(state.publish(), "the heartbeat")

    def test_the_branch_is_created_without_history_when_missing(self):
        state, gh, _, _ = make()
        gh.branch = False
        state.publish()
        self.assertEqual([("ring-state", ["README.md", "ring0.json"])], gh.orphans)
        self.assertEqual([], gh.writes, "the first commit already holds the file")

    def test_a_failed_publish_does_not_end_the_thread(self):
        state, gh, _, _ = make()
        slept = []

        def boom():
            raise OSError("GitHub away")
        state.publish = boom

        def sleep(seconds):
            slept.append(seconds)
            if len(slept) == 2:
                raise SystemExit
        with self.assertRaises(SystemExit):
            state.run(sleep=sleep)
        self.assertEqual(2, len(slept))


class FollowerStepTest(unittest.TestCase):
    def test_the_follower_reports_each_step_and_clears_it_after(self):
        import test_follower
        follower, gh, kube, seen = test_follower.make()
        steps = []
        original = follower._quiet

        def quiet(*args):
            steps.append(dict(follower.current))
            return original(*args)
        follower._quiet = quiet
        follower.tick()
        self.assertEqual("quiet", steps[0]["step"])
        self.assertIn("converged", steps[0]["done"])
        self.assertIsNone(follower.current)


if __name__ == "__main__":
    unittest.main()
