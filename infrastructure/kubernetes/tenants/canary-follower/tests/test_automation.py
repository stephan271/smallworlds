"""Tests for what makes Ring 0 run on its own: the pre-update backup, the checks Job,
and the merge queue with its revert (design §3.5, §3.6). Fakes only, no network.

    python3 -m unittest discover -s infrastructure/kubernetes/tenants/canary-follower/tests
"""
import pathlib
import sys
import unittest

TENANT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(TENANT / "src"))
sys.path.insert(0, str(TENANT / "tests"))

from backups import GROUP, LABEL, BackupFailed, Backups, backup_name  # noqa: E402
from checksjob import ChecksJob  # noqa: E402
from follower import Outcome  # noqa: E402
from mergequeue import REJECTED, MergeQueue, fingerprint  # noqa: E402
from test_follower import BASE, Clock, FakeArgoKube, make  # noqa: E402
from argocd import ArgoCD  # noqa: E402

SHA = "a" * 40


class FakeCnpg:
    """CNPG clusters and Backups; a Backup completes after `polls` reads."""

    def __init__(self, clusters, polls=2, fail=()):
        self.clusters = clusters  # namespace -> [cluster names]
        self.backups = {}  # (namespace, name) -> object
        self.reads = {}
        self.polls = polls
        self.fail = set(fail)
        self.deleted = []
        self.created = []

    def get(self, path):
        parts = path.split("?")[0].split("/")
        namespace = parts[parts.index("namespaces") + 1]
        if path.split("?")[0].endswith("/clusters"):
            return {"items": [{"metadata": {"name": n}} for n in self.clusters.get(namespace, [])]}
        if path.split("?")[0].endswith("/backups"):
            items = [b for (ns, _), b in self.backups.items() if ns == namespace]
            if "labelSelector" in path:
                items = [b for b in items if LABEL in b["metadata"].get("labels", {})]
            return {"items": items}
        name = parts[-1]
        backup = self.backups[(namespace, name)]
        self.reads[name] = self.reads.get(name, 0) + 1
        if namespace in self.fail:
            backup["status"] = {"phase": "failed", "error": "can't reach the object store"}
        elif self.reads[name] >= self.polls:
            backup["status"] = {"phase": "completed"}
        return backup

    def create(self, path, body):
        namespace = body["metadata"]["namespace"]
        body.setdefault("metadata", {})["creationTimestamp"] = f"2026-11-11T{len(self.backups):02d}:00:00Z"
        self.backups[(namespace, body["metadata"]["name"])] = body
        self.created.append((namespace, body["metadata"]["name"], body["spec"]["cluster"]["name"]))
        return body

    def delete(self, path):
        parts = path.split("/")
        self.deleted.append(parts[-1])
        self.backups.pop((parts[parts.index("namespaces") + 1], parts[-1]))


class BackupsTest(unittest.TestCase):
    def make(self, **kwargs):
        clock = Clock()
        kube = FakeCnpg(**kwargs)
        return Backups(kube, {"gallery": "immich"}, timeout=600, keep=2,
                       clock=clock, sleep=clock.sleep), kube

    def test_every_cluster_of_a_touched_tenant_is_backed_up_and_waited_for(self):
        backups, kube = self.make(clusters={"immich": ["database"], "keycloak": ["keycloak-db"]})
        taken = backups.before(SHA, ["gallery", "keycloak"])
        self.assertEqual(["immich/database", "keycloak/keycloak-db"], taken)
        self.assertEqual({("immich", backup_name(SHA), "database"),
                          ("keycloak", backup_name(SHA), "keycloak-db")}, set(kube.created))
        backup = kube.backups[("immich", backup_name(SHA))]
        self.assertEqual("plugin", backup["spec"]["method"])
        self.assertEqual("barman-cloud.cloudnative-pg.io", backup["spec"]["pluginConfiguration"]["name"])
        self.assertTrue(all(b.get("status", {}).get("phase") == "completed" for b in kube.backups.values()))

    def test_a_tenant_without_a_database_here_is_skipped(self):
        backups, kube = self.make(clusters={})
        self.assertEqual([], backups.before(SHA, ["forgejo"]))

    def test_a_restarted_follower_reuses_the_backup_it_started(self):
        backups, kube = self.make(clusters={"forgejo": ["database"]})
        backups.before(SHA, ["forgejo"])
        backups.before(SHA, ["forgejo"])
        self.assertEqual(1, len(kube.created))

    def test_a_failed_backup_stops_the_update(self):
        backups, kube = self.make(clusters={"stalwart": ["database"]}, fail=["stalwart"])
        with self.assertRaisesRegex(BackupFailed, "object store"):
            backups.before(SHA, ["stalwart"])

    def test_a_backup_that_never_finishes_stops_the_update(self):
        backups, kube = self.make(clusters={"stalwart": ["database"]}, polls=10 ** 9)
        with self.assertRaisesRegex(BackupFailed, "in time: stalwart/database"):
            backups.before(SHA, ["stalwart"])

    def test_only_the_newest_of_our_own_backups_are_kept(self):
        backups, kube = self.make(clusters={"forgejo": ["database"]})
        kube.backups[("forgejo", "nightly")] = {"metadata": {"name": "nightly", "creationTimestamp": "2020"}}
        for sha in ("1" * 40, "2" * 40, "3" * 40):
            backups.before(sha, ["forgejo"])
        self.assertEqual([backup_name("1" * 40)], kube.deleted)
        self.assertIn(("forgejo", "nightly"), kube.backups, "the scheduled backups are not ours to prune")


