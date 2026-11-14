"""A check run as a Job on Ring 0: the e2e with full OIDC and the data checks
(design §3.5 steps 3–4, CronJob `canary-checks`), and the storage checks
(CronJob `canary-storage-checks`, checks/storage-checks.sh).

The Job is cut from a suspended CronJob (checks-job.yaml, storage-checks-job.yaml
next to this tenant), so its image and resources live in a manifest Renovate can
update, not in Python. Its label `app` is the CronJob's name, so each kind clears
only its own earlier Jobs. The follower only names the commit: CHECK_SHA is set on every
container, and the Job fetches that commit's e2e-tests/ and admin-tools/canary/,
so the checks always match the code they verify.

The Job's last line starting with "CHECKS " is its summary; it goes into the
commit status. Pass or fail is the Job's own outcome, except that a Job that
could not get as far as checking anything (the commit not fetched, npm or the
browser unavailable) says "CHECKS-INFRA ": that is no verdict on the commit, and
the follower reports an error instead of rejecting it.
"""
import logging
import time

log = logging.getLogger("checksjob")

# A pod that cannot start: no Secret, no image. Nothing will change by waiting
# out the Job's deadline (an hour, Ring 0's first night: no Secret canary-checks).
STUCK_REASONS = ("CreateContainerConfigError", "CreateContainerError", "ErrImagePull",
                 "ImagePullBackOff", "InvalidImageName")


class ChecksJob:
    def __init__(self, kube, namespace: str, cronjob: str, timeout: int,
                 clock=time.monotonic, sleep=time.sleep, now=time.time, stuck_seconds: int = 300):
        self.stuck_seconds = stuck_seconds
        self.kube = kube
        self.namespace = namespace
        self.cronjob = cronjob
        self.timeout = timeout
        self.clock = clock
        self.sleep = sleep
        self.now = now
        self.jobs = f"/apis/batch/v1/namespaces/{namespace}/jobs"
        self.label = cronjob

    def _job(self, sha: str) -> dict:
        template = self.kube.get(f"/apis/batch/v1/namespaces/{self.namespace}/cronjobs/{self.cronjob}")
        spec = template["spec"]["jobTemplate"]["spec"]
        pod = spec["template"]["spec"]
        for container in pod.get("initContainers", []) + pod.get("containers", []):
            env = [e for e in container.get("env", []) if e.get("name") != "CHECK_SHA"]
            container["env"] = env + [{"name": "CHECK_SHA", "value": sha}]
        name = f"{self.label}-{sha[:12]}-{int(self.now()) % 1000000:06d}"
        labels = {**template["spec"]["jobTemplate"].get("metadata", {}).get("labels", {}),
                  "app": self.label, "smallworlds.network/sha": sha[:12]}
        return {"apiVersion": "batch/v1", "kind": "Job",
                "metadata": {"name": name, "namespace": self.namespace, "labels": labels},
                "spec": spec}

    def _clear(self) -> None:
        """One run at a time: a follower restarted mid-run leaves its Job behind.

        Failed Jobs of earlier runs go too. Their verdict is already posted, and
        each keeps a KubeJobFailed alert firing until its TTL (three days): on
        Ring 0, 2026-11-13, five of them grouped into one notification too large
        for ntfy, and the delivery failure became a critical alert that rejected
        every commit after them. Succeeded Jobs raise nothing and stay."""
        for job in self.kube.get(f"{self.jobs}?labelSelector=app={self.label}").get("items", []):
            status = job.get("status", {})
            if status.get("active"):
                log.info("stopping the earlier checks Job %s", job["metadata"]["name"])
            elif status.get("failed"):
                log.info("removing the failed checks Job %s", job["metadata"]["name"])
            else:
                continue
            self.kube.delete(f"{self.jobs}/{job['metadata']['name']}")

    def _summary(self, name: str) -> tuple[bool, str]:
        """(the Job got to checking, its summary line)."""
        try:
            pods = self.kube.get(f"/api/v1/namespaces/{self.namespace}/pods"
                                 f"?labelSelector=job-name={name}").get("items", [])
            pods.sort(key=lambda p: p["metadata"].get("creationTimestamp", ""))
            if not pods:
                return False, "no pod"
            log_text = self.kube.text(f"/api/v1/namespaces/{self.namespace}/pods/"
                                      f"{pods[-1]['metadata']['name']}/log?tailLines=200")
        except Exception as error:
            return False, f"log unreadable ({type(error).__name__})"
        found = [(not l.startswith("CHECKS-INFRA "), l.split(" ", 1)[1].strip())
                 for l in log_text.splitlines() if l.startswith(("CHECKS ", "CHECKS-INFRA "))]
        return found[-1] if found else (True, "no summary in the log")

    def _stuck(self, name: str) -> str | None:
        """Why the Job's pod cannot start, or None."""
        try:
            pods = self.kube.get(f"/api/v1/namespaces/{self.namespace}/pods"
                                 f"?labelSelector=job-name={name}").get("items", [])
        except Exception:
            return None
        for pod in pods:
            for status in pod.get("status", {}).get("containerStatuses", []):
                waiting = status.get("state", {}).get("waiting", {})
                if waiting.get("reason") in STUCK_REASONS:
                    return f"{waiting['reason']}: {waiting.get('message', '')}".rstrip(": ")[:160]
        return None

    def run(self, sha: str) -> tuple[bool | None, str]:
        """(passed, summary); passed is None when nothing could be checked."""
        self._clear()
        job = self._job(sha)
        name = job["metadata"]["name"]
        self.kube.create(self.jobs, job)
        log.info("Job %s started", name)
        started = self.clock()
        deadline = started + self.timeout
        while True:
            status = self.kube.get(f"{self.jobs}/{name}").get("status", {})
            conditions = {c["type"]: c.get("status") for c in status.get("conditions", [])}
            if conditions.get("Complete") == "True" or status.get("succeeded"):
                return True, self._summary(name)[1]
            if conditions.get("Failed") == "True":
                checked, summary = self._summary(name)
                return (False if checked else None), summary
            if self.clock() - started >= self.stuck_seconds and (why := self._stuck(name)):
                self.kube.delete(f"{self.jobs}/{name}")
                return None, f"the checks pod cannot start: {why}"
            if self.clock() >= deadline:
                _, summary = self._summary(name)
                self.kube.delete(f"{self.jobs}/{name}")
                # Hung while checking: an application that never answers is a
                # finding, so this rejects the commit.
                return False, f"timed out after {self.timeout // 60} min; {summary}"
            self.sleep(30)
