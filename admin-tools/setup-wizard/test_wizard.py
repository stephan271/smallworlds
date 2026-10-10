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
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

WIZARD_DIRECTORY = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.abspath(os.path.join(WIZARD_DIRECTORY, "..", ".."))
sys.path.insert(0, WIZARD_DIRECTORY)
sys.path.insert(0, os.path.join(REPO_ROOT, "admin-tools"))

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
    check("Office is an explicit three-way choice",
          state["catalog"]["choice_groups"][0]["name"] == "office" and
          {"collabora", "euro-office"} <=
          {app["name"] for app in state["catalog"]["optional"]} and
          not {"collabora", "euro-office"}.intersection(state["apps"]))

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
    check("Collabora is a valid Office choice",
          "apps" not in fields({"apps": ["nextcloud", "collabora"]}))
    check("Euro-Office is a valid Office choice",
          "apps" not in fields({"apps": ["nextcloud", "euro-office"]}))
    check("both Office editors are refused",
          "apps" in fields({"apps": ["nextcloud", "collabora", "euro-office"]}))
    check("an Office editor without Nextcloud is refused",
          "apps" in fields({"apps": ["euro-office"]}))

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
    euro_plan = plan.build_plan({**complete, "apps": ["nextcloud", "euro-office"]})
    check("Euro-Office uses the one shared office hostname",
          [entry["host"] for entry in euro_plan["hostnames"]].count("office.example.org") == 1)

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

    # docs/adr/0053: an external S3 provider instead of the in-cluster Garage.
    external = {
        "object_storage": "external", "s3_endpoint": "https://s3.provider.example",
        "s3_region": "eu-central-1", "s3_bucket_prefix": "sw-example-org",
        "s3_access_key_id": "AKID", "s3_secret_access_key": "s3-secret-value",
    }
    check("Garage remains the default, and asks for nothing",
          not {f for f in fields({}) if f.startswith("s3_")})
    check("a complete provider description validates", fields(external) == set())
    check("a bucket URL in place of the endpoint is caught",
          "s3_endpoint" in fields({**external, "s3_endpoint": "https://s3.provider.example/bucket"}))
    check("an unusable bucket prefix is caught",
          "s3_bucket_prefix" in fields({**external, "s3_bucket_prefix": "Has_Upper"}))
    check("a missing S3 secret is caught",
          "s3_secret_access_key" in fields({**external, "s3_secret_access_key": ""}))
    check("a saved S3 secret satisfies it",
          "s3_secret_access_key" not in fields(
              {**external, "s3_secret_access_key": "", "use_saved_s3_secret": True}))
    check("a separate backup endpoint needs its own region and key",
          {"s3_backup_region", "s3_backup_access_key_id", "s3_backup_secret_access_key"} <=
          fields({**external, "s3_backup_endpoint": "https://backup.other.example"}))
    local_external = {
        **external, "target": "local", "ssh_target": "root@10.0.0.2",
        "local_tls": "self-signed", "local_public": "no",
        "data_dir": "/srv/data", "backup_dir": "/srv/data",
    }
    check("with a provider, one disk is enough: no backup directory is demanded",
          "backup_dir" not in fields(local_external))
    external_plan = plan.build_plan({**complete, **local_external, "hcloud_token": ""})
    check("the plan says where files and backups go",
          "s3.provider.example" in external_plan["summary"]["storage"])
    check("the plan no longer promises a separate device",
          not any("separate device" in step["detail"] for step in external_plan["steps"]))
    check("the plan carries no S3 secret", "s3-secret-value" not in json.dumps(external_plan))
    check("storing data at a third party is warned about",
          any("can read them" in warning for warning in external_plan["warnings"]))
    check("backups sharing the provider are warned about",
          any("provider account" in warning for warning in external_plan["warnings"]))
    separate = plan.build_plan({
        **complete, **external, "s3_backup_endpoint": "https://backup.other.example",
        "s3_backup_region": "fra1", "s3_backup_access_key_id": "BK",
        "s3_backup_secret_access_key": "backup-secret-value"})
    check("a second provider for backups lifts that warning",
          not any("provider account" in warning for warning in separate["warnings"]))
    check("the default bucket prefix matches the script's",
          plan.default_bucket_prefix("Example.org", ".dev") == "sw-example-org-dev")

    # docs/adr/0054: Gallery is a choice within Photos, not another application.
    check("an unknown photo application is caught",
          "photo_backend" in fields({"photo_backend": "picasa"}))
    immich_plan = plan.build_plan(complete)
    check("Immich is what Photos runs unless told otherwise",
          immich_plan["summary"]["photos"].startswith("Immich"))
    gallery_plan = plan.build_plan({**complete, **external, "photo_backend": "gallery"})
    check("the plan says Gallery keeps the photos at the provider",
          "Gallery" in gallery_plan["summary"]["photos"]
          and "provider" in gallery_plan["summary"]["photos"])
    check("Gallery does not change which hostnames exist",
          [h["host"] for h in gallery_plan["hostnames"]] ==
          [h["host"] for h in plan.build_plan({**complete, **external})["hostnames"]])
    check("depending on a fork is warned about",
          any("fork" in warning for warning in gallery_plan["warnings"]))
    check("Gallery with the pod archive warns that nothing fills the pods",
          any("nothing fills the pods" in warning for warning in plan.build_plan(
              {**complete, "photo_backend": "gallery",
               "apps": ["immich", "pod-gateway"]})["warnings"]))
    check("Gallery without Photos selected is not mentioned at all",
          plan.build_plan({**complete, "photo_backend": "gallery", "apps": ["nextcloud"]}
                          )["summary"]["photos"] == "not installed")

    # docs/adr/0055: a rented server is the local target, on the internet,
    # with a firewall the installer sets up.
    rented = {
        "target": "local", "ssh_target": "root@203.0.113.9", "hcloud_token": "x",
        "data_dir": "/var/lib/smallworlds-data", "backup_dir": "/mnt/backup",
        "local_tls": "letsencrypt", "local_public": "yes", "host_firewall": "yes",
    }
    check("a rented server validates", fields(rented) == set())
    check("a firewall without internet exposure is refused",
          "host_firewall" in fields({**rented, "local_public": "no"}))
    check("a relative photo library directory is caught",
          "library_dir" in fields({**rented, "library_dir": "photos"}))
    rented_plan = plan.build_plan({**complete, **rented, "apps": ["immich", "stalwart"],
                                   "library_dir": "/mnt/big/photos"})
    check("the plan calls it a rented server",
          "rented server" in rented_plan["summary"]["target"])
    check("the plan announces the firewall",
          any("inbound port" in warning for warning in rented_plan["warnings"]))
    check("a rented server is not told to configure a router",
          not any("router" in warning for warning in rented_plan["warnings"]))
    check("mail on a rented server is told about reverse DNS, not home connections",
          any("PTR" in warning for warning in rented_plan["warnings"])
          and not any("home connection" in warning for warning in rented_plan["warnings"]))
    check("the plan says where the photo library goes",
          "/mnt/big/photos" in rented_plan["summary"]["photos"])


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

    # Hand-rolled SigV4, pinned to what botocore's S3SigV4Auth produces for the
    # same request and instant. A drift here is a key every provider rejects.
    import datetime
    signed = checks.sigv4_headers(
        "GET", "https://s3.provider.example/", "eu-central-1", "AKIDEXAMPLE",
        "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
        datetime.datetime(2026, 10, 10, 12, 0, 0, tzinfo=datetime.timezone.utc))
    check("the S3 request signature matches the reference implementation",
          signed["Authorization"].endswith(
              "Signature=bc95c6b0f32cda3969c8227101fb9c0129714ff8e6105dce57c2507ddde3c1a0"),
          signed["Authorization"])

    import http.server
    import threading

    answer = {"status": 200, "body": b""}
    seen = {}

    class FakeS3(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            seen["authorization"] = self.headers.get("Authorization", "")
            self.send_response(answer["status"])
            self.send_header("Content-Length", str(len(answer["body"])))
            self.end_headers()
            self.wfile.write(answer["body"])

        def log_message(self, *arguments):
            pass

    server = http.server.HTTPServer(("127.0.0.1", 0), FakeS3)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    endpoint = f"http://127.0.0.1:{server.server_port}"
    try:
        answer.update(status=200, body=(
            b"<ListAllMyBucketsResult><Buckets><Bucket><Name>one</Name></Bucket>"
            b"</Buckets></ListAllMyBucketsResult>"))
        accepted = checks.check_s3(endpoint, "eu-central-1", "AKID", "zz-distinct-value-zz")
        check("an accepted S3 key is reported ok",
              accepted["status"] == "ok" and accepted["buckets"] == ["one"], str(accepted))
        check("the S3 check signs its request",
              "Credential=AKID/" in seen.get("authorization", ""))
        check("the S3 secret is neither sent nor echoed",
              "zz-distinct-value-zz" not in json.dumps(accepted) + seen.get("authorization", ""))

        answer.update(status=403, body=b"<Error><Code>SignatureDoesNotMatch</Code></Error>")
        check("a wrong S3 secret is an error",
              checks.check_s3(endpoint, "eu-central-1", "AKID", "wrong")["status"] == "error")
        answer.update(status=403, body=b"<Error><Code>AccessDenied</Code></Error>")
        check("a key that may not list buckets is a warning, not a failure",
              checks.check_s3(endpoint, "eu-central-1", "AKID", "secret")["status"] == "warning")
        answer.update(status=200, body=b"<html>welcome</html>")
        check("an endpoint that is not S3 is not called ok",
              checks.check_s3(endpoint, "eu-central-1", "AKID", "secret")["status"] == "unknown")
    finally:
        server.shutdown()
    check("an unreachable S3 endpoint is an error, not a crash",
          checks.check_s3("http://127.0.0.1:9", "r", "AKID", "secret")["status"] == "error")
    check("an incomplete S3 description is refused before any request",
          checks.check_s3("", "", "", "")["status"] == "error")

    tools = checks.check_local_tools()
    check("every tool is reported with what it is needed for",
          all({"name", "present", "needed_for", "purpose"} <= set(tool)
              for tool in tools["tools"]))


def test_office_overlay_helpers():
    print("office selection — idempotent overlay changes")
    import reconcile_office_selection

    with tempfile.TemporaryDirectory() as directory:
        nextcloud = os.path.join(directory, "nextcloud.yaml")
        with open(nextcloud, "w", encoding="utf-8") as handle:
            handle.write("resources:\n  - base\n")
        generator = os.path.join(REPO_ROOT, "admin-tools", "generate_domain_patches.py")
        for backend in ("collabora", "euro-office", "none"):
            completed = subprocess.run([
                sys.executable, generator, "--app", "nextcloud",
                "--domain", "smallworlds.network", "--office-backend", backend,
                "--kustomization-file", nextcloud,
            ], capture_output=True, text=True)
            check(f"generator accepts {backend}", completed.returncode == 0,
                  completed.stderr)
        with open(nextcloud, encoding="utf-8") as handle:
            generated = handle.read()
        check("reruns leave one Office selector",
              generated.count("path: /data/OFFICE_BACKEND") == 1)
        check("the latest Office choice replaces the old one",
              'value: "none"' in generated and 'value: "collabora"' not in generated)

        root = os.path.join(directory, "kustomization.yaml")
        with open(root, "w", encoding="utf-8") as handle:
            handle.write("""resources:
  - https://example.test/apps/collabora.yaml
  - https://example.test/apps/euro-office.yaml
patches:
  - target:
      kind: Application
      name: collabora
    patch: |-\n      - op: replace\n        path: /spec/source/path\n        value: collabora
  - target:
      kind: Application
      name: euro-office
    patch: |-\n      - op: replace\n        path: /spec/source/path\n        value: euro-office
  - target:
      kind: CronJob
      name: euro-office
    patch: |-\n      - op: replace\n        path: /spec/schedule\n        value: daily
images:
  - name: unrelated
    newTag: current
""")
        reconcile_office_selection.reconcile(Path(root), "euro-office")
        with open(root, encoding="utf-8") as handle:
            reconciled = handle.read()
        check("switching removes the unselected Application",
              "/apps/collabora.yaml" not in reconciled and
              "name: collabora" not in reconciled)
        check("switching keeps the selected Application",
              "/apps/euro-office.yaml" in reconciled and
              "kind: Application\n      name: euro-office" in reconciled)
        check("only Application patches are removed",
              "kind: CronJob\n      name: euro-office" in reconciled and
              "newTag: current" in reconciled)
        reconcile_office_selection.reconcile(Path(root), "none")
        with open(root, encoding="utf-8") as handle:
            without_office = handle.read()
        check("choosing no editor removes the remaining Office Application",
              "/apps/euro-office.yaml" not in without_office and
              "kind: Application\n      name: euro-office" not in without_office)


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
        status, body = call(base, "/api/check/s3", token=token,
                            body={"endpoint": "http://127.0.0.1:9", "region": "r",
                                  "access_key_id": "AKID", "secret": "not-a-real-secret"})
        check("an unreachable storage provider is reported, not crashed into",
              status == 200 and json.loads(body)["status"] == "error", body)
        check("the storage check does not echo the secret", "not-a-real-secret" not in body)
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
result() { printf '>>> result\\t%s\\t%s\\t%s\\n' "$1" "$2" "$3"; }
result kubeconfig kubeconfig "/home/someone/.smallworlds/kubeconfigs/production.yaml"
result credential "Keycloak Operator (operator)" "operator-password-value"
result note "Keycloak Operator (operator)" "your login; sets up a TOTP authenticator"
result credential "Argo CD Admin (admin)" "argocd-password-value"
result url Dashboard "https://dashboard.example.org"
result url Identity "https://identity.example.org"
result url Files "https://files.example.org"
result url Photos "https://photos.example.org"
result hosts hosts "192.168.1.50 identity.example.org dashboard.example.org"
if [ -n "${STUB_INSTALLER_EXIT:-}" ]; then
  printf '>>> guidance\t%s\t%s\n' "Try Falkenstein instead" "nbg1 has no cx43 right now."
  printf '>>> guidance\t%s\t%s\n' "Try Falkenstein instead" "a repeat that must not stack"
fi
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
    check("unselected Office alternatives are both recorded",
          written["STORED_APP_collabora"] == "n" and
          written["STORED_APP_euro_office"] == "n")
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

    check("an install that never chose a provider records Garage for both scripts",
          written["STORED_OBJECT_STORAGE"] == "garage" and cached["OBJECT_STORAGE"] == "garage"
          and cached["S3_SECRET_ACCESS_KEY"] == "")

    external_answers = {
        **answers, "object_storage": "external", "s3_endpoint": "https://s3.provider.example",
        "s3_region": "eu-central-1", "s3_bucket_prefix": "sw-example-org-dev",
        "s3_access_key_id": "AKID",
        # Left over from an earlier attempt with a second provider: must not leak.
        "s3_backup_region": "stale", "s3_backup_access_key_id": "stale",
    }
    external_tokens = {**tokens, "S3_SECRET_ACCESS_KEY": "s3-secret",
                       "S3_BACKUP_SECRET_ACCESS_KEY": "stale-backup-secret"}
    runner.write_repository_config(external_answers, repository_config, answers["apps"])
    stored_external = plan_module()._read_shell_assignments(repository_config)
    check("the provider's coordinates reach the repository script",
          (stored_external["STORED_OBJECT_STORAGE"], stored_external["STORED_S3_ENDPOINT"],
           stored_external["STORED_S3_BUCKET_PREFIX"]) ==
          ("external", "https://s3.provider.example", "sw-example-org-dev"))
    with open(repository_config) as handle:
        check("no S3 key is written where the overlay script reads",
              "s3-secret" not in handle.read())
    external_cache = os.path.join(directory, "external-cache.env")
    runner.write_installer_cache(external_answers, external_cache, external_tokens)
    cached_external = plan_module()._read_shell_assignments(external_cache)
    check("the S3 key reaches the installer",
          (cached_external["OBJECT_STORAGE"], cached_external["S3_ACCESS_KEY_ID"],
           cached_external["S3_SECRET_ACCESS_KEY"]) == ("external", "AKID", "s3-secret"))
    check("Immich is recorded as the photo application by default",
          written["STORED_PHOTO_BACKEND"] == "immich" and cached["LOCAL_FIREWALL"] == "no")
    rented_answers = {
        **external_answers, "target": "local", "ssh_target": "root@203.0.113.9",
        "local_public": "yes", "local_tls": "letsencrypt", "host_firewall": "yes",
        "library_dir": "/mnt/big/photos", "photo_backend": "gallery",
    }
    runner.write_repository_config(rented_answers, repository_config, answers["apps"])
    check("the photo application reaches the repository script",
          plan_module()._read_shell_assignments(repository_config)["STORED_PHOTO_BACKEND"]
          == "gallery")
    rented_cache = os.path.join(directory, "rented-cache.env")
    runner.write_installer_cache(rented_answers, rented_cache, external_tokens)
    cached_rented = plan_module()._read_shell_assignments(rented_cache)
    check("a rented server reaches the installer as an exposed machine with a firewall",
          (cached_rented["DEPLOY_TARGET"], cached_rented["LOCAL_PUBLIC"],
           cached_rented["LOCAL_FIREWALL"], cached_rented["LIBRARY_DIR"]) ==
          ("local", "yes", "yes", "/mnt/big/photos"))
    check("a Hetzner install never asks for a host firewall",
          plan_module()._read_shell_assignments(external_cache)["LOCAL_FIREWALL"] == "no")
    check("without a backup endpoint no backup key is passed on",
          cached_external["S3_BACKUP_REGION"] == "" and
          cached_external["S3_BACKUP_ACCESS_KEY_ID"] == "" and
          cached_external["S3_BACKUP_SECRET_ACCESS_KEY"] == "")

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
    check("reserving the public address is one of them",
          "primary-ip" in runner.HETZNER_STAGES)
    # The longest silence in the whole install is the node's first boot. A
    # stage nobody can see is indistinguishable from a hang.
    check("the server's first boot is a stage of its own",
          "node-ready" in runner.HETZNER_STAGES)
    check("it comes after the server is created and before the secrets",
          runner.HETZNER_STAGES.index("terraform-apply")
          < runner.HETZNER_STAGES.index("node-ready")
          < runner.HETZNER_STAGES.index("secrets"))

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

    print("runner — what the install produced")
    result = final["result"]
    names = {entry["name"]: entry for entry in result.get("credentials", [])}
    check("credentials are collected",
          names["Keycloak Operator (operator)"]["value"] == "operator-password-value" and
          names["Argo CD Admin (admin)"]["value"] == "argocd-password-value")
    check("a note is attached to the credential it explains",
          "TOTP" in names["Keycloak Operator (operator)"]["note"])
    check("result markers never reach the log",
          not any(">>> result" in line for line in final["log"]))
    check("the kubeconfig path is captured",
          final["result"]["kubeconfig"].endswith("production.yaml"))
    check("the hosts line is captured", "192.168.1.50" in final["result"]["hosts"])

    # The installer prints every application's address; listing one for
    # something this community did not install is a link to a certificate error.
    offered = {entry["name"] for entry in result.get("urls", [])}
    check("addresses are shown for what was installed",
          {"Dashboard", "Identity", "Files"} <= offered)
    check("addresses are not shown for what was not", "Photos" not in offered)

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

    # "The installer failed, read the log" is not something an operator can act
    # on. The scripts say what to do about it; the wizard carries the words
    # through rather than inventing its own.
    snapshot = failing.snapshot()
    headings = [entry["heading"] for entry in snapshot["guidance"]]
    check("the installer's own advice reaches the browser",
          "Try Falkenstein instead" in headings, headings)
    check("the same advice is not stacked twice",
          headings.count("Try Falkenstein instead") == 1, headings)
    check("the advice keeps its detail",
          any("no cx43" in entry["detail"] for entry in snapshot["guidance"]))
    check("guidance markers never reach the log",
          not any(">>> guidance" in line for line in snapshot["log"]))

    # A script that fails without saying why still has to leave the operator
    # somewhere to go.
    silent = runner.Run(answers, {}, checkout)
    silent._set_stage("repo-push", "running")
    silent._fail(1, "writing the settings repository")
    check("a failure with no advice still gets some",
          [entry["heading"] for entry in silent.guidance] ==
          ["Check what the Git token may do", "Read the end of the log",
           "Nothing has to be undone"],
          [entry["heading"] for entry in silent.guidance])


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

        check("the handover reached the browser",
              any(entry["name"] == "Argo CD Admin (admin)"
                  for entry in final["result"].get("credentials", [])))
        check("no generated credential leaked into the log",
              not any("argocd-password-value" in line for line in final["log"]))

        status, body = call(base, "/api/install", token=token,
                            body={"answers": {"target": "local"}})
        refused = json.loads(body)
        check("an incomplete install is refused, with the fields named",
              refused.get("started") is False and refused.get("reason") == "incomplete")

        # The wizard is single-use, but closing it mid-apply would orphan
        # terraform, so shutting down is only offered once nothing is running.
        status, body = call(base, "/api/shutdown", token=token, body={})
        check("the wizard can be closed once the install is done",
              json.loads(body).get("stopped") is True)
        for _ in range(40):
            if process.poll() is not None:
                break
            time.sleep(0.25)
        check("closing it actually stops the server", process.poll() is not None)
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
    test_office_overlay_helpers()
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