class FakeJobsKube:
    def __init__(self, outcome="Complete", log="CHECKS e2e 27 passed, 3 skipped; data 4/4 apps intact\n",
                 polls=2):
        self.outcome = outcome
        self.log = log
        self.polls = polls
        self.jobs = {}
        self.deleted = []
        self.reads = 0
        self.cronjob = {"spec": {"jobTemplate": {
            "metadata": {"labels": {"app": "canary-checks"}},
            "spec": {"backoffLimit": 0, "template": {"spec": {"containers": [
                {"name": "checks", "env": [{"name": "DOMAIN", "value": "canary.x"},
                                           {"name": "CHECK_SHA", "value": "stale"}]}]}}}}}}

    def get(self, path):
        if "/cronjobs/" in path:
            return self.cronjob
        if "/pods?" in path:
            return {"items": [{"metadata": {"name": "pod-1"}}]}
        if "/jobs?labelSelector=app=" in path:
            label = path.rsplit("=", 1)[1]
            return {"items": [j for j in self.jobs.values()
                              if j.get("metadata", {}).get("labels", {}).get("app", label) == label]}
        name = path.rsplit("/", 1)[1]
        self.reads += 1
        job = self.jobs[name]
        if self.reads >= self.polls and self.outcome:
            job["status"] = {"conditions": [{"type": self.outcome, "status": "True"}]}
        return job

    def create(self, path, body):
        body["status"] = {"active": 1}
        self.jobs[body["metadata"]["name"]] = body
        return body

    def delete(self, path):
        name = path.rsplit("/", 1)[1]
        self.deleted.append(name)
        self.jobs.pop(name, None)

    def text(self, path):
        return ">>> e2e\nnoise\n" + self.log


