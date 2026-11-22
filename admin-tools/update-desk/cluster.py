"""The community's cluster, through the Operator's own kubectl and kubeconfig.

Everything here is something the Operator could type: each action returns the
command it ran, and the page shows it. The Desk holds no credential of its own.

Actions are limited to what an update needs: move the root Application's pin to
an overlay commit, ask Argo CD to refresh, and take a CNPG Backup. Nothing is
deleted.
"""
from __future__ import annotations

import json
import os
import shlex
import subprocess
import time

ROOT_APP = os.environ.get("SMALLWORLDS_ROOT_APP", "smallworlds-root")
CNPG = "clusters.postgresql.cnpg.io"
BACKUPS = "backups.postgresql.cnpg.io"
LABEL = "smallworlds.network/update-desk"


class Cluster:
    def __init__(self, kubeconfig: str | None, run=subprocess.run):
        self.kubeconfig = kubeconfig
        self._run = run
        self.last_error: str | None = None

    def command(self, *args: str) -> list[str]:
        prefix = ["kubectl"]
        if self.kubeconfig:
            prefix += ["--kubeconfig", self.kubeconfig]
        return prefix + list(args)

    def shown(self, *args: str) -> str:
        """The command as the Operator would type it, without the kubeconfig."""
        return shlex.join(["kubectl", *args])

    def kubectl(self, *args: str, input_text: str | None = None, timeout: int = 30):
        try:
            result = self._run(self.command(*args), capture_output=True, text=True,
                               input=input_text, timeout=timeout)
        except subprocess.TimeoutExpired:
            return 124, "", f"kubectl {args[0]} took longer than {timeout}s"
        except OSError as error:
            return 127, "", f"kubectl could not run: {error}"
        return result.returncode, result.stdout, result.stderr

    def _json(self, *args: str, timeout: int = 30) -> dict | None:
        code, out, err = self.kubectl(*args, "-o", "json", timeout=timeout)
        if code != 0:
            self.last_error = (err or out).strip()[:400]
            return None
        self.last_error = None
        try:
            return json.loads(out)
        except ValueError:
            self.last_error = "kubectl answered with something that is not JSON"
            return None

    # -- reading ---------------------------------------------------------------
    def applications(self) -> list[dict] | None:
        data = self._json("-n", "argocd", "get", "applications.argoproj.io", timeout=20)
        if data is None:
            return None
        found = []
        for item in data.get("items", []):
            spec, status = item.get("spec", {}), item.get("status", {})
            source = spec.get("source") or {}
            operation = status.get("operationState") or {}
            found.append({
                "name": item["metadata"]["name"],
                "repoURL": source.get("repoURL"),
                "targetRevision": source.get("targetRevision"),
                "chart": source.get("chart"),
                "sync": (status.get("sync") or {}).get("status"),
                "revision": (status.get("sync") or {}).get("revision"),
                "health": (status.get("health") or {}).get("status"),
                "operation": {"phase": operation.get("phase"),
                              "message": (operation.get("message") or "")[:300],
                              "started": operation.get("startedAt"),
                              "finished": operation.get("finishedAt")},
                "reconciled": status.get("reconciledAt"),
                "namespace": (spec.get("destination") or {}).get("namespace"),
            })
        return sorted(found, key=lambda a: a["name"])

    def databases(self) -> list[dict] | None:
        data = self._json("get", CNPG, "-A", timeout=20)
        if data is None:
            return None
        found = []
        for item in data.get("items", []):
            plugins = (item.get("spec") or {}).get("plugins") or []
            archiver = next((p.get("name") for p in plugins if p.get("isWALArchiver")), None)
            found.append({"namespace": item["metadata"]["namespace"], "name": item["metadata"]["name"],
                          "phase": (item.get("status") or {}).get("phase"), "plugin": archiver})
        return found

    def backups(self) -> list[dict] | None:
        """Backups this Desk took, and the ones the pre-update hook took."""
        found = []
        for selector in (LABEL, "smallworlds.network/pre-update"):
            data = self._json("get", BACKUPS, "-A", "-l", selector, timeout=20)
            if data is None:
                return None
            for item in data.get("items", []):
                status = item.get("status") or {}
                found.append({"namespace": item["metadata"]["namespace"],
                              "name": item["metadata"]["name"],
                              "cluster": ((item.get("spec") or {}).get("cluster") or {}).get("name"),
                              "created": item["metadata"].get("creationTimestamp"),
                              "phase": status.get("phase"), "error": status.get("error"),
                              "by": "desk" if selector == LABEL else "pre-update hook",
                              "for": (item["metadata"].get("labels") or {}).get(LABEL)})
        return sorted(found, key=lambda b: b["created"] or "", reverse=True)

    # -- acting ----------------------------------------------------------------
    def move_root_pin(self, overlay_sha: str) -> dict:
        patch = json.dumps({"spec": {"source": {"targetRevision": overlay_sha}}})
        args = ("-n", "argocd", "patch", "applications.argoproj.io", ROOT_APP, "--type", "merge", "-p", patch)
        code, out, err = self.kubectl(*args)
        return {"ok": code == 0, "command": self.shown(*args), "output": (out or err).strip()}

    def refresh(self, names: list[str], hard: bool = True) -> dict:
        kind = "hard" if hard else "normal"
        results, commands = [], []
        for name in names:
            args = ("-n", "argocd", "annotate", "applications.argoproj.io", name,
                    f"argocd.argoproj.io/refresh={kind}", "--overwrite")
            code, out, err = self.kubectl(*args)
            commands.append(self.shown(*args))
            if code != 0:
                results.append(f"{name}: {(err or out).strip()[:200]}")
        return {"ok": not results, "command": "\n".join(commands), "output": "\n".join(results)}

    def take_backup(self, namespace: str, cluster: str, plugin: str | None, step: str) -> dict:
        name = f"desk-{step[:12]}-{time.strftime('%Y%m%d%H%M%S', time.gmtime())}"
        manifest = {"apiVersion": "postgresql.cnpg.io/v1", "kind": "Backup",
                    "metadata": {"name": name, "namespace": namespace, "labels": {LABEL: step[:12]}},
                    "spec": {"cluster": {"name": cluster}}}
        if plugin:
            manifest["spec"]["method"] = "plugin"
            manifest["spec"]["pluginConfiguration"] = {"name": plugin}
        code, out, err = self.kubectl("create", "-f", "-", input_text=json.dumps(manifest))
        return {"ok": code == 0, "name": name,
                "command": f"kubectl -n {namespace} create -f - <<< '{json.dumps(manifest)}'",
                "output": (out or err).strip()}
