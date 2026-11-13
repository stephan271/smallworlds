"""Tests for the Ring 0 Steward's half of the follower: incidents, their evidence,
and the merge queue's rules for Steward PRs (design-per-app-updates.md §5).
Fakes only, no network.

    python3 -m unittest discover -s infrastructure/kubernetes/tenants/canary-follower/tests
"""
import datetime
import json
import pathlib
import sys
import types
import unittest

TENANT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(TENANT / "src"))
sys.path.insert(0, str(TENANT / "tests"))

import evidence  # noqa: E402
import settings as settings_module  # noqa: E402
from follower import Outcome  # noqa: E402
from github import GitHub  # noqa: E402
from incidents import NEEDS_PERSON, REJECTED_UPDATE, REQUESTED, STUCK, WORKING, Incidents  # noqa: E402
from main import metrics  # noqa: E402
from mergequeue import MergeQueue  # noqa: E402
from test_automation import RENOVATE, FakeRepo, pull  # noqa: E402
from test_follower import make  # noqa: E402

SHA = "a" * 40
STEWARD = "smallworlds-steward[bot]"


class Now:
    def __init__(self, t=1_000_000.0):
        self.t = t

    def __call__(self):
        return self.t


def iso(t):
    return datetime.datetime.fromtimestamp(t, datetime.UTC).isoformat().replace("+00:00", "Z")


class FakeOverlay:
    """Issues of the private Ring 0 overlay repository."""

    def __init__(self, now):
        self.now = now
        self.items = {}
        self.next = 1

    def issues(self, label, state="open"):
        return [json.loads(json.dumps(i)) for i in self.items.values()
                if i["state"] == state and label in {l["name"] for l in i["labels"]}]

    def create_issue(self, title, body, labels):
        number = self.next
        self.next += 1
        self.items[number] = {"number": number, "title": title, "body": body, "state": "open",
                              "labels": [{"name": l} for l in labels], "comments": []}
        return number

    def edit_issue(self, number, **fields):
        self.items[number].update(fields)

    def add_label(self, number, name):
        if name not in self.labels(number):
            self.items[number]["labels"].append({"name": name})

    def remove_label(self, number, name):
        self.items[number]["labels"] = [l for l in self.items[number]["labels"] if l["name"] != name]

    def comment(self, number, body):
        self.items[number]["comments"].append({"body": body, "created_at": iso(self.now())})

    def comments(self, number):
        return list(self.items[number]["comments"])

    def labels(self, number):
        return {l["name"] for l in self.items[number]["labels"]}


class FakeBase:
    def __init__(self, head=SHA, pulls=None):
        self.head = head
        self.pulls = pulls or {}  # sha -> [pull]

    def head_sha(self, branch):
        return self.head

    def pulls_of(self, sha):
        return self.pulls.get(sha, [])

    def pull_files(self, number):
        return [{"filename": "infrastructure/kubernetes/tenants/immich/kustomization.yaml",
                 "patch": "+  newTag: v5.11.0"}]

    def changed_paths(self, sha):
        return ["infrastructure/kubernetes/tenants/immich/kustomization.yaml"]

    def pull(self, number):
        return {"number": number, "title": f"PR {number}", "html_url": f"https://x/{number}"}


class FakeQueue:
    def __init__(self, now):
        self.rejection = None
        self.candidates = 0
        self.last_merge = now()
        self.failing = {}
        self.waiting = None

    def take_rejection(self):
        rejection, self.rejection = self.rejection, None
        return rejection


class FakeEvidence:
    def __init__(self):
        self.collected = 0

    def cluster(self):
        self.collected += 1
        return "### Argo CD Applications\n\nimmich: Synced/Degraded"


def settings(**overrides):
    values = {"steward": "true", "incidents": "true"}
    values.update(overrides)
    return settings_module.Settings(**values)


