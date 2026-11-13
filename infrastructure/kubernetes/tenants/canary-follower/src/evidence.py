"""The evidence bundle a stuck Ring 0 hands to the Ring 0 Steward (design §5.1).

Collected at the moment the follower notices, while the cluster still shows the
problem (a rejected update is reverted a few minutes later): the Applications
that are not Synced and Healthy, what their last operation said, the Warning
events and failing pods in their namespaces with the end of each log, and the
end of the checks Job's log.

It goes into an issue in the *private* Ring 0 overlay repository. Logs can still
carry secrets, so obvious ones are masked here as well; the follower can read
no Secret itself (rbac.yaml), only what pods print.
"""
import logging
import re

log = logging.getLogger("evidence")

LIMIT = 60000  # GitHub refuses an issue body or comment above 65536 characters
LOG_LINES = 200
MAX_PODS = 6
MAX_EVENTS = 30

_MASKS = [
    (re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", re.S),
     "<private key masked>"),
    (re.compile(r"\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})"), "<github token masked>"),
    (re.compile(r"(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}"), r"\1 <masked>"),
    (re.compile(r"\b(AKIA[0-9A-Z]{16}|GK[0-9a-f]{24})\b"), "<access key masked>"),
    (re.compile(r"(?i)\b([\w.-]*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)[\w.-]*)"
                r"(\"?\s*[:=]\s*\"?)([^\s\"',;&]{4,})"), r"\1\2<masked>"),
    (re.compile(r"(?i)(://[^:/\s@]+:)([^@\s/]{4,})@"), r"\1<masked>@"),
]


def mask(text: str) -> str:
    for pattern, replacement in _MASKS:
        text = pattern.sub(replacement, text)
    return text


def _fence(text: str, limit: int) -> str:
    text = text.strip("\n")
    if len(text) > limit:
        text = "…(earlier lines cut)\n" + text[-limit:]
    return "```\n" + text.replace("```", "ʼʼʼ") + "\n```"


def _status(app: dict) -> str:
    status = app.get("status", {})
    return f"{status.get('sync', {}).get('status', '?')}/{status.get('health', {}).get('status', '?')}"


def _troubled(app: dict) -> bool:
    status = app.get("status", {})
    phase = status.get("operationState", {}).get("phase")
    return (_status(app) != "Synced/Healthy" or phase in ("Failed", "Error")
            or any(c.get("type", "").endswith("Error") for c in status.get("conditions", [])))


def _namespace(app: dict) -> str | None:
    return app.get("spec", {}).get("destination", {}).get("namespace")


def _pod_problem(pod: dict) -> str | None:
    status = pod.get("status", {})
    if status.get("phase") in ("Succeeded",):
        return None
    if status.get("phase") == "Failed":
        return "Failed"
    for init, statuses in ((True, status.get("initContainerStatuses", [])),
                           (False, status.get("containerStatuses", []))):
        for c in statuses:
            state = c.get("state", {})
            if "waiting" in state and state["waiting"].get("reason") != "PodInitializing":
                return f"{c['name']}: {state['waiting'].get('reason', 'waiting')}"
            if "terminated" in state and state["terminated"].get("exitCode", 0) != 0:
                return f"{c['name']}: exited {state['terminated'].get('exitCode')}"
            if not init and not c.get("ready") and "running" in state:
                return f"{c['name']}: not ready"
            if c.get("restartCount", 0) >= 3:
                return f"{c['name']}: {c['restartCount']} restarts"
    return None


