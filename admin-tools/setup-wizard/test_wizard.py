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

def start_wizard():
    process = subprocess.Popen(
        [sys.executable, os.path.join(WIZARD_DIRECTORY, "wizard.py"),
         "--no-browser", "--port", "0"],
        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, cwd=REPO_ROOT,
        env={**os.environ,
             "SMALLWORLDS_COMMUNITY_CONFIG": "/nonexistent/community.conf",
             "SMALLWORLDS_INIT_CACHE": "/nonexistent/init.env"},
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


if __name__ == "__main__":
    test_plan_module()
    test_checks_module()
    test_server()
    print()
    if skipped:
        print(f"{len(skipped)} check(s) skipped for lack of network.")
    if failures:
        print(f"{len(failures)} check(s) failed.")
        raise SystemExit(1)
    print("All checks passed.")
