"""Open a memory-limit bump PR against the private overlay repo.

The overlay (my-community-config) is the only ref that deploys on merge, so
right-sizing is expressed there as a kustomize strategic-merge patch on the
tenant's Deployment — the same shape a human operator would hand-write. The PR
creates/updates `<app>/limits-<deployment>.yaml` and makes sure the app's
kustomization.yaml references it.

Uses the GitHub REST contents API over urllib — no third-party dependency. The
patch is emitted as a template string and the kustomization is edited by
targeted text insertion (not a YAML round-trip) so the operator's comments and
formatting in that hand-maintained file are preserved.

Idempotent: re-running for a still-open incident (e.g. after a pod restart that
cleared the in-process escalation state) reuses the existing branch and returns
the existing PR instead of erroring.
"""
import base64
import json
import logging
import urllib.error
import urllib.request

import config

log = logging.getLogger("overlay_pr")


class _GitHub:
    def __init__(self):
        self.repo = config.OVERLAY_REPO
        self.base = config.OVERLAY_BASE_BRANCH
        self._h = {
            "Authorization": f"Bearer {config.GITHUB_TOKEN}",
            "Accept": "application/vnd.github+json",
            "User-Agent": "tier1-remediation",
        }

    def _req(self, method: str, path: str, body: dict | None = None):
        url = f"{config.GITHUB_API}/repos/{self.repo}{path}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, headers={**self._h,
                                     "Content-Type": "application/json"}, method=method)
        with urllib.request.urlopen(req, timeout=20) as resp:
            return json.load(resp)

    def base_sha(self) -> str:
        return self._req("GET", f"/git/ref/heads/{self.base}")["object"]["sha"]

    def create_branch(self, name: str, sha: str) -> None:
        """Create the branch, or reuse it if a prior run already made it."""
        try:
            self._req("POST", "/git/refs", {"ref": f"refs/heads/{name}", "sha": sha})
        except urllib.error.HTTPError as e:
            if e.code == 422:  # Reference already exists — reuse it.
                log.info("branch %s already exists; reusing", name)
                return
            raise

    def get_file(self, path: str, ref: str):
        """Return (text, sha) or (None, None) if the file does not exist."""
        try:
            j = self._req("GET", f"/contents/{path}?ref={ref}")
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None, None
            raise
        return base64.b64decode(j["content"]).decode(), j["sha"]

    def put_file(self, path: str, text: str, message: str, branch: str, sha: str | None):
        body = {
            "message": message,
            "content": base64.b64encode(text.encode()).decode(),
            "branch": branch,
        }
        if sha:
            body["sha"] = sha
        self._req("PUT", f"/contents/{path}", body)

    def open_pr(self, title: str, head: str, body: str) -> str:
        """Open the PR, or return the existing open one for this branch."""
        try:
            pr = self._req("POST", "/pulls", {"title": title, "head": head,
                           "base": self.base, "body": body})
            return pr["html_url"]
        except urllib.error.HTTPError as e:
            if e.code == 422:  # A PR for this head already exists.
                owner = self.repo.split("/")[0]
                existing = self._req("GET", f"/pulls?head={owner}:{head}&state=open")
                if existing:
                    log.info("PR for %s already open; reusing", head)
                    return existing[0]["html_url"]
            raise


def _patch_yaml(deployment: str, container: str, new_limit: str) -> str:
    """Strategic-merge patch as a template string (deterministic, comment-safe —
    no YAML library needed)."""
    return (
        "# Managed by tier1-remediation (OOMKill right-sizing).\n"
        "# Bumps only the memory limit; safe to hand-edit or delete.\n"
        "apiVersion: apps/v1\n"
        "kind: Deployment\n"
        f"metadata:\n  name: {deployment}\n"
        "spec:\n  template:\n    spec:\n      containers:\n"
        f"        - name: {container}\n"
        "          resources:\n            limits:\n"
        f"              memory: {new_limit}\n"
    )


def _ensure_patch_referenced(kustomization_text: str, patch_path: str) -> str | None:
    """Add a reference to `patch_path` under the kustomization's patch list by
    targeted text insertion (preserving comments/formatting). Returns the new
    text, or None if it was already referenced. Handles both the modern
    `patches:` (list of maps) and legacy `patchesStrategicMerge:` (list of
    path strings) styles; falls back to appending a `patches:` block."""
    if patch_path in kustomization_text:
        return None  # already referenced

    lines = kustomization_text.splitlines()
    for i, line in enumerate(lines):
        stripped = line.strip()
        if stripped == "patches:":
            lines.insert(i + 1, f"  - path: {patch_path}")
            return "\n".join(lines) + "\n"
        if stripped == "patchesStrategicMerge:":
            lines.insert(i + 1, f"  - {patch_path}")
            return "\n".join(lines) + "\n"

    tail = "" if kustomization_text.endswith("\n") else "\n"
    return kustomization_text + f"{tail}patches:\n  - path: {patch_path}\n"


def open_limit_bump_pr(app: str, deployment: str, container: str,
                       new_limit: str, old_limit: str, context: str) -> str:
    """Create the branch, write the patch (+ kustomization reference), open the
    PR. Returns the PR URL. Honors config.DRY_RUN and is safe to re-run."""
    patch_path = f"limits-{deployment}.yaml"          # relative to the app dir
    full_patch = f"{app}/{patch_path}"
    kustomization = f"{app}/kustomization.yaml"
    branch = f"tier1/oom-{app}-{deployment}"
    title = f"fix({app}): raise {deployment}/{container} memory limit to {new_limit}"
    pr_body = (
        f"Automated Tier 1 remediation for a recurring OOMKill.\n\n"
        f"- **App:** `{app}`  **Workload:** `{deployment}`  **Container:** `{container}`\n"
        f"- **Memory limit:** `{old_limit}` → `{new_limit}`\n\n"
        f"{context}\n\n"
        f"Merging deploys immediately (ArgoCD watches this overlay). "
        f"If this does not stop the OOMKills, the alert re-fires and the "
        f"incident escalates to Hermes (Tier 2)."
    )

    if config.DRY_RUN:
        log.info("[DRY_RUN] would open PR on %s: %s (%s -> %s)",
                 config.OVERLAY_REPO, title, old_limit, new_limit)
        return "(dry-run, no PR opened)"

    gh = _GitHub()
    gh.create_branch(branch, gh.base_sha())

    _, patch_sha = gh.get_file(full_patch, branch)
    gh.put_file(full_patch, _patch_yaml(deployment, container, new_limit), title, branch, patch_sha)

    kustomization_text, kustomization_sha = gh.get_file(kustomization, branch)
    if kustomization_text is not None:
        updated = _ensure_patch_referenced(kustomization_text, patch_path)
        if updated:
            gh.put_file(kustomization, updated,
                        f"chore({app}): register {patch_path}", branch, kustomization_sha)
    else:
        log.warning("no kustomization.yaml at %s; patch file written but not wired", kustomization)

    return gh.open_pr(title, branch, pr_body)