class Evidence:
    def __init__(self, kube, argocd, checks_namespace: str = "canary-follower"):
        self.kube = kube
        self.argocd = argocd
        self.checks_namespace = checks_namespace

    def _section(self, title: str, collect) -> str:
        try:
            body = collect()
        except Exception as error:  # evidence is best effort: say what was missing
            log.warning("evidence: %s unreadable: %s", title, error)
            body = f"_unreadable: {type(error).__name__}: {error}_"
        return f"### {title}\n\n{body or '_nothing to report_'}\n"

    def _applications(self, troubled: list[dict]) -> str:
        parts = []
        for app in troubled:
            status = app.get("status", {})
            op = status.get("operationState", {})
            lines = [f"**{app['metadata']['name']}** — {_status(app)}"
                     + (f", last operation {op.get('phase')}: {op.get('message', '')[:600]}" if op else "")]
            for c in status.get("conditions", []):
                lines.append(f"- condition {c.get('type')}: {c.get('message', '')[:600]}")
            for r in status.get("resources", []):
                health = r.get("health", {})
                if health.get("status") not in (None, "Healthy") or r.get("status") == "OutOfSync":
                    lines.append(f"- {r.get('kind')} {r.get('namespace', '')}/{r.get('name')}: "
                                 f"{r.get('status', '')} {health.get('status', '')} {health.get('message', '')[:300]}")
            for res in op.get("syncResult", {}).get("resources", []):
                if res.get("status") not in ("Synced", None) or res.get("hookPhase") in ("Failed", "Error"):
                    lines.append(f"- sync result {res.get('kind')} {res.get('name')}: "
                                 f"{res.get('status', '')} {res.get('hookPhase', '')} {res.get('message', '')[:300]}")
            parts.append("\n".join(lines))
        return "\n\n".join(parts)

    def _events(self, namespace: str) -> str:
        items = self.kube.get(f"/api/v1/namespaces/{namespace}/events?fieldSelector=type%3DWarning"
                              ).get("items", [])
        items.sort(key=lambda e: e.get("lastTimestamp") or e.get("eventTime") or "")
        lines = [f"{e.get('lastTimestamp') or e.get('eventTime') or ''} "
                 f"{e.get('involvedObject', {}).get('kind', '')}/{e.get('involvedObject', {}).get('name', '')} "
                 f"{e.get('reason', '')}: {e.get('message', '')} (x{e.get('count', 1)})"
                 for e in items[-MAX_EVENTS:]]
        return _fence("\n".join(lines), 6000) if lines else ""

    def _pods(self, namespace: str, budget: list[int]) -> str:
        pods = self.kube.get(f"/api/v1/namespaces/{namespace}/pods").get("items", [])
        parts = []
        for pod in pods:
            problem = _pod_problem(pod)
            if problem is None or budget[0] <= 0:
                continue
            budget[0] -= 1
            name = pod["metadata"]["name"]
            parts.append(f"**{name}** — {problem}")
            statuses = (pod.get("status", {}).get("initContainerStatuses", [])
                        + pod.get("status", {}).get("containerStatuses", []))
            for c in statuses:
                if c.get("ready") and not c.get("restartCount"):
                    continue
                previous = "&previous=true" if c.get("restartCount") and "waiting" in c.get("state", {}) else ""
                try:
                    text = self.kube.text(f"/api/v1/namespaces/{namespace}/pods/{name}/log"
                                          f"?container={c['name']}&tailLines={LOG_LINES}{previous}")
                except Exception as error:
                    text = f"(log unreadable: {error})"
                parts.append(f"container `{c['name']}`{' (previous run)' if previous else ''}:\n"
                             + _fence(text, 5000))
        return "\n\n".join(parts)

    def _checks(self) -> str:
        jobs = self.kube.get(f"/apis/batch/v1/namespaces/{self.checks_namespace}/jobs"
                             "?labelSelector=app%3Dcanary-checks").get("items", [])
        if not jobs:
            return ""
        jobs.sort(key=lambda j: j["metadata"].get("creationTimestamp", ""))
        name = jobs[-1]["metadata"]["name"]
        pods = self.kube.get(f"/api/v1/namespaces/{self.checks_namespace}/pods"
                             f"?labelSelector=job-name%3D{name}").get("items", [])
        if not pods:
            return f"Job `{name}`: no pod left"
        pods.sort(key=lambda p: p["metadata"].get("creationTimestamp", ""))
        text = self.kube.text(f"/api/v1/namespaces/{self.checks_namespace}/pods/"
                              f"{pods[-1]['metadata']['name']}/log?tailLines=150")
        return f"Job `{name}`:\n" + _fence(text, 9000)

    def cluster(self) -> str:
        """What Ring 0 shows right now, as Markdown."""
        try:
            troubled = [a for a in self.argocd.list() if _troubled(a)]
        except Exception as error:
            log.warning("evidence: Applications unreadable: %s", error)
            troubled = []
        out = [self._section("Argo CD Applications that are not Synced and Healthy",
                             lambda: self._applications(troubled))]
        budget = [MAX_PODS]
        for namespace in sorted({ns for a in troubled if (ns := _namespace(a))}):
            out.append(self._section(f"Namespace `{namespace}`: Warning events",
                                     lambda ns=namespace: self._events(ns)))
            out.append(self._section(f"Namespace `{namespace}`: pods in trouble",
                                     lambda ns=namespace: self._pods(ns, budget)))
        out.append(self._section("The last checks Job (e2e and data checks)", self._checks))
        return "\n".join(out)


def bundle(header: str, change: str, cluster: str) -> str:
    """Join the parts, masked, and keep the whole under GitHub's limit."""
    text = mask(f"{header}\n\n{change}\n\n{cluster}")
    if len(text) > LIMIT:
        text = text[:LIMIT] + "\n\n…(cut: the evidence was longer than an issue can hold)\n"
    return text


def change_section(github, sha: str | None, pull: dict | None) -> str:
    """The commit or PR at the centre of the incident, with its diff."""
    if sha is None and pull is None:
        return ""
    lines = ["### The change"]
    try:
        if pull is not None:
            lines.append(f"PR #{pull['number']}: {pull.get('title', '')} ({pull.get('html_url', '')})")
            files = github.pull_files(pull["number"])
        else:
            files = []
        if sha:
            lines.append(f"Commit `{sha}`")
        patch = "\n".join(f"--- {f.get('filename')}\n{f.get('patch', '(no textual diff)')[:3000]}"
                          for f in files)
        if patch:
            lines.append(_fence(patch, 15000))
        elif sha:
            lines.append("Files: " + ", ".join(f"`{p}`" for p in github.changed_paths(sha)[:40]))
    except Exception as error:
        lines.append(f"_unreadable: {type(error).__name__}: {error}_")
    return "\n\n".join(lines)