class Ring0:
    """The incidents with everything around them faked."""

    def __init__(self, steward="true", **overrides):
        self.now = Now()
        self.overlay = FakeOverlay(self.now)
        self.base = FakeBase()
        self.queue = FakeQueue(self.now)
        self.evidence = FakeEvidence()
        self.incidents = Incidents(settings(steward=steward, **overrides), self.base, self.overlay,
                                   self.evidence, now=self.now)
        self.follower = types.SimpleNamespace(github=self.base, last_outcome=None, errors={},
                                              queue=self.queue)

    def tick(self, verdict=None, outcome=None):
        if outcome is not None:
            self.follower.last_outcome = outcome
        if verdict is not None:
            self.follower.last_outcome = verdict
        self.incidents.refresh()
        self.incidents.review(self.follower, verdict)

    def only(self):
        [issue] = [i for i in self.overlay.items.values() if i["state"] == "open"]
        return issue

    def workflow_runs(self, result):
        """What the Steward's workflow does: swap the labels, work, report, finish."""
        number = self.only()["number"]
        self.overlay.remove_label(number, REQUESTED)
        self.overlay.add_label(number, WORKING)
        self.now.t += 60
        self.tick()  # the follower sees it working
        self.now.t += 600
        self.overlay.comment(number, f"Did things.\n\n<!-- steward-done result={result} -->")
        self.overlay.remove_label(number, WORKING)
        self.tick()


def failed(sha=SHA, restore=(), state="failure"):
    return Outcome(sha, state, "Checks failed: e2e 1 failed", ["converged", "smoke 9 hosts"],
                   restore_only=list(restore))