class ChecksJobTest(unittest.TestCase):
    def make(self, **kwargs):
        clock = Clock()
        kube = FakeJobsKube(**kwargs)
        return ChecksJob(kube, "canary-follower", "canary-checks", 3600,
                         clock=clock, sleep=clock.sleep, now=lambda: 1700000000), kube

    def test_a_passing_job_returns_its_summary(self):
        job, kube = self.make()
        self.assertEqual((True, "e2e 27 passed, 3 skipped; data 4/4 apps intact"), job.run(SHA))
        created = next(iter(kube.jobs.values()))
        env = created["spec"]["template"]["spec"]["containers"][0]["env"]
        self.assertEqual([{"name": "DOMAIN", "value": "canary.x"}, {"name": "CHECK_SHA", "value": SHA}], env)
        self.assertEqual("canary-checks", created["metadata"]["labels"]["app"])
        self.assertLessEqual(len(created["metadata"]["name"]), 63)

    def test_a_failing_job_fails_with_its_summary(self):
        job, kube = self.make(outcome="Failed", log="CHECKS e2e 25 passed, 2 failed; data 4/4 apps intact\n")
        self.assertEqual((False, "e2e 25 passed, 2 failed; data 4/4 apps intact"), job.run(SHA))

    def test_a_job_that_could_not_check_anything_is_no_verdict(self):
        job, kube = self.make(outcome="Failed", log="CHECKS-INFRA npm ci failed\n")
        self.assertEqual((None, "npm ci failed"), job.run(SHA))

    def test_a_pod_that_cannot_start_is_no_verdict_and_does_not_wait_the_hour(self):
        job, kube = self.make(outcome=None, log="")
        original = kube.get

        def get(path):
            if "/pods?" in path:
                return {"items": [{"metadata": {"name": "pod-1"}, "status": {"containerStatuses": [
                    {"state": {"waiting": {"reason": "CreateContainerConfigError",
                                           "message": 'secret "canary-checks" not found'}}}]}}]}
            return original(path)
        kube.get = get
        passed, summary = job.run(SHA)
        self.assertIsNone(passed)
        self.assertIn('CreateContainerConfigError: secret "canary-checks" not found', summary)
        self.assertLess(job.clock(), 600, "given up after minutes, not at the deadline")
        self.assertEqual(1, len(kube.deleted))

    def test_a_job_that_hangs_is_stopped_and_fails(self):
        job, kube = self.make(outcome=None, log="")
        passed, summary = job.run(SHA)
        self.assertFalse(passed)
        self.assertIn("timed out after 60 min", summary)
        self.assertIn("no summary", summary)
        self.assertEqual(1, len(kube.deleted))

    def test_an_earlier_running_job_is_stopped_first(self):
        job, kube = self.make()
        kube.jobs["canary-checks-old"] = {"metadata": {"name": "canary-checks-old"}, "status": {"active": 1}}
        job.run(SHA)
        self.assertIn("canary-checks-old", kube.deleted)

    def test_each_kind_of_check_clears_only_its_own_jobs(self):
        # The storage checks run right after the e2e checks: clearing by a
        # shared label would delete the e2e Job's record of a failure.
        clock = Clock()
        kube = FakeJobsKube(log="CHECKS storage: Garage v2.3.0, rclone 1.74 ok\n")
        kube.jobs["canary-checks-failed"] = {"metadata": {"name": "canary-checks-failed",
                                                          "labels": {"app": "canary-checks"}},
                                             "status": {"failed": 1}}
        storage = ChecksJob(kube, "canary-follower", "canary-storage-checks", 1500,
                            clock=clock, sleep=clock.sleep, now=lambda: 1700000000)
        self.assertEqual((True, "storage: Garage v2.3.0, rclone 1.74 ok"), storage.run(SHA))
        created = next(j for n, j in kube.jobs.items() if n.startswith("canary-storage-checks-"))
        self.assertEqual("canary-storage-checks", created["metadata"]["labels"]["app"])
        self.assertNotIn("canary-checks-failed", kube.deleted)

    def test_failed_jobs_of_earlier_runs_are_removed_and_passed_ones_kept(self):
        # Each failed Job keeps KubeJobFailed firing for its three-day TTL; five
        # of them made one notification too large for ntfy (Ring 0, 2026-11-13).
        job, kube = self.make()
        kube.jobs["canary-checks-failed"] = {"metadata": {"name": "canary-checks-failed"},
                                             "status": {"failed": 1}}
        kube.jobs["canary-checks-passed"] = {"metadata": {"name": "canary-checks-passed"},
                                             "status": {"succeeded": 1}}
        job.run(SHA)
        self.assertIn("canary-checks-failed", kube.deleted)
        self.assertNotIn("canary-checks-passed", kube.deleted)


RENOVATE = "renovate[bot]"


def pull(number, title, labels=("ring0-queue",), user=RENOVATE, merged_at=None):
    return {"number": number, "title": title, "user": {"login": user}, "draft": False,
            "labels": [{"name": l} for l in labels], "merged_at": merged_at,
            "head": {"sha": f"head{number}"}}


class FakeRepo:
    def __init__(self, pulls=(), files=None, green=(), mergeable=None, labels=(), from_pull=None):
        self.pulls = {p["number"]: p for p in pulls}
        self.files = files or {}
        self.green = set(green)
        self.mergeable = mergeable or {}
        self.labels = list(labels)
        self.from_pull = from_pull
        self.merged = []
        self.labelled = []
        self.comments = []
        self.reverts = []
        self.opened = []

    def label_names(self):
        return self.labels

    def open_pulls(self, base):
        return [p for p in self.pulls.values() if not p.get("merged_at")]

    def pull(self, number):
        return {**self.pulls[number], "mergeable": self.mergeable.get(number, True)}

    def pull_files(self, number):
        return self.files.get(number, [{"filename": f"tenants/{number}/kustomization.yaml",
                                        "patch": f"-  newTag: 1\n+  newTag: {number}"}])

    def checks_passed(self, sha):
        return sha in self.green

    def merge(self, number, sha, title):
        self.merged.append((number, sha))
        return f"merged{number}"

    def pulls_of(self, sha):
        return [self.from_pull] if self.from_pull else []

    def add_label(self, number, name):
        self.labelled.append((number, name))
        self.labels.append(name)

    def comment(self, number, body):
        self.comments.append((number, body))

    def revert_commit(self, sha, message):
        self.reverts.append((sha, message))
        return "revert" + sha[:6]

    def open_pull(self, branch, sha, base, title, body):
        self.opened.append((branch, sha, title))
        return 99


