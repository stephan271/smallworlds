#!/usr/bin/env python3
"""Tests for the setup wizard's server, its defaults, and its dry-run plan.

Two things are being defended here. The first is the security posture: this
process holds a Hetzner token, a Git token and every generated cluster
password, so loopback binding, the session token, the Host-header guard and
"no secret ever reaches the browser" are all asserted rather than assumed.
The second is that the plan is a plan -- Phase 1 must not create anything.

Checks that need the network report what they could not determine instead of
failing: a restrictive resolver is not a broken wizard.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

WIZARD_DIRECTORY = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(WIZARD_DIRECTORY, "..", ".."))
sys.path.insert(0, WIZARD_DIRECTORY)

failures: list[str] = []
skipped: list[str] = []


def check(label: str, condition: bool, detail: str = ""):
    if condition:
        print(f"  ok    {label}")
    else:
        failures.append(label)
        print(f"  FAIL  {label}" + (f" — {detail}" if detail else ""))


def skip(label: str, why: str):
    skipped.append(label)
    print(f"  skip  {label} — {why}")


# ---------------------------------------------------------------------------
# Offline: defaults and validation
# ---------------------------------------------------------------------------

def test_plan_module():
    print("plan — defaults and validation")
    # An empty machine, so the assertions are about a first-time operator
    # rather than about whatever this developer happens to have cached.
    os.environ["SMALLWORLDS_COMMUNITY_CONFIG"] = "/nonexistent/community.conf"
    os.environ["SMALLWORLDS_INIT_CACHE"] = "/nonexistent/init.env"
    import plan

    state = plan.initial_state()
    check("no remembered domain on a new machine", state["domain"] == "")
    check("a machine is preselected", state["target"] == "hetzner")
    check("data and backup directories differ",
          state["data_dir"] != state["backup_dir"])
    check("certificates default to Let's Encrypt", state["local_tls"] == "letsencrypt")
    check("the cluster is not exposed by default", state["local_public"] == "no")
    check("onboarding defaults to invitation", state["onboarding_mode"] == "invitation")
    check("no saved secrets are reported",
          state["has_saved_hcloud_token"] is False and state["has_saved_git_token"] is False)

    # docs/adr/0049: mail is opt-in, because nothing in the cluster needs it.
    check("mail is off by default", "stalwart" not in state["apps"])
    check("webmail is off by default", "bulwark" not in state["apps"])
    check("a useful set is on by default",
          {"nextcloud", "immich", "forgejo", "excalidraw"} == set(state["apps"]))

    serialised = json.dumps(state)
    check("the state carries no secret",
          "HCLOUD_TOKEN" not in serialised and "GITOPS_REPO_TOKEN" not in serialised)

    # The cache files are parsed, never sourced -- they must not be able to run
    # anything, and a hostile value must not escape into a default.
    import tempfile
    with tempfile.NamedTemporaryFile("w", suffix=".env", delete=False) as handle:
        handle.write('DOMAIN="$(touch /tmp/smallworlds-wizard-should-not-exist)"\n')
        handle.write('ADMIN_EMAIL="you@example.org"\n')
        hostile = handle.name
    os.environ["SMALLWORLDS_INIT_CACHE"] = hostile
    parsed = plan._read_shell_assignments(hostile)
    check("cache files are parsed, not executed",
          not os.path.exists("/tmp/smallworlds-wizard-should-not-exist")
          and parsed.get("ADMIN_EMAIL") == "you@example.org")
    os.unlink(hostile)
    os.environ["SMALLWORLDS_INIT_CACHE"] = "/nonexistent/init.env"

    complete = {
        "domain": "example.org", "admin_email": "you@example.org", "env_ext": "",
        "onboarding_mode": "invitation", "target": "hetzner", "hcloud_token": "x",
        "git_token": "y", "git_mode": "create", "repo_name": "my-community-config",
        "repo_path": "/tmp/overlay", "version": "v1.2.43",
        "apps": ["nextcloud", "immich"],
    }
    check("a complete set of answers validates", plan.validate_answers(complete) == [])

    def fields(changes):
        answers = dict(complete)
        answers.update(changes)
        return {problem["field"] for problem in plan.validate_answers(answers)}

    check("a bad domain is caught", "domain" in fields({"domain": "not a domain"}))
    check("a bad email is caught", "admin_email" in fields({"admin_email": "nope"}))
    check("an extension without its dot is caught", "env_ext" in fields({"env_ext": "dev"}))
    check("a missing Hetzner token is caught", "hcloud_token" in fields({"hcloud_token": ""}))
    check("a missing Git token is caught", "git_token" in fields({"git_token": ""}))

    # docs/adr/0048: backups have to survive losing the operational disk.
    same_directory = fields({
        "target": "local", "ssh_target": "root@10.0.0.2", "local_tls": "self-signed",
        "local_public": "no", "data_dir": "/srv/data", "backup_dir": "/srv/data",
    })
    check("backups inside the data directory are refused", "backup_dir" in same_directory)

    # Both Let's Encrypt (DNS-01) and internet exposure write into Hetzner DNS.
    lan_needs_token = fields({
        "target": "local", "ssh_target": "root@10.0.0.2", "hcloud_token": "",
        "data_dir": "/srv/data", "backup_dir": "/mnt/backup", "local_tls": "letsencrypt",
    })
    check("a LAN install with real certificates still needs a DNS token",
          "hcloud_token" in lan_needs_token)
    lan_self_signed = fields({
        "target": "local", "ssh_target": "root@10.0.0.2", "hcloud_token": "",
        "data_dir": "/srv/data", "backup_dir": "/mnt/backup",
        "local_tls": "self-signed", "local_public": "no",
    })
    check("a self-signed LAN install needs no DNS token",
          "hcloud_token" not in lan_self_signed)

    built = plan.build_plan(complete)
    check("a plan is produced", built["valid"] is True)
    check("the plan names the two real scripts",
          {"admin-tools/prepare-community-repo.sh", "./smallworlds-init.sh"} ==
          {command["command"] for command in built["commands"]})
    check("the plan lists the hostnames members will use",
          {"identity.example.org", "files.example.org", "photos.example.org"} <=
          {entry["host"] for entry in built["hostnames"]})
    check("the plan carries no secret", "x" not in json.dumps(built["summary"]).split())

    warned = plan.build_plan({**complete, "apps": ["bulwark"]})
    check("webmail without mail is warned about",
          any("Webmail" in warning for warning in warned["warnings"]))
    unwarned = plan.build_plan({**complete, "apps": ["bulwark", "stalwart"]})
    check("webmail with mail is not warned about",
          not any("Webmail" in warning for warning in unwarned["warnings"]))
    self_signed = plan.build_plan({
        **complete, "target": "local", "ssh_target": "root@10.0.0.2",
        "data_dir": "/srv/data", "backup_dir": "/mnt/backup",
        "local_tls": "self-signed", "local_public": "no", "hcloud_token": "",
    })
    check("self-signed certificates are warned about",
          any("single sign-on" in warning for warning in self_signed["warnings"]))


# ---------------------------------------------------------------------------
# Offline: the DNS resolver
# ---------------------------------------------------------------------------

def test_checks_module():
    print("checks — offline behaviour")
    import checks

    check("a nonsense domain is not treated as a lookup failure",
          checks.check_domain_nameservers("")["status"] == "unknown")
    check("an empty Hetzner token is refused without a call",
          checks.check_hetzner_token("")["status"] == "error")
    check("an empty Git token is refused without a call",
          checks.check_git_token("")["status"] == "error")
    check("an empty SSH target is refused without a call",
          checks.check_local_target("", "/a", "/b")["status"] == "error")

    tools = checks.check_local_tools()
    check("every tool is reported with what it is needed for",
          all({"name", "present", "needed_for", "purpose"} <= set(tool)
              for tool in tools["tools"]))


# ---------------------------------------------------------------------------
# The HTTP surface
# ---------------------------------------------------------------------------

def start_wizard(extra_environment: dict | None = None):
    environment = {**os.environ,
                   "SMALLWORLDS_COMMUNITY_CONFIG": "/nonexistent/community.conf",
                   "SMALLWORLDS_INIT_CACHE": "/nonexistent/init.env"}
    environment.update(extra_environment or {})
    process = subprocess.Popen(
        [sys.executable, os.path.join(WIZARD_DIRECTORY, "wizard.py"),
         "--no-browser", "--port", "0"],
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, cwd=REPO_ROOT,
        env=environment,
    )
    deadline = time.time() + 20
    while time.time() < deadline:
        line = process.stdout.readline()
        if not line:
            break
        match = re.search(r"(http://127\.0\.0\.1:\d+/\?t=[\w\-]+)", line)
        if match:
            url = match.group(1)
            return process, url.split("/?")[0], url.split("t=")[1]
    process.terminate()
    raise SystemExit("The wizard did not print its URL.")


def call(base, path, *, token=None, host=None, body=None):
    request = urllib.request.Request(
        base + path, method="POST" if body is not None else "GET",
        data=json.dumps(body).encode() if body is not None else None)
    if token:
        request.add_header("X-Setup-Token", token)
    if body is not None:
        request.add_header("Content-Type", "application/json")
    if host:
        request.add_header("Host", host)
    try:
        with urllib.request.urlopen(request, timeout=25) as response:
            return response.status, response.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode("utf-8", "replace")


def test_server():
    print("server — routing, session token and secrecy")
    process, base, token = start_wizard()
    try:
        status, body = call(base, f"/?t={token}")
        check("the page is served with the right token",
              status == 200 and "SmallWorlds Setup" in body)
        check("the token is injected into the page, not fetched",
              f'"{token}"' in body and "__SESSION_TOKEN__" not in body)
        check("a wrong token is refused", call(base, "/?t=wrong")[0] == 403)
        check("the API is refused without a token", call(base, "/api/state")[0] == 403)

        # Loopback binding alone does not stop a page on the internet pointing a
        # hostname it controls at 127.0.0.1 and driving this from the browser.
        check("a non-loopback Host is refused",
              call(base, "/api/state", token=token, host="evil.example.com")[0] == 403)

        status, body = call(base, "/api/state", token=token)
        state = json.loads(body)
        check("the state is served", status == 200 and "catalog" in state)
        check("no secret reaches the browser",
              "HCLOUD_TOKEN" not in body and "GITOPS_REPO_TOKEN" not in body)

        check("static assets are served", call(base, "/static/app.js")[0] == 200)
        check("there is no path out of the static directory",
              call(base, "/static/../wizard.py")[0] in (403, 404))
        check("unknown routes are refused",
              call(base, "/api/nope", token=token, body={})[0] == 404)

        answers = {
            "domain": "example.org", "admin_email": "you@example.org", "env_ext": "",
            "onboarding_mode": "invitation", "target": "hetzner",
            "hcloud_token": "a-token-that-must-not-come-back",
            "git_token": "another-token-that-must-not-come-back", "git_mode": "create",
            "repo_name": "my-community-config", "repo_path": "/tmp/overlay",
            "version": "v1.2.43", "apps": ["nextcloud"],
        }
        status, body = call(base, "/api/plan", token=token, body={"answers": answers})
        built = json.loads(body)
        check("a plan is returned", built.get("valid") is True)
        check("the plan echoes no submitted secret",
              "must-not-come-back" not in body)

        status, body = call(base, "/api/plan", token=token,
                            body={"answers": {"target": "local"}})
        incomplete = json.loads(body)
        check("incomplete answers come back named, not as a failure",
              incomplete.get("valid") is False and
              {"domain", "admin_email", "ssh_target"} <=
              {problem["field"] for problem in incomplete["problems"]})

        # Phase 1 explains and does not act.
        overlay_written = os.path.exists("/tmp/overlay")
        check("planning created nothing", not overlay_written)

        status, body = call(base, "/api/check/dns", token=token,
                            body={"domain": "smallworlds.network"})
        dns = json.loads(body)
        if dns["status"] == "unknown":
            skip("the nameserver check recognises Hetzner delegation", "no outbound DNS")
        else:
            check("the nameserver check recognises Hetzner delegation",
                  dns["status"] == "ok" and "Hetzner" in dns["summary"], dns["summary"])

        status, body = call(base, "/api/check/hetzner", token=token,
                            body={"token": "not-a-real-token", "domain": "example.org"})
        hetzner = json.loads(body)
        if hetzner["status"] == "unknown":
            skip("an invalid Hetzner token is rejected", "no internet")
        else:
            check("an invalid Hetzner token is rejected",
                  hetzner["status"] == "error", hetzner["summary"])

        # A failing check must never take the wizard down with it.
        status, body = call(base, "/api/check/target", token=token,
                            body={"ssh_target": "root@203.0.113.1",
                                  "data_dir": "/a", "backup_dir": "/b"})
        check("an unreachable machine is reported, not crashed into",
              status == 200 and json.loads(body)["status"] in ("error", "unknown"))
        check("the wizard is still answering after a failed check",
              call(base, "/api/state", token=token)[0] == 200)
    finally:
        process.terminate()
        process.wait(timeout=10)


# ---------------------------------------------------------------------------
# Running the scripts
# ---------------------------------------------------------------------------

STUB_REPOSITORY_SCRIPT = """#!/usr/bin/env bash
set -e
[ "$SMALLWORLDS_NONINTERACTIVE" = "1" ] || { echo "not headless"; exit 9; }
echo ">>> step:repo-init Initialising"
echo "writing the overlay"
echo ">>> ok:repo-init"
echo ">>> step:repo-push Pushing"
echo "pushing as $GIT_USERNAME"
echo ">>> ok:repo-push"
"""

STUB_INSTALLER_SCRIPT = """#!/usr/bin/env bash
set -e
echo ">>> step:terraform-apply Creating the server"
echo "a careless script printing its token: $GIT_PASSWORD"
echo ">>> ok:terraform-apply"
echo ">>> step:argocd-converge Waiting"
echo "  [3/120] 12/19 apps healthy (root: Synced/Progressing)"
echo "${STUB_INSTALLER_EXTRA:-}"
exit "${STUB_INSTALLER_EXIT:-0}"
"""


def make_stub_checkout(directory: str) -> str:
    """A checkout whose two scripts print markers and exit, so the runner can be
    exercised without provisioning anything."""
    root = os.path.join(directory, "checkout")
    os.makedirs(os.path.join(root, "admin-tools"), exist_ok=True)
    for relative, body in (
        ("admin-tools/prepare-community-repo.sh", STUB_REPOSITORY_SCRIPT),
        ("smallworlds-init.sh", STUB_INSTALLER_SCRIPT),
    ):
        path = os.path.join(root, relative)
        with open(path, "w") as handle:
            handle.write(body)
        os.chmod(path, 0o755)

    # Tagged, so the wizard reads its default release from here instead of
    # falling back to asking GitHub — the stub stays usable offline.
    environment = {**os.environ, "GIT_AUTHOR_NAME": "test",
                   "GIT_AUTHOR_EMAIL": "test@example.org",
                   "GIT_COMMITTER_NAME": "test",
                   "GIT_COMMITTER_EMAIL": "test@example.org"}
    for command in (["git", "init", "-q", "-b", "main"], ["git", "add", "-A"],
                    ["git", "commit", "-qm", "stub"], ["git", "tag", "v1.2.43"]):
        subprocess.run(command, cwd=root, env=environment, capture_output=True)
    return root


def test_runner():
    print("runner — writing the cache files")
    import tempfile
    import runner

    directory = tempfile.mkdtemp()
    answers = {
        "domain": "example.org", "admin_email": "you@example.org", "env_ext": ".dev",
        "onboarding_mode": "invitation", "target": "hetzner",
        "git_mode": "existing", "git_username": "octocat",
        "remote_url": "https://github.com/octocat/overlay.git",
        "repo_path": os.path.join(directory, "overlay"), "version": "v1.2.43",
        "apps": ["nextcloud", "excalidraw"],
    }
    tokens = {"HCLOUD_TOKEN": "hetzner-secret", "GITOPS_REPO_TOKEN": "git-secret"}

    repository_config = os.path.join(directory, "config", "community-setup.conf")
    runner.write_repository_config(answers, repository_config, answers["apps"])
    written = plan_module()._read_shell_assignments(repository_config)
    check("the repository config carries the answers",
          written["STORED_TARGET_DOMAIN"] == "example.org" and
          written["STORED_VERSION"] == "v1.2.43")
    check("selected applications are marked y",
          written["STORED_APP_nextcloud"] == "y" and written["STORED_APP_excalidraw"] == "y")
    check("unselected applications are marked n", written["STORED_APP_immich"] == "n")
    check("pod-gateway becomes a shell-safe name", "STORED_APP_pod_gateway" in written)
    # The wizard creates the repository itself, so the script must not shell out
    # to the GitHub CLI, and must push what it wrote.
    check("repository creation is left to the wizard", written["STORED_CREATE_REPO"] == "n")
    check("the overlay is pushed", written["STORED_PUSH_CHOICE"] == "y")
    check("the config is private", oct(os.stat(repository_config).st_mode)[-3:] == "600")

    installer_cache = os.path.join(directory, "cache.env")
    runner.write_installer_cache(answers, installer_cache, tokens)
    cached = plan_module()._read_shell_assignments(installer_cache)
    check("the installer cache carries the answers",
          cached["DOMAIN"] == "example.org" and cached["ENV_EXT"] == ".dev")
    check("the tokens reach the installer", cached["HCLOUD_TOKEN"] == "hetzner-secret")
    check("the cache is private", oct(os.stat(installer_cache).st_mode)[-3:] == "600")

    # Regenerating these would lock the operator out of a cluster already up.
    with open(installer_cache, "a") as handle:
        handle.write("KC_PASS='already-generated'\n")
    runner.write_installer_cache(answers, installer_cache, tokens)
    check("generated credentials survive a second run",
          plan_module()._read_shell_assignments(installer_cache)["KC_PASS"]
          == "already-generated")

    # A value is quoted for bash, not merely inserted into it.
    runner.write_installer_cache(
        {**answers, "domain": "evil'; touch /tmp/smallworlds-wizard-injection; #"},
        installer_cache, tokens)
    subprocess.run(["bash", "-c", f". {installer_cache}"], capture_output=True)
    check("a hostile answer cannot escape into the shell",
          not os.path.exists("/tmp/smallworlds-wizard-injection"))

    print("runner — running the scripts")
    checkout = make_stub_checkout(directory)
    run = runner.Run(answers, {"GITOPS_REPO_TOKEN": "SUPER-SECRET-TOKEN-VALUE"}, checkout)
    check("the stage list is derived from the answers",
          [stage["id"] for stage in run.stages] == runner.HETZNER_STAGES)

    watcher = run.subscribe()
    run.start()
    deadline = time.time() + 60
    seen = []
    while time.time() < deadline:
        try:
            message = watcher.get(timeout=5)
        except Exception:
            break
        seen.append(message["event"])
        if message["event"] == "done":
            break
    final = run.snapshot()

    check("the run succeeded", final["status"] == "succeeded", final.get("error", ""))
    check("markers became stages, not log lines",
          not any(line.startswith(">>>") for line in final["log"]))
    states = {stage["id"]: stage["state"] for stage in final["stages"]}
    check("finished stages are marked done",
          states["repo-init"] == "done" and states["terraform-apply"] == "done")
    check("stages nobody reached stay pending", states["secrets"] == "pending")
    check("the convergence counter is parsed",
          final["progress"] == {"healthy": 12, "total": 19})
    check("the scripts ran headless",
          not any("not headless" in line for line in final["log"]))
    check("git credentials reach the scripts",
          any("pushing as octocat" in line for line in final["log"]))

    # The single most important property of the log.
    check("a secret printed by a script is redacted",
          not any("SUPER-SECRET-TOKEN-VALUE" in line for line in final["log"]) and
          any("[redacted]" in line for line in final["log"]))
    check("redaction leaves the rest of the line intact",
          any("a careless script printing its token" in line for line in final["log"]))
    check("watchers saw the stream", {"stage", "log", "status", "done"} <= set(seen))

    print("runner — a script that fails")
    os.environ["STUB_INSTALLER_EXIT"] = "1"
    failing = runner.Run(answers, {}, checkout)
    failing.start()
    deadline = time.time() + 60
    while time.time() < deadline and failing.status in ("starting", "running"):
        time.sleep(0.2)
    del os.environ["STUB_INSTALLER_EXIT"]
    check("a failing script fails the run", failing.status == "failed", failing.status)
    check("the failure says what it was doing", "installing the cluster" in failing.error)
    check("the stage it died in is marked failed",
          any(stage["state"] == "failed" for stage in failing.stages))


def plan_module():
    import plan
    return plan


def test_install_endpoint():
    print("server — starting and streaming an install")
    import tempfile
    directory = tempfile.mkdtemp()
    checkout = make_stub_checkout(directory)

    process, base, token = start_wizard(extra_environment={
        "SMALLWORLDS_REPO_ROOT": checkout,
        "SMALLWORLDS_COMMUNITY_CONFIG": os.path.join(directory, "community.conf"),
        "SMALLWORLDS_INIT_CACHE": os.path.join(directory, "cache.env"),
    })
    try:
        status, body = call(base, "/api/install/status", token=token)
        check("nothing is running to begin with", json.loads(body)["status"] == "idle")

        answers = {
            "domain": "example.org", "admin_email": "you@example.org", "env_ext": "",
            "onboarding_mode": "invitation", "target": "hetzner",
            "hcloud_token": "a-token", "git_token": "another-token",
            "git_mode": "existing", "git_username": "octocat",
            "remote_url": "https://github.com/octocat/overlay.git",
            "repo_path": os.path.join(directory, "overlay"),
            "repo_name": "overlay", "version": "v1.2.43", "apps": ["nextcloud"],
        }
        status, body = call(base, "/api/install", token=token, body={"answers": answers})
        started = json.loads(body)
        check("the install starts", started.get("started") is True, body[:200])
        check("the stage list comes back with it", len(started.get("stages", [])) > 0)

        # A second install must not race the first over the same files and node.
        status, body = call(base, "/api/install", token=token, body={"answers": answers})
        second = json.loads(body)
        check("a second install is refused while one runs",
              second.get("started") is False and second.get("reason") == "already_running")

        events = read_events(base, token, limit_seconds=45)
        check("the stream delivers a snapshot first", events[0][0] == "snapshot")
        kinds = {name for name, _ in events}
        check("the stream carries stages and log lines", {"stage", "log"} <= kinds)
        check("the stream ends with done", events[-1][0] == "done")

        final = json.loads(events[-1][1])
        check("the install succeeded", final["status"] == "succeeded", final.get("error", ""))
        check("no secret reached the browser",
              not any("a-token" in line or "another-token" in line for line in final["log"]))

        status, body = call(base, "/api/install/status", token=token)
        check("the outcome survives for a reopened page",
              json.loads(body)["status"] == "succeeded")

        # The two cache files are what the scripts read; they must be private.
        for path in (os.path.join(directory, "community.conf"),
                     os.path.join(directory, "cache.env")):
            check(f"{os.path.basename(path)} was written 0600",
                  os.path.exists(path) and oct(os.stat(path).st_mode)[-3:] == "600")

        status, body = call(base, "/api/install", token=token,
                            body={"answers": {"target": "local"}})
        refused = json.loads(body)
        check("an incomplete install is refused, with the fields named",
              refused.get("started") is False and refused.get("reason") == "incomplete")
    finally:
        process.terminate()
        process.wait(timeout=10)


def read_events(base, token, limit_seconds=45):
    """Read the event stream to its end, as the browser's EventSource does."""
    request = urllib.request.Request(f"{base}/api/events?t={token}")
    collected = []
    deadline = time.time() + limit_seconds
    with urllib.request.urlopen(request, timeout=limit_seconds) as response:
        name = None
        while time.time() < deadline:
            line = response.readline().decode("utf-8", "replace").rstrip("\n")
            if line.startswith("event: "):
                name = line[len("event: "):]
            elif line.startswith("data: ") and name:
                collected.append((name, line[len("data: "):]))
                if name == "done":
                    break
                name = None
    return collected


if __name__ == "__main__":
    test_plan_module()
    test_checks_module()
    test_server()
    test_runner()
    test_install_endpoint()
    print()
    if skipped:
        print(f"{len(skipped)} check(s) skipped for lack of network.")
    if failures:
        print(f"{len(failures)} check(s) failed.")
        raise SystemExit(1)
    print("All checks passed.")