class IncidentTest(unittest.TestCase):
    def test_a_rejected_head_that_stays_opens_one_incident_with_evidence(self):
        ring0 = Ring0()
        ring0.base.pulls[SHA] = [pull(58, "Update gallery", merged_at="x")]
        ring0.tick(failed())
        ring0.tick()
        issue = ring0.only()
        self.assertEqual(1, len(ring0.overlay.items), "one incident, however often it is seen")
        self.assertIn(f"S1:{SHA[:12]}", issue["body"])
        self.assertIn("immich: Synced/Degraded", issue["body"])
        self.assertIn("newTag: v5.11.0", issue["body"])
        self.assertIn(STUCK, ring0.overlay.labels(issue["number"]))

    def test_the_steward_is_asked_and_the_follower_pauses_until_it_is_done(self):
        ring0 = Ring0()
        ring0.tick(failed())
        number = ring0.only()["number"]
        self.assertIn(REQUESTED, ring0.overlay.labels(number))
        ring0.incidents.refresh()
        self.assertTrue(ring0.incidents.paused(), "nothing is verified while the Steward is about to act")

    def test_without_the_steward_incidents_are_kept_but_nobody_is_asked(self):
        ring0 = Ring0(steward="false")
        ring0.tick(failed())
        self.assertNotIn(REQUESTED, ring0.overlay.labels(ring0.only()["number"]))
        self.assertFalse(ring0.incidents.paused())

    def test_a_restore_tells_the_operator_at_once_and_the_steward_still_looks(self):
        ring0 = Ring0()
        ring0.tick(failed(restore=["gallery"]))
        labels = ring0.overlay.labels(ring0.only()["number"])
        self.assertIn(NEEDS_PERSON, labels)
        self.assertIn(REQUESTED, labels)
        self.assertIn("gallery", ring0.only()["body"])

    def test_after_the_steward_acts_the_head_is_verified_again(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.workflow_runs("acted")
        self.assertEqual(SHA, ring0.incidents.take_reverify())
        ring0.incidents.refresh()
        self.assertFalse(ring0.incidents.paused())

    def test_a_run_that_finishes_between_two_polls_is_still_read(self):
        ring0 = Ring0()
        ring0.tick(failed())
        number = ring0.only()["number"]
        ring0.overlay.remove_label(number, REQUESTED)
        ring0.now.t += 120
        ring0.overlay.comment(number, "Quick.\n\n<!-- steward-done result=acted -->")
        ring0.tick()
        self.assertIn("phase=verifying", ring0.only()["body"])
        self.assertEqual(SHA, ring0.incidents.take_reverify())

    def test_a_superseded_verdict_is_not_an_attempt(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.workflow_runs("acted")
        ring0.tick(verdict=Outcome(SHA, "error", "Superseded before verified", superseded=True))
        self.assertIn("attempts=0", ring0.only()["body"])

    def test_a_green_verdict_after_an_attempt_resolves_the_incident(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.workflow_runs("acted")
        ring0.tick(verdict=Outcome(SHA, "success", "Ring 0: converged"))
        issue = ring0.overlay.items[1]
        self.assertEqual("closed", issue["state"])
        self.assertIn("Resolved", issue["comments"][-1]["body"])
        self.assertNotIn(REQUESTED, ring0.overlay.labels(1))

    def test_a_failed_attempt_adds_evidence_and_asks_again(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.workflow_runs("acted")
        ring0.tick(verdict=failed())
        issue = ring0.only()
        self.assertIn("attempts=1", issue["body"])
        self.assertIn("Attempt 1 of 3 failed", issue["comments"][-1]["body"])
        self.assertIn(REQUESTED, ring0.overlay.labels(issue["number"]))
        self.assertEqual(2, ring0.evidence.collected, "fresh evidence for the next attempt")

    def test_three_failed_attempts_hand_it_to_a_person(self):
        ring0 = Ring0()
        ring0.tick(failed())
        for _ in range(3):
            ring0.workflow_runs("acted")
            ring0.tick(verdict=failed())
        issue = ring0.only()
        self.assertIn(NEEDS_PERSON, ring0.overlay.labels(issue["number"]))
        self.assertNotIn(REQUESTED, ring0.overlay.labels(issue["number"]))
        self.assertIn("3 attempts", issue["comments"][-1]["body"])
        self.assertIn(f'smallworlds_ring0_needs_person{{issue="{issue["number"]}"',
                      metrics(None, ring0.incidents))

    def test_a_run_without_a_result_is_not_an_attempt_and_is_retried_later(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.workflow_runs("not-run")
        issue = ring0.only()
        self.assertIn("attempts=0", issue["body"])
        self.assertIn("missed=1", issue["body"])
        self.assertNotIn(REQUESTED, ring0.overlay.labels(issue["number"]))
        ring0.now.t += 3 * 3600 + 1
        ring0.tick()
        self.assertIn(REQUESTED, ring0.overlay.labels(issue["number"]), "asked again after the delay")

    def test_a_workflow_that_never_starts_does_not_pause_ring0_forever(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.now.t += 3601
        ring0.tick()
        ring0.incidents.refresh()
        self.assertFalse(ring0.incidents.paused())
        self.assertIn("did not start", ring0.only()["comments"][-1]["body"])

    def test_a_refused_credential_goes_to_a_person_at_once(self):
        ring0 = Ring0()
        ring0.tick(failed())
        ring0.workflow_runs("credential")
        self.assertIn(NEEDS_PERSON, ring0.overlay.labels(ring0.only()["number"]))

    def test_a_reverted_update_opens_a_pr_only_incident_and_ring0_keeps_moving(self):
        ring0 = Ring0()
        renovate = pull(58, "Update gallery", merged_at="x")
        ring0.queue.rejection = {"sha": SHA, "pull": renovate, "reverted": True,
                                 "fingerprint": "abc1234567", "description": "Checks failed"}
        ring0.base.head = "b" * 40  # the revert
        ring0.tick(failed())
        issue = ring0.only()
        self.assertIn("R:abc1234567", issue["body"])
        self.assertIn(REJECTED_UPDATE, ring0.overlay.labels(issue["number"]))
        ring0.incidents.refresh()
        self.assertFalse(ring0.incidents.paused(), "a PR-only incident never pauses Ring 0")

    def test_a_steward_fix_that_passes_closes_its_incident(self):
        ring0 = Ring0()
        ring0.queue.rejection = {"sha": SHA, "pull": pull(58, "Update gallery", merged_at="x"),
                                 "reverted": True, "fingerprint": "abc1234567", "description": "x"}
        ring0.base.head = "b" * 40
        ring0.tick(failed())
        number = ring0.only()["number"]
        ring0.workflow_runs("pr")
        self.assertIn("phase=waiting-pr", ring0.only()["body"])
        fix = "c" * 40
        ring0.base.head = fix
        ring0.base.pulls[fix] = [{"labels": [{"name": "steward"}], "merged_at": "x",
                                  "body": f"Make Gallery 5.11 work.\n\nRing0-Incident: #{number}\n"}]
        ring0.tick(verdict=Outcome(fix, "success", "Ring 0: ok"))
        self.assertEqual("closed", ring0.overlay.items[number]["state"])

    def test_the_steward_holding_an_update_back_closes_it(self):
        ring0 = Ring0()
        ring0.queue.rejection = {"sha": SHA, "pull": pull(58, "Update gallery", merged_at="x"),
                                 "reverted": True, "fingerprint": "abc1234567", "description": "x"}
        ring0.base.head = "b" * 40
        ring0.tick(failed())
        ring0.workflow_runs("hold")
        self.assertEqual("closed", ring0.overlay.items[1]["state"])

    def test_errors_become_an_incident_only_after_their_retries(self):
        ring0 = Ring0()
        error = failed(state="error")
        ring0.follower.errors[SHA] = (3, 0.0)
        ring0.tick(error)
        self.assertEqual({}, ring0.overlay.items)
        ring0.follower.errors[SHA] = (4, 0.0)
        ring0.tick(error)
        self.assertIn(f"S2:{SHA[:12]}", ring0.only()["body"])

    def test_a_queued_update_red_for_twelve_hours_is_an_incident(self):
        ring0 = Ring0()
        ring0.queue.failing = {57: (ring0.now() - 11 * 3600, "red (a PR check failed)")}
        ring0.tick()
        self.assertEqual({}, ring0.overlay.items)
        ring0.queue.failing = {57: (ring0.now() - 13 * 3600, "red (a PR check failed)")}
        ring0.tick()
        self.assertIn("S4:#57", ring0.only()["body"])
        ring0.queue.failing = {}
        ring0.tick()
        self.assertEqual("closed", ring0.overlay.items[1]["state"])

    def test_a_queue_that_merges_nothing_for_a_day_is_an_incident(self):
        ring0 = Ring0()
        ring0.queue.candidates = 2
        ring0.queue.last_merge = ring0.now() - 86401
        ring0.tick()
        self.assertIn("S3:queue", ring0.only()["body"])
        ring0.queue.last_merge = ring0.now() + 1
        ring0.tick()
        self.assertEqual("closed", ring0.overlay.items[1]["state"])

    def test_the_daily_budget_limits_how_often_the_steward_is_asked(self):
        ring0 = Ring0(steward_runs_per_day=1)
        ring0.queue.failing = {57: (0, "red"), 58: (0, "red")}
        ring0.tick()
        asked = [n for n in ring0.overlay.items if REQUESTED in ring0.overlay.labels(n)]
        self.assertEqual(1, len(asked))


class MergeQueueStewardTest(unittest.TestCase):
    def queue(self, repo):
        return MergeQueue(repo, "main", "ring0-queue", RENOVATE, steward=STEWARD, owner="stephan271",
                          protected=("e2e-tests/", ".github/"))

    def test_a_queued_steward_pr_is_merged_like_an_update(self):
        repo = FakeRepo([pull(60, "Fix Gallery's probe", labels=("ring0-queue", "steward"), user=STEWARD)],
                        green={"head60"})
        self.assertEqual(60, self.queue(repo).advance())

    def test_without_the_steward_label_or_from_someone_else_it_is_not(self):
        repo = FakeRepo([pull(60, "a", labels=("ring0-queue",), user=STEWARD),
                         pull(61, "b", labels=("ring0-queue", "steward"), user="someone")],
                        green={"head60", "head61"})
        self.assertIsNone(self.queue(repo).advance())

    def test_a_steward_pr_on_a_protected_path_waits_for_the_owners_approval(self):
        repo = FakeRepo([pull(60, "Loosen a test", labels=("ring0-queue", "steward"), user=STEWARD)],
                        files={60: [{"filename": "e2e-tests/tests/03-immich.spec.ts", "patch": "+x"}]},
                        green={"head60"})
        q = self.queue(repo)
        self.assertIsNone(q.advance())
        repo.approvals.add((60, "stephan271", "head60"))
        self.assertEqual(60, q.advance())

    def test_while_ring0_is_stuck_only_the_stewards_fix_is_merged(self):
        repo = FakeRepo([pull(41, "Update cert-manager"),
                         pull(60, "Fix forward", labels=("ring0-queue", "steward"), user=STEWARD)],
                        green={"head41", "head60"})
        self.assertEqual(60, self.queue(repo).advance_fix())
        self.assertEqual([(60, "head60")], repo.merged)

    def test_red_and_conflicting_updates_are_remembered_with_their_start(self):
        now = Now()
        repo = FakeRepo([pull(41, "a"), pull(42, "b"), pull(43, "c")],
                        mergeable={42: False})
        repo.red.add("head41")
        q = MergeQueue(repo, "main", "ring0-queue", RENOVATE, now=now)
        q.advance()
        self.assertEqual({41, 42}, set(q.failing), "a waiting update (43) is not stuck")
        started = q.failing[41][0]
        now.t += 3600
        q.advance()
        self.assertEqual(started, q.failing[41][0], "the clock is not restarted")
        repo.red.clear()
        repo.green.add("head41")
        q.advance()
        self.assertNotIn(41, q.failing)

    def test_a_rejection_is_kept_for_the_incidents(self):
        repo = FakeRepo(from_pull=pull(41, "Update cloudnative-pg", merged_at="x"))
        q = self.queue(repo)
        q.reject(Outcome(SHA, "failure", "Checks failed", restore_only=[]), head=SHA)
        rejection = q.take_rejection()
        self.assertTrue(rejection["reverted"])
        self.assertEqual(41, rejection["pull"]["number"])
        self.assertIsNone(q.take_rejection())


class FakeIncidents:
    def __init__(self, paused=False, reverify=None, cluster=False):
        self._paused = paused
        self._reverify = reverify
        self.cluster = cluster
        self.reviewed = []

    def refresh(self):
        pass

    def paused(self):
        return self._paused

    def take_reverify(self):
        sha, self._reverify = self._reverify, None
        return sha

    def has_open_cluster(self):
        return self.cluster

    def review(self, follower, outcome):
        self.reviewed.append(outcome)


class FollowerTest(unittest.TestCase):
    def test_nothing_is_verified_while_the_steward_works(self):
        follower, gh, kube, _ = make()
        follower.incidents = FakeIncidents(paused=True)
        self.assertIsNone(follower.tick())
        self.assertEqual([], gh.posted)
        self.assertIn("paused", follower.last_action)

    def test_a_head_with_a_verdict_is_verified_again_when_asked(self):
        follower, gh, kube, _ = make(statuses={SHA: "failure"})
        follower.incidents = FakeIncidents(reverify=SHA)
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertEqual([outcome], follower.incidents.reviewed)

    def test_a_stuck_red_head_merges_only_the_stewards_fix(self):
        follower, gh, kube, _ = make(statuses={SHA: "failure"})
        calls = []
        follower.queue = types.SimpleNamespace(advance=lambda: calls.append("advance"),
                                               advance_fix=lambda: calls.append("fix"))
        follower.incidents = FakeIncidents(cluster=True)
        follower.tick()
        follower.tick()
        self.assertEqual(["fix", "fix"], calls, "updates wait; the fix is looked for on every tick")


class EvidenceTest(unittest.TestCase):
    def test_obvious_secrets_are_masked(self):
        text = evidence.mask("token=ghp_abcdefghijklmnopqrstuvwx1234 password: hunter2hunter2\n"
                             "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig\n"
                             "s3://GK0123456789abcdef01234567 https://user:pa55word@host/x")
        for secret in ("ghp_abcdefghijklmnopqrstuvwx1234", "hunter2hunter2", "eyJhbGciOiJIUzI1NiJ9",
                       "GK0123456789abcdef01234567", "pa55word"):
            self.assertNotIn(secret, text)

    def test_the_bundle_fits_in_an_issue(self):
        text = evidence.bundle("h", "c", "x" * 100000)
        self.assertLessEqual(len(text), evidence.LIMIT + 200)

    def test_failing_pods_and_their_logs_are_collected(self):
        class Kube:
            def get(self, path):
                if "/pods" in path and "events" not in path:
                    return {"items": [
                        {"metadata": {"name": "immich-server-1"}, "status": {"phase": "Running",
                         "containerStatuses": [{"name": "server", "ready": False, "restartCount": 4,
                                                "state": {"waiting": {"reason": "CrashLoopBackOff"}}}]}},
                        {"metadata": {"name": "fine"}, "status": {"phase": "Running",
                         "containerStatuses": [{"name": "c", "ready": True, "state": {"running": {}}}]}}]}
                if "events" in path:
                    return {"items": [{"reason": "BackOff", "message": "Back-off restarting",
                                       "involvedObject": {"kind": "Pod", "name": "immich-server-1"}}]}
                return {"items": []}

            def text(self, path):
                return "Error: migration failed\n"

        argocd = types.SimpleNamespace(list=lambda: [
            {"metadata": {"name": "immich"}, "spec": {"destination": {"namespace": "immich"}},
             "status": {"sync": {"status": "Synced"}, "health": {"status": "Degraded"}}}])
        text = evidence.Evidence(Kube(), argocd).cluster()
        self.assertIn("immich-server-1", text)
        self.assertIn("CrashLoopBackOff", text)
        self.assertIn("migration failed", text)
        self.assertIn("previous run", text)
        self.assertNotIn("**fine**", text)


class GitHubTest(unittest.TestCase):
    def client(self, answers):
        class Response:
            def __init__(self, body):
                self.body = body

            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

            def read(self):
                return self.body

        return GitHub("https://api", "o/r", "t", "ring0/verified",
                      opener=lambda req, timeout: Response(json.dumps(
                          answers[req.full_url.split("/repos/o/r", 1)[1].split("?")[0]]).encode()))

    def test_readiness_tells_waiting_from_failed(self):
        ok = {"status": "completed", "conclusion": "success"}
        cases = [
            ({"statuses": [{"state": "success"}]}, [ok], "ready"),
            ({"statuses": [{"state": "pending", "context": "renovate/stability-days"}]}, [ok], "waiting"),
            ({"statuses": []}, [{"status": "in_progress"}], "waiting"),
            ({"statuses": []}, [], "waiting"),
            ({"statuses": []}, [ok, {"status": "completed", "conclusion": "failure"}], "failed"),
        ]
        for status, runs, expected in cases:
            gh = self.client({"/commits/s/status": status, "/commits/s/check-runs": {"check_runs": runs}})
            self.assertEqual(expected, gh.readiness("s"), (status, runs))

    def test_only_the_owners_newest_review_of_the_current_head_approves(self):
        reviews = [{"user": {"login": "stephan271"}, "state": "APPROVED", "commit_id": "old"},
                   {"user": {"login": "someone"}, "state": "APPROVED", "commit_id": "new"}]
        gh = self.client({"/pulls/7/reviews": reviews})
        self.assertFalse(gh.approved_by(7, "stephan271", "new"))
        reviews.append({"user": {"login": "stephan271"}, "state": "APPROVED", "commit_id": "new"})
        self.assertTrue(gh.approved_by(7, "stephan271", "new"))


class RbacTest(unittest.TestCase):
    def manifest(self):
        return "\n".join(l for l in (TENANT / "steward-rbac.yaml").read_text().splitlines()
                         if not l.lstrip().startswith("#"))

    def test_the_steward_reads_no_secrets_and_cannot_exec(self):
        text = self.manifest()
        rules = text.split("kind: ClusterRole\n", 1)[1].split("---", 1)[0]
        self.assertNotIn('"secrets"', rules)
        self.assertNotIn("pods/exec", text)
        self.assertNotIn("pods/attach", text)
        self.assertNotIn('resources: ["*"]\n    verbs: ["delete', rules)
        core = rules.split('apiGroups: [""]', 1)[1].split("- apiGroups", 1)[0]
        self.assertNotIn('"*"', core, "core resources are listed one by one, Secrets left out")

    def test_the_steward_deletes_only_pods_and_jobs(self):
        rules = self.manifest().split("rules:")[1:]
        deletable = []
        for rule in "".join(rules).split("- apiGroups:")[1:]:
            if "delete" in rule.split("verbs:", 1)[1].split("\n", 1)[0]:
                deletable.append(rule.split("resources:", 1)[1].split("\n", 1)[0].strip())
        self.assertEqual(['["pods"]', '["jobs"]'], deletable)


if __name__ == "__main__":
    unittest.main()