def queue(repo, **kwargs):
    return MergeQueue(repo, "main", "ring0-queue", RENOVATE, **kwargs)


class MergeQueueTest(unittest.TestCase):
    def test_the_oldest_ready_update_is_merged_alone(self):
        repo = FakeRepo([pull(41, "Update cloudnative-pg"), pull(42, "Update cert-manager")],
                        green={"head41", "head42"})
        self.assertEqual(41, queue(repo).advance())
        self.assertEqual([(41, "head41")], repo.merged, "one update per green head, never two")

    def test_an_update_whose_checks_have_not_passed_waits(self):
        repo = FakeRepo([pull(41, "a"), pull(42, "b")], green={"head42"})
        self.assertEqual(42, queue(repo).advance())

    def test_majors_and_human_prs_are_never_merged(self):
        repo = FakeRepo([pull(50, "Update traefik to v42", labels=("major",)),
                         pull(51, "Fix a thing", user="stephan271")], green={"head50", "head51"})
        q = queue(repo)
        self.assertIsNone(q.advance())
        self.assertEqual([], repo.merged)
        self.assertEqual("no queued update is ready", q.waiting)

    def test_a_conflicting_or_unknown_mergeability_waits(self):
        repo = FakeRepo([pull(41, "a"), pull(42, "b")], green={"head41", "head42"},
                        mergeable={41: None, 42: False})
        self.assertIsNone(queue(repo).advance())

    def test_an_update_ring0_rejected_before_is_not_merged_again(self):
        files = {42: [{"filename": "x", "patch": "+  newTag: v2"}]}
        repo = FakeRepo([pull(42, "Update nextcloud")], files=files, green={"head42"},
                        labels=[REJECTED + fingerprint(files[42])])
        self.assertIsNone(queue(repo).advance())

    def test_a_newer_release_of_a_rejected_update_is_tried(self):
        old = [{"filename": "x", "patch": "+  newTag: v2"}]
        repo = FakeRepo([pull(42, "Update nextcloud")], files={42: [{"filename": "x", "patch": "+  newTag: v3"}]},
                        green={"head42"}, labels=[REJECTED + fingerprint(old)])
        self.assertEqual(42, queue(repo).advance())

    def test_a_refused_merge_does_not_block_the_updates_behind_it(self):
        repo = FakeRepo([pull(38, "Pin dependencies"), pull(41, "Update cloudnative-pg")],
                        green={"head38", "head41"})
        original = repo.merge

        def merge(number, sha, title):
            if number == 38:
                raise PermissionError("403: refusing to allow a token to update a workflow")
            return original(number, sha, title)
        repo.merge = merge
        q = queue(repo)
        self.assertEqual(41, q.advance())
        repo.pulls.pop(41)
        self.assertIsNone(q.advance(), "#38 is not tried again until its head changes")

    def test_merging_can_be_switched_off(self):
        repo = FakeRepo([pull(41, "a")], green={"head41"})
        self.assertIsNone(queue(repo, merge=False).advance())

    def test_the_fingerprint_ignores_removed_lines_and_file_order(self):
        a = [{"filename": "b", "patch": "-old\n+new"}, {"filename": "a", "patch": "+x"}]
        b = [{"filename": "a", "patch": "+x"}, {"filename": "b", "patch": "-other\n+new"}]
        self.assertEqual(fingerprint(a), fingerprint(b))


def rejected(restore_only=(), sha=SHA):
    return Outcome(sha, "failure", "Checks failed: e2e 1 failed", restore_only=list(restore_only)
                   if restore_only is not None else None)


