"""Running the two setup scripts, and streaming what they say to the browser.

The wizard does not install anything itself. It renders the answers into the
two files the scripts already cache into, runs those scripts with
SMALLWORLDS_NONINTERACTIVE=1, and turns their output into a stage list and a
log. Everything that decides what a cluster becomes stays in the scripts.

Standard library only. See plans-and-walkthroughs/design-setup-wizard.md.
"""

from __future__ import annotations

import os
import queue
import re
import signal
import subprocess
import tempfile
import threading
import time

import plan as planning

# Ordered, with the label the browser shows. Which ones actually apply depends
# on the answers -- expected_stages() decides that, so the operator sees the
# whole journey up front instead of stages appearing out of nowhere.
STAGE_LABELS = {
    "repo-init": "Initialise the settings repository",
    "repo-apps": "Write the application overlays",
    "repo-commit": "Commit the configuration",
    "repo-push": "Push it to your Git host",
    "dns-zone": "Prepare the DNS zone",
    "ssh-key": "Register the admin SSH key",
    "primary-ip": "Reserve the public address",
    "terraform-apply": "Create the server",
    "bootstrap": "Install k3s and Argo CD",
    "secrets": "Plant the cluster credentials",
    "kubeconfig": "Retrieve cluster access",
    "argocd-converge": "Wait for the applications",
}

HETZNER_STAGES = ["repo-init", "repo-apps", "repo-commit", "repo-push",
                  "dns-zone", "ssh-key", "primary-ip", "terraform-apply",
                  "secrets", "kubeconfig", "argocd-converge"]
LOCAL_STAGES = ["repo-init", "repo-apps", "repo-commit", "repo-push",
                "dns-zone", "bootstrap", "kubeconfig", "argocd-converge"]

_ANSI = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
_STEP = re.compile(r"^>>> step:([a-z0-9-]+)\s*(.*)$")
_OK = re.compile(r"^>>> ok:([a-z0-9-]+)")
_FAIL = re.compile(r"^>>> fail:([a-z0-9-]+)\s*(.*)$")
_MISSING = re.compile(r"^!!! missing:([A-Za-z_][A-Za-z0-9_]*)")
# The installer's convergence loop: "  [7/120] 12/19 apps healthy (root: ...)"
_CONVERGENCE = re.compile(r"^\s*\[\d+/\d+\]\s+(\d+)/(\d+)\s+apps healthy")
# What the install produced: ">>> result\t<kind>\t<name>\t<value>". Emitted by
# sw_result rather than scraped out of the installer's prose, so rewording a
# message cannot silently empty the handover screen.
_RESULT = re.compile(r"^>>> result\t([a-z]+)\t([^\t]*)\t(.*)$")
# What the operator can do about a failure, in the installer's own words:
# ">>> guidance\t<heading>\t<detail>". Emitted by sw_guidance, so the advice a
# person reads in the terminal and the advice this page shows are the same
# sentences -- see admin-tools/lib/ask.sh.
_GUIDANCE = re.compile(r"^>>> guidance\t([^\t]*)\t(.*)$")

MAX_LOG_LINES = 4000


def expected_stages(answers: dict) -> list[str]:
    stages = list(HETZNER_STAGES if answers.get("target") == "hetzner" else LOCAL_STAGES)
    # A LAN cluster that neither wants real certificates nor public access
    # never touches DNS.
    if answers.get("target") == "local" and \
            answers.get("local_tls") != "letsencrypt" and \
            answers.get("local_public") != "yes":
        stages.remove("dns-zone")
    return stages


# ---------------------------------------------------------------------------
# Writing the two cache files
# ---------------------------------------------------------------------------

def _shell_quote(value: str) -> str:
    """Both files are sourced by bash, so a value has to survive that exactly.
    Single quotes with the standard '"'"' escape: no expansion, no command
    substitution, nothing a domain or a token can do to the script."""
    return "'" + str(value or "").replace("'", "'\"'\"'") + "'"