class RejectTest(unittest.TestCase):
    def test_a_rejected_queued_update_is_marked_reverted_and_merged(self):
        repo = FakeRepo(from_pull=pull(41, "Update cloudnative-pg", merged_at="2026-11-11"))
        done = queue(repo).reject(rejected(), head=SHA)
        self.assertEqual("reverted by #99", done)
        self.assertEqual([(41, REJECTED + fingerprint(repo.pull_files(41)))], repo.labelled)
        self.assertEqual(SHA, repo.reverts[0][0])
        self.assertIn("Ring 0 rejected", repo.reverts[0][1])
        self.assertEqual(("ring0/revert-aaaaaaaaaaaa", "revertaaaaaa", 'Revert "Update cloudnative-pg"'),
                         repo.opened[0])
        self.assertEqual([(99, "revertaaaaaa")], repo.merged)

    def test_a_migrating_tenant_is_not_reverted(self):
        repo = FakeRepo(from_pull=pull(44, "Update gallery", merged_at="2026-11-11"))
        done = queue(repo).reject(rejected(["gallery"]), head=SHA)
        self.assertIn("restore", done)
        self.assertEqual([], repo.reverts)
        self.assertEqual(1, len(repo.labelled), "still marked, so it is not merged again")

    def test_unreadable_changes_are_not_reverted(self):
        repo = FakeRepo(from_pull=pull(41, "a", merged_at="x"))
        queue(repo).reject(rejected(None), head=SHA)
        self.assertEqual([], repo.reverts)

    def test_a_persons_commit_gets_a_revert_pr_but_no_merge(self):
        repo = FakeRepo(from_pull=pull(45, "Change Stalwart", user="stephan271", labels=(), merged_at="x"))
        done = queue(repo).reject(rejected(), head=SHA)
        self.assertEqual("revert offered in #99, for a person to merge", done)
        self.assertEqual([], repo.merged)

    def test_a_direct_push_gets_a_revert_pr_but_no_merge(self):
        repo = FakeRepo()
        queue(repo).reject(rejected(), head=SHA)
        self.assertEqual("Revert aaaaaaaaaaaa", repo.opened[0][2])
        self.assertEqual([], repo.merged)

    def test_nothing_is_reverted_once_main_moved_on(self):
        repo = FakeRepo(from_pull=pull(41, "a", merged_at="x"))
        self.assertEqual("not reverted: main has moved on", queue(repo).reject(rejected(), head="b" * 40))
        self.assertEqual([], repo.reverts)

    def test_reverting_can_be_switched_off(self):
        repo = FakeRepo(from_pull=pull(41, "a", merged_at="x"))
        queue(repo, revert=False).reject(rejected(), head=SHA)
        self.assertEqual([], repo.reverts)


class FakeQueue:
    def __init__(self):
        self.advanced = 0
        self.rejected = []

    def advance(self):
        self.advanced += 1
        return 41

    def reject(self, outcome, head):
        self.rejected.append(outcome.sha)
        return "reverted by #99"


class FakeChecks:
    def __init__(self, passed=True, summary="e2e 27 passed; data 4/4 apps intact"):
        self.result = (passed, summary)
        self.ran = []

    def run(self, sha):
        self.ran.append(sha)
        return self.result


class FakeBackups:
    def __init__(self, fail=None):
        self.fail = fail
        self.calls = []

    def before(self, sha, tenants):
        self.calls.append(tenants)
        if self.fail:
            raise BackupFailed(self.fail)
        return [f"{t}/database" for t in tenants]


def wired(paths=(), checks=None, backups=None, storage=None, **kwargs):
    follower, gh, kube, seen = make(paths=paths, **kwargs)
    follower.queue = FakeQueue()
    follower.checks_job = checks
    follower.storage_job = storage
    follower.backups = backups
    return follower, gh, kube


class FollowerAutomationTest(unittest.TestCase):
    def test_a_green_commit_lets_the_queue_merge_the_next_update(self):
        follower, gh, kube = wired(checks=FakeChecks())
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertIn("e2e 27 passed; data 4/4 apps intact", outcome.description)
        self.assertNotIn("not yet run", outcome.description)
        self.assertEqual(1, follower.queue.advanced)
        self.assertEqual("merged #41", follower.last_action)

    def test_a_green_head_keeps_the_queue_moving_every_poll(self):
        follower, gh, kube = wired(checks=FakeChecks())
        follower.tick()
        follower.tick()
        self.assertEqual(2, follower.queue.advanced)

    def test_a_green_commit_no_longer_at_the_head_merges_nothing(self):
        follower, gh, kube = wired(checks=FakeChecks())
        checks = follower.checks_job

        def run_while_main_moves(sha):
            gh.head = "b" * 40  # a person pushes during the checks
            return FakeChecks.run(checks, sha)
        checks.run = run_while_main_moves
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertEqual(0, follower.queue.advanced, "the new head is unverified")

    def test_a_restarted_pod_on_a_green_head_resumes_the_queue(self):
        follower, gh, kube = wired(statuses={SHA: "success"})
        self.assertIsNone(follower.tick())
        self.assertEqual(1, follower.queue.advanced)

    def test_a_rejected_head_stops_the_queue_and_is_handled(self):
        follower, gh, kube = wired(checks=FakeChecks(False, "e2e 26 passed, 1 failed; data 4/4 apps intact"))
        outcome = follower.tick()
        self.assertEqual("failure", outcome.state)
        self.assertIn("Checks failed: e2e 26 passed, 1 failed", outcome.description)
        self.assertEqual([SHA], follower.queue.rejected)
        follower.tick()
        self.assertEqual(0, follower.queue.advanced, "nothing merged onto a red head")

    def test_the_storage_checks_follow_the_e2e_checks_into_the_status(self):
        storage = FakeChecks(summary="storage: Garage v2.4.1, rclone 1.76 ok")
        follower, gh, kube = wired(checks=FakeChecks(), storage=storage)
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertEqual([SHA], storage.ran)
        self.assertIn("data 4/4 apps intact, storage: Garage v2.4.1, rclone 1.76 ok", outcome.description)

    def test_failed_storage_checks_reject_the_commit(self):
        follower, gh, kube = wired(checks=FakeChecks(),
                                   storage=FakeChecks(False, "storage: garage-system: could not create a key"))
        outcome = follower.tick()
        self.assertEqual("failure", outcome.state)
        self.assertIn("Storage checks failed: storage: garage-system: could not create a key",
                      outcome.description)
        self.assertEqual([SHA], follower.queue.rejected)

    def test_storage_checks_that_could_not_run_are_no_verdict(self):
        follower, gh, kube = wired(checks=FakeChecks(),
                                   storage=FakeChecks(None, "storage: could not start a replicator Job"))
        outcome = follower.tick()
        self.assertEqual("error", outcome.state)
        self.assertEqual([], follower.queue.rejected)

    def test_storage_checks_wait_for_the_e2e_checks_to_pass(self):
        storage = FakeChecks()
        follower, gh, kube = wired(checks=FakeChecks(False, "e2e 1 failed"), storage=storage)
        follower.tick()
        self.assertEqual([], storage.ran)

    def test_a_restarted_pod_on_a_red_head_merges_nothing(self):
        follower, gh, kube = wired(statuses={SHA: "failure"})
        follower.tick()
        follower.tick()
        self.assertEqual(0, follower.queue.advanced)

    def test_a_migrating_tenant_is_backed_up_before_anything_is_synced(self):
        backups = FakeBackups()
        follower, gh, kube = wired(paths=["infrastructure/kubernetes/tenants/nextcloud/values.yaml"],
                                   backups=backups)
        outcome = follower.tick()
        self.assertEqual([["nextcloud"]], backups.calls)
        self.assertIn("backup 1 databases", outcome.description)

    def test_a_commit_elsewhere_takes_no_backup(self):
        backups = FakeBackups()
        follower, gh, kube = wired(paths=["README.md"], backups=backups)
        follower.tick()
        self.assertEqual([], backups.calls)

    def test_unreadable_changes_back_up_every_migrating_tenant(self):
        backups = FakeBackups()
        follower, gh, kube = wired(paths=OSError("rate limited"), backups=backups)
        follower.tick()
        self.assertEqual([follower.s.migrating], backups.calls)

    def test_a_failed_backup_syncs_nothing_and_merges_nothing(self):
        follower, gh, kube = wired(paths=["infrastructure/kubernetes/tenants/forgejo/a.yaml"],
                                   backups=FakeBackups(fail="forgejo/database: object store down"))
        outcome = follower.tick()
        self.assertEqual("error", outcome.state)
        self.assertIn("Pre-update backup failed, not synced", outcome.description)
        self.assertFalse(any(c[0] == "PATCH" for c in kube.calls), "nothing may be synced")
        self.assertEqual(0, follower.queue.advanced)