def write_repository_config(answers: dict, path: str, apps: list[str]) -> None:
    """The answers prepare-community-repo.sh reads back as its defaults."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    lines = [
        f"STORED_REPO_PATH={_shell_quote(answers['repo_path'])}",
        f"STORED_REMOTE_URL={_shell_quote(answers['remote_url'])}",
        f"STORED_VERSION={_shell_quote(answers['version'])}",
        f"STORED_TARGET_DOMAIN={_shell_quote(answers['domain'])}",
        f"STORED_ENV_EXT={_shell_quote(answers.get('env_ext', ''))}",
        # The wizard creates the repository itself through the Git host's API,
        # so the script never shells out to `gh` -- one less tool an operator
        # has to have installed, and one less way to be half-authenticated.
        "STORED_CREATE_REPO='n'",
        "STORED_PUSH_CHOICE='y'",
    ]
    for app in planning.OPTIONAL_APPS:
        key = planning._app_preference_key(app["name"])
        lines.append(f"{key}={'y' if app['name'] in apps else 'n'}")
    _write_private(path, "\n".join(lines) + "\n")


def write_installer_cache(answers: dict, path: str, token_source: dict) -> None:
    """The answers smallworlds-init.sh reads back.

    Existing generated credentials are preserved: the file is the only place
    the Keycloak, Argo CD and Garage passwords live before the node exists, and
    regenerating them on a re-run would lock the operator out of a cluster that
    is already up.
    """
    existing = planning._read_shell_assignments(path)

    values = {
        "DEPLOY_TARGET": answers["target"],
        "LOCAL_SSH_TARGET": answers.get("ssh_target", ""),
        "DATA_DIR": answers.get("data_dir", ""),
        "BACKUP_DIR": answers.get("backup_dir", ""),
        "PROFILE_ID": existing.get("PROFILE_ID", ""),
        "BOOTSTRAP_RELEASE": existing.get("BOOTSTRAP_RELEASE", ""),
        "LOCAL_PUBLIC": answers.get("local_public", "no"),
        "LOCAL_TLS": answers.get("local_tls", "letsencrypt"),
        "DOMAIN": answers["domain"],
        "ENV_EXT": answers.get("env_ext", ""),
        "ADMIN_EMAIL": answers["admin_email"],
        "ONBOARDING_MODE": answers["onboarding_mode"],
        "HCLOUD_TOKEN": token_source.get("HCLOUD_TOKEN", ""),
        "GITOPS_REPO_URL": answers["remote_url"],
        "GITOPS_REPO_USER": answers.get("git_username") or "x-access-token",
        "GITOPS_REPO_TOKEN": token_source.get("GITOPS_REPO_TOKEN", ""),
    }
    # Anything the installer generates rather than asks for.
    for generated in ("KC_PASS", "KC_OPERATOR_PASS", "INVITE_SECRET",
                      "GARAGE_RPC_SECRET", "CONSOLE_SESSION_KEY",
                      "GARAGE_ADMIN_TOKEN", "GARAGE_BACKUP_RPC_SECRET",
                      "GARAGE_BACKUP_ADMIN_TOKEN", "GRAFANA_PASS"):
        values[generated] = existing.get(generated, "")

    _write_private(path, "".join(
        f"{key}={_shell_quote(value)}\n" for key, value in values.items()))


def _write_private(path: str, content: str) -> None:
    """Created 0600 from the start: a token must not exist world-readable even
    for the moment between writing and chmod."""
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w") as handle:
        handle.write(content)
    os.chmod(path, 0o600)


# ---------------------------------------------------------------------------
# A run
# ---------------------------------------------------------------------------

class Run:
    """One installation attempt, streamed to any number of watchers.

    The log is kept in memory and never written to disk. Reconnecting replays
    it, so closing the browser tab mid-install loses nothing.
    """

    def __init__(self, answers: dict, secrets: dict, repo_root: str):
        self.answers = answers
        self.secrets = secrets
        self.repo_root = repo_root
        self.status = "starting"       # starting | running | succeeded | failed | cancelled
        self.stages = [
            {"id": stage, "label": STAGE_LABELS[stage], "state": "pending", "note": ""}
            for stage in expected_stages(answers)
        ]
        self.log: list[str] = []
        self.error = ""
        self.guidance: list[dict] = []
        self.progress: dict | None = None
        self.result: dict = {}
        self.started_at = time.time()
        self.finished_at: float | None = None

        self._lock = threading.Lock()
        self._watchers: list[queue.Queue] = []
        self._process: subprocess.Popen | None = None
        self._cancelled = False
        self._askpass: str | None = None
        self._offered: set[str] | None = None

        # Every value that must never appear in the log, longest first so a
        # token containing another is masked whole.
        self._redactions = sorted(
            {value for value in secrets.values() if value and len(value) >= 8},
            key=len, reverse=True)

    # -------------------------------------------------------------- streaming

    def subscribe(self) -> queue.Queue:
        watcher: queue.Queue = queue.Queue()
        with self._lock:
            watcher.put({"event": "snapshot", "data": self.snapshot()})
            self._watchers.append(watcher)
        return watcher

    def unsubscribe(self, watcher: queue.Queue) -> None:
        with self._lock:
            if watcher in self._watchers:
                self._watchers.remove(watcher)

    def _emit(self, event: str, data) -> None:
        with self._lock:
            for watcher in list(self._watchers):
                watcher.put({"event": event, "data": data})

    def snapshot(self) -> dict:
        return {
            "status": self.status,
            "stages": self.stages,
            "log": self.log[-MAX_LOG_LINES:],
            "error": self.error,
            "guidance": self.guidance,
            "progress": self.progress,
            "result": self.result,
        }

    # ------------------------------------------------------------- redaction

    def redact(self, line: str) -> str:
        for secret in self._redactions:
            if secret in line:
                line = line.replace(secret, "[redacted]")
        return line

    # ---------------------------------------------------------------- stages

    def _set_stage(self, stage_id: str, state: str, note: str = "") -> None:
        for stage in self.stages:
            if stage["id"] == stage_id:
                stage["state"] = state
                if note:
                    stage["note"] = note
                self._emit("stage", stage)
                return
        # A stage nobody predicted still deserves to be shown rather than
        # silently dropped -- being surprised is better than being misinformed.
        stage = {"id": stage_id, "label": STAGE_LABELS.get(stage_id, stage_id),
                 "state": state, "note": note}
        self.stages.append(stage)
        self._emit("stage", stage)

    def _consume(self, raw: str) -> None:
        line = self.redact(_ANSI.sub("", raw.rstrip("\n")))

        match = _RESULT.match(line)
        if match:
            self._record_result(match.group(1), match.group(2), match.group(3))
            return
        match = _GUIDANCE.match(line)
        if match:
            self._add_guidance(match.group(1), match.group(2))
            return
        match = _STEP.match(line)
        if match:
            self._set_stage(match.group(1), "running", match.group(2))
            return
        match = _OK.match(line)
        if match:
            self._set_stage(match.group(1), "done")
            return
        match = _FAIL.match(line)
        if match:
            self._set_stage(match.group(1), "failed", match.group(2))
            return
        match = _MISSING.match(line)
        if match:
            self.error = (f"The installer needed {match.group(1)} and it was not "
                          "supplied. Go back and fill it in.")
            return
        match = _CONVERGENCE.match(line)
        if match:
            self.progress = {"healthy": int(match.group(1)), "total": int(match.group(2))}
            self._emit("progress", self.progress)
            return

        self.log.append(line)
        if len(self.log) > MAX_LOG_LINES:
            del self.log[: len(self.log) - MAX_LOG_LINES]
        self._emit("log", line)

    def _add_guidance(self, heading: str, detail: str) -> None:
        """Advice arrives while the script is still running, before the exit
        status is known. Kept in order, and de-duplicated: a re-run of the same
        check must not stack the same paragraph twice."""
        if not heading:
            return
        if any(entry["heading"] == heading for entry in self.guidance):
            return
        self.guidance.append({"heading": heading, "detail": detail})
        self._emit("guidance", self.guidance)

    def _offered_urls(self) -> set[str]:
        """Always installed, plus whatever was chosen."""
        if self._offered is None:
            labels = {app["name"]: app["label"] for app in planning.OPTIONAL_APPS}
            self._offered = {"Dashboard", "Identity", "Monitoring"} | {
                labels[app] for app in (self.answers.get("apps") or []) if app in labels}
        return self._offered

    def _record_result(self, kind: str, name: str, value: str) -> None:
        """What the install produced. Credentials are held in memory for the
        handover screen and nothing else — the wizard never writes them
        anywhere, and the operator's copy is whatever they save."""
        if kind == "credential":
            for entry in self.result.setdefault("credentials", []):
                if entry["name"] == name:
                    entry["value"] = value
                    break
            else:
                self.result["credentials"].append(
                    {"name": name, "value": value, "note": ""})
        elif kind == "note":
            for entry in self.result.setdefault("credentials", []):
                if entry["name"] == name:
                    entry["note"] = value
        elif kind == "url":
            # The installer prints every application's address whether or not
            # it was selected. Listing an address for something this community
            # did not install would be a link to a certificate error.
            if name not in self._offered_urls():
                return
            urls = self.result.setdefault("urls", [])
            if not any(entry["name"] == name for entry in urls):
                urls.append({"name": name, "url": value})
        elif kind in ("kubeconfig", "hosts"):
            self.result[kind] = value
        self._emit("result", self.result)

    # -------------------------------------------------------------- execution

    def _environment(self) -> dict:
        environment = dict(os.environ)
        environment["SMALLWORLDS_NONINTERACTIVE"] = "1"
        environment["SMALLWORLDS_MARKERS"] = "1"
        # No script may block on a credential prompt: there is no terminal to
        # answer it, so it would hang until the operator gave up.
        environment["GIT_TERMINAL_PROMPT"] = "0"
        environment["DEBIAN_FRONTEND"] = "noninteractive"
        if self.secrets.get("GITOPS_REPO_TOKEN"):
            environment["GIT_ASKPASS"] = self._askpass_helper()
            environment["GIT_USERNAME"] = self.answers.get("git_username") or "x-access-token"
            environment["GIT_PASSWORD"] = self.secrets["GITOPS_REPO_TOKEN"]
        return environment

    def _askpass_helper(self) -> str:
        """git asks this for credentials, so the token never appears in a URL —
        a URL reaches the process list and git's own error output."""
        if self._askpass and os.path.exists(self._askpass):
            return self._askpass
        handle = tempfile.NamedTemporaryFile("w", suffix="-askpass.sh", delete=False)
        handle.write(
            "#!/bin/sh\n"
            "case \"$1\" in\n"
            "  *[Uu]sername*) printf '%s' \"$GIT_USERNAME\" ;;\n"
            "  *) printf '%s' \"$GIT_PASSWORD\" ;;\n"
            "esac\n"
        )
        handle.close()
        os.chmod(handle.name, 0o700)
        self._askpass = handle.name
        return self._askpass

    def _run_script(self, command: list[str], label: str) -> int:
        if self._cancelled:
            return 130
        self._process = subprocess.Popen(
            command, cwd=self.repo_root, env=self._environment(),
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT, text=True, bufsize=1,
            # Its own process group, so cancelling stops terraform and ssh too
            # rather than orphaning them.
            start_new_session=True,
        )
        assert self._process.stdout is not None
        for line in self._process.stdout:
            self._consume(line)
        return self._process.wait()

    def cancel(self) -> bool:
        self._cancelled = True
        process = self._process
        if process and process.poll() is None:
            try:
                os.killpg(os.getpgid(process.pid), signal.SIGTERM)
            except (ProcessLookupError, PermissionError):
                return False
            return True
        return False

    def start(self) -> None:
        threading.Thread(target=self._execute, daemon=True).start()

    def _execute(self) -> None:
        try:
            self.status = "running"
            self._emit("status", self.status)

            code = self._run_script(
                ["bash", os.path.join(self.repo_root, "admin-tools",
                                      "prepare-community-repo.sh")],
                "settings repository")
            if code != 0:
                self._fail(code, "writing the settings repository")
                return

            code = self._run_script(
                ["bash", os.path.join(self.repo_root, "smallworlds-init.sh")],
                "installer")
            if code != 0:
                self._fail(code, "installing the cluster")
                return

            self.status = "succeeded"
            self.finished_at = time.time()
            self._emit("status", self.status)
        except Exception as error:  # noqa: BLE001 - the browser must hear about it
            self.error = f"The wizard itself failed: {error}"
            self.status = "failed"
            self.finished_at = time.time()
            self._emit("status", self.status)
        finally:
            if self._askpass and os.path.exists(self._askpass):
                os.unlink(self._askpass)
            self._emit("done", self.snapshot())

    def _fail(self, code: int, during: str) -> None:
        self.finished_at = time.time()
        if self._cancelled:
            self.status = "cancelled"
            self.error = "Stopped at your request. Nothing was rolled back — " \
                         "run the wizard again to carry on where it left off."
        else:
            self.status = "failed"
            if code == 78 and not self.error:
                self.error = "An answer the installer needed was missing."
            elif not self.error:
                self.error = f"Failed while {during} (exit status {code}). " \
                             "The log below has the details."
        for stage in self.stages:
            if stage["state"] == "running":
                stage["state"] = "failed"
        if self.status == "failed":
            self._add_fallback_guidance()
        self._emit("status", self.status)

    # Advice for the failures the scripts do not recognise themselves. The
    # scripts speak first (sw_guidance), because they know what they were
    # doing; this is what is left to say when they did not.
    _FALLBACK_GUIDANCE = {
        "repo-push": (
            "Check what the Git token may do",
            "Pushing the overlay needs Contents: read/write on the repository "
            "(and Pull requests: read/write for the weekly update proposals). "
            "A token that passed the earlier check can still be missing those.",
        ),
        "dns-zone": (
            "Check the Hetzner token and the domain",
            "The zone is created through the Hetzner Cloud API with the token "
            "from the second panel, which needs Read & Write. If the zone "
            "already exists in another Hetzner project, this token cannot see "
            "it.",
        ),
        "argocd-converge": (
            "The cluster is up and still working",
            "Convergence timing out is not the same as the install failing: "
            "Argo CD keeps retrying on its own. Watch it with "
            "kubectl get application -n argocd.",
        ),
    }

    def _add_fallback_guidance(self) -> None:
        if self.guidance:
            return
        failed = next((stage for stage in self.stages
                       if stage["state"] == "failed"), None)
        specific = self._FALLBACK_GUIDANCE.get(failed["id"]) if failed else None
        if specific:
            self._add_guidance(*specific)
        self._add_guidance(
            "Read the end of the log",
            "Open \u201cShow what the installer is doing\u201d below. The last "
            "few lines carry the actual error; everything above it succeeded.",
        )
        self._add_guidance(
            "Nothing has to be undone",
            "Fix what the error names and run ./smallworlds-setup again. Every "
            "step is written to be safe to repeat \u2014 what already exists is "
            "found and reused, not created a second time.",
        )