class ErrorRetryTest(unittest.TestCase):
    """An error is no verdict on the commit: it is not reverted, and the head is
    verified again later instead of stopping the queue for good."""

    def test_checks_that_could_not_run_are_an_error_and_nothing_is_reverted(self):
        follower, gh, kube = wired(checks=FakeChecks(None, "npm ci failed"))
        outcome = follower.tick()
        self.assertEqual("error", outcome.state)
        self.assertIn("Checks could not run: npm ci failed", outcome.description)
        self.assertEqual([], follower.queue.rejected)

    def test_an_errored_head_is_verified_again_after_the_retry_period(self):
        checks = FakeChecks(None, "could not fetch")
        follower, gh, kube = wired(checks=checks)
        clock = Clock()
        follower.clock = clock
        follower.tick()
        self.assertIsNone(follower.tick(), "not at once")
        clock.now += follower.s.error_retry_seconds
        checks.result = (True, "e2e 27 passed; data 4/4 apps intact")
        outcome = follower.tick()
        self.assertEqual("success", outcome.state)
        self.assertEqual(2, len(checks.ran))
        self.assertEqual(1, follower.queue.advanced)

    def test_retries_are_bounded(self):
        checks = FakeChecks(None, "could not fetch")
        follower, gh, kube = wired(checks=checks)
        clock = Clock()
        follower.clock = clock
        for _ in range(10):
            follower.tick()
            clock.now += follower.s.error_retry_seconds
        self.assertEqual(1 + follower.s.error_retries, len(checks.ran))

    def test_a_restarted_pod_retries_an_errored_head(self):
        follower, gh, kube = wired(statuses={SHA: "error"}, checks=FakeChecks())
        clock = Clock()
        follower.clock = clock
        self.assertIsNone(follower.tick())
        clock.now += follower.s.error_retry_seconds
        self.assertEqual("success", follower.tick().state)

    def test_a_failure_after_an_error_ends_the_retries(self):
        # Ring 0, 2026-11-11: an errored head was verified again, rejected, and
        # then verified again five minutes later because the error was remembered.
        checks = FakeChecks(None, "could not fetch")
        follower, gh, kube = wired(checks=checks)
        clock = Clock()
        follower.clock = clock
        follower.tick()
        clock.now += follower.s.error_retry_seconds
        checks.result = (False, "e2e 1 failed")
        self.assertEqual("failure", follower.tick().state)
        clock.now += follower.s.error_retry_seconds
        self.assertIsNone(follower.tick())
        self.assertEqual(2, len(checks.ran))

    def test_a_rejection_is_not_retried(self):
        checks = FakeChecks(False, "e2e 1 failed")
        follower, gh, kube = wired(checks=checks)
        clock = Clock()
        follower.clock = clock
        follower.tick()
        clock.now += 10 ** 6
        follower.tick()
        self.assertEqual(1, len(checks.ran))


class ErroredSyncKube(FakeArgoKube):
    """An app whose next `errors` syncs end in phase Error, as Argo CD reports a
    ComparisonError (Ring 0, 2026-11-11: CRD health unreadable)."""

    def __init__(self, sha, errors):
        super().__init__(sha)
        self.errors = dict(errors)

    def _settle(self, app):
        name = app["metadata"]["name"]
        had_operation = "operation" in app
        super()._settle(app)
        if had_operation and self.errors.get(name, 0) > 0:
            self.errors[name] -= 1
            app["status"]["operationState"] = {
                "phase": "Error",
                "message": "ComparisonError: error setting app health: failed to get resource health "
                           "for \"CustomResourceDefinition\""}


class ConvergeRetryTest(unittest.TestCase):
    def converge(self, errors):
        clock = Clock()
        kube = ErroredSyncKube(SHA, errors)
        argo = ArgoCD(kube, "argocd", "smallworlds-root", clock, clock.sleep)
        argo.follow(300)
        return argo.converge(2700, 20), kube

    def test_a_transient_error_is_synced_again_and_converges(self):
        (converged, lagging), kube = self.converge({"keycloak": 2})
        self.assertTrue(converged, lagging)
        syncs = [c for c in kube.calls if c[0] == "PATCH" and "operation" in c[2]
                 and c[1].endswith("/keycloak")]
        self.assertEqual(3, len(syncs), "the first sync and two more")

    def test_an_error_that_stays_fails_after_the_bounded_retries(self):
        (converged, lagging), kube = self.converge({"keycloak": 10})
        self.assertFalse(converged)
        self.assertIn("keycloak: sync Error: ComparisonError", lagging[0])


class SwitchoverKube(FakeArgoKube):
    """An app whose next `degraded` syncs end Failed because a plain resource (no
    hook) read Degraded while it rolled, and which is Synced and Healthy right
    after. Ring 0, 2026-11-12: Postgres 16.13 -> 16.15 in immich; Argo CD's
    built-in health check calls CNPG's "Switchover in progress" Degraded, the
    sync failed, CNPG finished the switchover a moment later."""

    def __init__(self, sha, degraded):
        super().__init__(sha)
        self.degraded = dict(degraded)

    def _settle(self, app):
        name = app["metadata"]["name"]
        had_operation = "operation" in app
        super()._settle(app)
        if had_operation and self.degraded.get(name, 0) > 0:
            self.degraded[name] -= 1
            app["status"]["operationState"] = {
                "phase": "Failed",
                "message": "one or more synchronization tasks completed unsuccessfully",
                "syncResult": {"resources": [
                    {"kind": "Job", "name": "garage-init", "status": "Synced",
                     "hookType": "Sync", "hookPhase": "Succeeded", "syncPhase": "Sync"},
                    {"kind": "Cluster", "name": "database", "status": "Synced",
                     "hookPhase": "Failed", "syncPhase": "Sync",
                     "message": "cluster.postgresql.cnpg.io/database configured"}]}}


class SwitchoverRetryTest(unittest.TestCase):
    def converge(self, degraded):
        clock = Clock()
        kube = SwitchoverKube(SHA, degraded)
        argo = ArgoCD(kube, "argocd", "smallworlds-root", clock, clock.sleep)
        argo.follow(300)
        return argo.converge(2700, 20), kube

    def syncs(self, kube, name):
        return [c for c in kube.calls if c[0] == "PATCH" and "operation" in c[2]
                and c[1].endswith("/" + name)]

    def test_a_resource_degraded_only_while_it_rolled_is_synced_again_and_converges(self):
        (converged, lagging), kube = self.converge({"gallery": 1})
        self.assertTrue(converged, lagging)
        self.assertEqual(2, len(self.syncs(kube, "gallery")), "the first sync and one more")

    def test_a_resource_that_keeps_failing_the_sync_fails_after_the_bounded_retries(self):
        (converged, lagging), kube = self.converge({"gallery": 10})
        self.assertFalse(converged)
        self.assertIn("gallery: sync Failed: Cluster database", lagging[0])

    def test_a_failed_hook_is_not_synced_again(self):
        # A hook that failed is a verdict (stalwart-init, Ring 0 2026-11-10):
        # syncing again would only hide it behind a retry.
        clock = Clock()
        kube = FakeArgoKube(SHA)
        kube.fail["keycloak"] = ("Job", "stalwart-init", "Job has reached the specified backoff limit")
        argo = ArgoCD(kube, "argocd", "smallworlds-root", clock, clock.sleep)
        argo.follow(300)
        converged, lagging = argo.converge(2700, 20)
        self.assertFalse(converged)
        self.assertEqual(1, len(self.syncs(kube, "keycloak")))



CODEBERG_DOWN = (
    "ComparisonError: Failed to load target state: failed to generate manifest for source 1 of 1: "
    "rpc error: code = Unknown desc = Manifest generation error (cached): `kustomize build` failed "
    "exit status 1: Error: accumulating resources from 'https://github.com/x/smallworlds.git/"
    "infrastructure/kubernetes/tenants/forgejo?ref=HEAD': Error: failed to perform \"FetchReference\" "
    "on source: GET \"https://codeberg.org/v2/forgejo-contrib/forgejo/manifests/17.1.1\": "
    "response status code 403: Forbidden")


class UnrenderableKube(FakeArgoKube):
    """Apps whose target cannot be rendered: every sync ends in Error with `message`
    and the sync status Unknown, as Argo CD reported forgejo on Ring 0 (2026-11-12)."""

    def __init__(self, sha, broken):
        super().__init__(sha)
        self.broken = dict(broken)

    def _settle(self, app):
        name = app["metadata"]["name"]
        super()._settle(app)
        if name in self.broken:
            app["status"]["sync"] = {"status": "Unknown"}
            app["status"]["operationState"] = {"phase": "Error", "message": self.broken[name]}
            app["status"]["conditions"] = [{"type": "ComparisonError", "message": self.broken[name]}]


class UpstreamUnreachableTest(unittest.TestCase):
    def verify(self, broken):
        clock = Clock()
        kube = UnrenderableKube(SHA, broken)
        follower, gh, _, _ = make(SHA)
        follower.kube = kube
        follower.argocd = ArgoCD(kube, "argocd", "smallworlds-root", clock, clock.sleep)
        return follower.tick()

    def test_an_upstream_that_does_not_answer_is_an_error_not_a_rejection(self):
        outcome = self.verify({"keycloak": CODEBERG_DOWN})
        self.assertEqual("error", outcome.state)
        self.assertIn("keycloak: upstream unreachable: codeberg.org: response status code 403",
                      outcome.description)

    def test_a_version_that_does_not_exist_still_fails(self):
        outcome = self.verify({"keycloak": CODEBERG_DOWN.replace("403: Forbidden", "404: Not Found")})
        self.assertEqual("failure", outcome.state)

    def test_a_broken_kustomization_still_fails(self):
        outcome = self.verify({"keycloak": "ComparisonError: Manifest generation error: "
                                           "accumulating resources: missing metadata.name"})
        self.assertEqual("failure", outcome.state)

    def test_an_unreachable_upstream_beside_a_real_failure_fails(self):
        outcome = self.verify({"keycloak": CODEBERG_DOWN,
                               "gallery": "ComparisonError: Manifest generation error: bad patch"})
        self.assertEqual("failure", outcome.state)


if __name__ == "__main__":
    unittest.main()
