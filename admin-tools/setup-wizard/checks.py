"""Validation the SmallWorlds setup wizard runs before anything is installed.

Every check here corresponds to a way an install currently fails halfway
through, and every one is an API call or a command the setup scripts already
make -- moved earlier, so the answer arrives while the operator is still
looking at the field that caused it.

Standard library only, like the rest of the wizard. See
plans-and-walkthroughs/design-setup-wizard.md.
"""

from __future__ import annotations

import json
import os
import random
import shutil
import socket
import struct
import subprocess
import urllib.error
import urllib.parse
import urllib.request

HETZNER_API = "https://api.hetzner.cloud/v1"
GITHUB_API = "https://api.github.com"
ADMIN_SSH_KEY_NAME = "SmallWorlds Admin Key"

# Hetzner's authoritative nameservers. A domain whose registrar still points
# somewhere else is the single most common silent failure: the DNS-01 challenge
# then never resolves and every certificate stays pending with nothing saying
# why.
HETZNER_NAMESERVERS = ("ns.hetzner.com", "ns.hetzner.de", "ns.hetzner.net")

TIMEOUT_SECONDS = 15


def ok(summary: str, **extra) -> dict:
    return {"status": "ok", "summary": summary, **extra}


def warn(summary: str, **extra) -> dict:
    return {"status": "warning", "summary": summary, **extra}


def bad(summary: str, **extra) -> dict:
    return {"status": "error", "summary": summary, **extra}


def unknown(summary: str, **extra) -> dict:
    """Could not be determined -- which is not the same as failing, and must
    never read like one. An operator behind a restrictive resolver still has to
    be able to continue."""
    return {"status": "unknown", "summary": summary, **extra}


# ---------------------------------------------------------------------------
# HTTP
# ---------------------------------------------------------------------------

def _request(url: str, *, headers: dict | None = None, method: str = "GET"):
    """Returns (status_code, parsed_body_or_None, response_headers)."""
    request = urllib.request.Request(url, headers=headers or {}, method=method)
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT_SECONDS) as response:
            raw = response.read()
            try:
                body = json.loads(raw)
            except ValueError:
                body = None
            return response.status, body, dict(response.headers)
    except urllib.error.HTTPError as error:
        raw = error.read()
        try:
            body = json.loads(raw)
        except ValueError:
            body = None
        return error.code, body, dict(error.headers)
    except (urllib.error.URLError, socket.timeout, OSError) as error:
        return None, {"reason": str(error)}, {}


# ---------------------------------------------------------------------------
# Local prerequisites
# ---------------------------------------------------------------------------

# `terraform` is needed only for the Hetzner target, `ssh` only to reach a LAN
# machine, and `kubectl` only to watch the rollout -- the install completes
# without it, just blind. Which of these actually matter depends on answers the
# operator has not given yet, so each carries what it is needed for and the
# panel decides.
LOCAL_TOOLS = (
    ("git", "hetzner+local", "resolving the overlay repository"),
    ("terraform", "hetzner", "provisioning the Hetzner VM and DNS"),
    ("ssh", "local", "reaching the LAN machine"),
    ("scp", "local", "copying the bootstrap payload"),
    ("kubectl", "hetzner+local", "watching the rollout (optional)"),
    ("openssl", "hetzner+local", "generating cluster credentials"),
    ("curl", "hetzner+local", "talking to the Hetzner API"),
)


def check_local_tools() -> dict:
    tools = []
    for name, needed_for, purpose in LOCAL_TOOLS:
        path = shutil.which(name)
        tools.append({
            "name": name,
            "present": path is not None,
            "path": path,
            "needed_for": needed_for,
            "purpose": purpose,
        })
    missing = [tool["name"] for tool in tools if not tool["present"]]
    if not missing:
        return ok("Every command the installer uses is available.", tools=tools)
    return warn(
        "Not found: " + ", ".join(missing) + ". Whether that matters depends on "
        "where you install.",
        tools=tools,
    )


def check_ssh_public_key(path: str | None = None) -> dict:
    """The Hetzner target uploads this key and then logs in with it. Terraform
    never creates one, so a missing key fails the install rather than falling
    back to a password."""
    candidates = [path] if path else [
        os.path.expanduser("~/.ssh/id_ed25519.pub"),
        os.path.expanduser("~/.ssh/id_rsa.pub"),
    ]
    for candidate in candidates:
        if candidate and os.path.isfile(candidate):
            return ok(f"Found {candidate}.", path=candidate)
    return bad(
        "No SSH public key found. Create one with: ssh-keygen -t ed25519",
        path=None,
    )


# ---------------------------------------------------------------------------
# Hetzner
# ---------------------------------------------------------------------------

def check_hetzner_token(token: str, domain: str = "") -> dict:
    """Answers three questions in one call sequence: is the token valid, does
    the DNS zone already exist, and is the shared admin SSH key registered."""
    if not token:
        return bad("No token given.")

    headers = {"Authorization": f"Bearer {token}"}
    status, body, _ = _request(f"{HETZNER_API}/zones", headers=headers)

    if status is None:
        return unknown(
            "Could not reach the Hetzner API — check your internet connection.",
            detail=(body or {}).get("reason"),
        )
    if status in (401, 403):
        return bad("Hetzner rejected this token.")
    if status >= 400:
        message = ((body or {}).get("error") or {}).get("message") or f"HTTP {status}"
        return bad(f"Hetzner returned an error: {message}")

    result = {"zone_exists": False, "ssh_key_registered": False}
    notes = ["Token accepted."]

    if domain:
        wanted = domain.rstrip(".")
        for zone in (body or {}).get("zones", []):
            if (zone.get("name") or "").rstrip(".") == wanted:
                result["zone_exists"] = True
                result["zone_id"] = zone.get("id")
                break
        notes.append(
            f"DNS zone for {domain} already exists."
            if result["zone_exists"]
            else f"No DNS zone for {domain} yet — the installer creates it."
        )

    key_query = urllib.parse.quote(ADMIN_SSH_KEY_NAME)
    status, body, _ = _request(f"{HETZNER_API}/ssh_keys?name={key_query}", headers=headers)
    if status == 200 and (body or {}).get("ssh_keys"):
        result["ssh_key_registered"] = True
        notes.append("The shared admin SSH key is already registered.")
    else:
        notes.append("The admin SSH key will be uploaded during the install.")

    return ok(" ".join(notes), **result)


# ---------------------------------------------------------------------------
# The domain's nameservers
# ---------------------------------------------------------------------------

def _query_nameservers(domain: str, resolver: str) -> list[str] | None:
    """A minimal DNS NS query over UDP.

    Deliberately not shelling out to `dig`: it is absent on plenty of desktop
    installs, and this is 40 lines of standard library that behaves the same
    everywhere.
    """
    transaction_id = random.randint(0, 0xFFFF)
    header = struct.pack(">HHHHHH", transaction_id, 0x0100, 1, 0, 0, 0)
    question = b"".join(
        bytes([len(label)]) + label.encode("idna")
        for label in domain.rstrip(".").split(".")
    ) + b"\x00" + struct.pack(">HH", 2, 1)  # QTYPE=NS, QCLASS=IN

    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.settimeout(4)
    try:
        sock.sendto(header + question, (resolver, 53))
        payload, _ = sock.recvfrom(4096)
    except (OSError, socket.timeout):
        return None
    finally:
        sock.close()

    if len(payload) < 12:
        return None
    _, _, question_count, answer_count = struct.unpack(">HHHH", payload[:8])

    def read_name(offset: int) -> tuple[str, int]:
        labels, jumped, original = [], False, offset
        while True:
            if offset >= len(payload):
                return "", original
            length = payload[offset]
            if length == 0:
                offset += 1
                break
            if length & 0xC0 == 0xC0:  # compression pointer
                pointer = struct.unpack(">H", payload[offset:offset + 2])[0] & 0x3FFF
                if not jumped:
                    original = offset + 2
                offset, jumped = pointer, True
                continue
            labels.append(payload[offset + 1:offset + 1 + length].decode("ascii", "replace"))
            offset += 1 + length
        return ".".join(labels), (original if jumped else offset)

    offset = 12
    for _ in range(question_count):
        _, offset = read_name(offset)
        offset += 4

    nameservers = []
    for _ in range(answer_count):
        _, offset = read_name(offset)
        if offset + 10 > len(payload):
            break
        record_type, _, _, data_length = struct.unpack(">HHIH", payload[offset:offset + 10])
        offset += 10
        if record_type == 2:  # NS
            name, _ = read_name(offset)
            if name:
                nameservers.append(name.lower().rstrip("."))
        offset += data_length
    return nameservers


def check_domain_nameservers(domain: str, resolver: str = "1.1.1.1") -> dict:
    """Whether the registrar delegates this domain to Hetzner DNS.

    Only advisory: a LAN-only cluster with self-signed certificates needs no
    public DNS at all, and a delegation changed minutes ago has not propagated
    yet. Never blocks — it explains.
    """
    if not domain or "." not in domain:
        return unknown("Enter a domain first.")

    nameservers = _query_nameservers(domain, resolver)
    if nameservers is None:
        return unknown(
            "Could not look up the nameservers — this check needs outbound "
            "DNS on port 53. It is advisory; you can continue."
        )
    if not nameservers:
        return warn(
            f"{domain} has no nameserver records. Either it is not registered "
            "yet, or the delegation has not propagated.",
            nameservers=[],
        )

    at_hetzner = [
        name for name in nameservers
        if any(name.endswith(suffix) for suffix in HETZNER_NAMESERVERS)
    ]
    if at_hetzner:
        return ok(
            f"{domain} is delegated to Hetzner DNS ({', '.join(sorted(at_hetzner))}).",
            nameservers=nameservers,
        )
    return warn(
        f"{domain} is delegated to {', '.join(sorted(nameservers))}, not Hetzner "
        "DNS. Let's Encrypt solves its challenge by writing a TXT record into "
        "Hetzner's zone, so certificates will stay pending until you repoint the "
        "nameservers at your registrar.",
        nameservers=nameservers,
    )


# ---------------------------------------------------------------------------
# The settings repository
# ---------------------------------------------------------------------------

def check_git_token(token: str, remote_url: str = "") -> dict:
    """GitHub gets a real identity and scope check. Any other host gets an
    honest 'cannot tell from here' plus, if a URL is known, a reachability
    probe — self-hosted Forgejo and GitLab are legitimate choices here."""
    if not token:
        return bad("No token given.")

    host = urllib.parse.urlparse(remote_url).hostname if remote_url else None
    if remote_url and host and "github.com" not in host:
        return check_git_remote(remote_url, token)

    status, body, headers = _request(
        f"{GITHUB_API}/user",
        headers={
            "Authorization": f"Bearer {token}",
            "Accept": "application/vnd.github+json",
            "User-Agent": "smallworlds-setup-wizard",
        },
    )
    if status is None:
        return unknown(
            "Could not reach the GitHub API — check your internet connection.",
            detail=(body or {}).get("reason"),
        )
    if status in (401, 403):
        return bad("GitHub rejected this token.")
    if status >= 400 or not body:
        return bad(f"GitHub returned HTTP {status}.")

    login = body.get("login") or "?"
    scopes = [s.strip() for s in (headers.get("x-oauth-scopes") or "").split(",") if s.strip()]
    result = {"login": login, "scopes": scopes}

    if not scopes:
        # Fine-grained tokens do not report scopes at all. Absence is not
        # evidence of insufficiency, so say what is unknown rather than
        # inventing a verdict.
        return ok(
            f"Token accepted for {login}. It is a fine-grained token, so its "
            "permissions cannot be read from here — it needs Contents: "
            "read/write, and Pull requests: read/write for the weekly Renovate "
            "update PRs.",
            **result,
        )
    if "repo" in scopes:
        return ok(f"Token accepted for {login}, with full repository access.", **result)
    return warn(
        f"Token accepted for {login}, but it carries only: {', '.join(scopes)}. "
        "Creating a private repository and letting Renovate open update PRs "
        "needs the 'repo' scope.",
        **result,
    )


def check_git_remote(remote_url: str, token: str = "", username: str = "") -> dict:
    """Can this repository actually be reached with these credentials?

    smallworlds-init.sh resolves the overlay's HEAD before it touches a
    machine, and fails the whole install if it cannot. Same question, asked
    while it is still cheap to answer.
    """
    if not remote_url:
        return bad("No repository URL given.")
    if not shutil.which("git"):
        return unknown("git is not installed, so the repository cannot be probed.")

    environment = dict(os.environ)
    environment["GIT_TERMINAL_PROMPT"] = "0"
    if token:
        # Credentials travel through the environment and an askpass helper,
        # never in the URL -- a URL reaches the process list and git's own
        # error output.
        environment["GIT_USERNAME"] = username or "x-access-token"
        environment["GIT_PASSWORD"] = token
        environment["GIT_ASKPASS"] = _askpass_helper()

    try:
        completed = subprocess.run(
            ["git", "ls-remote", "--heads", remote_url],
            capture_output=True, text=True, timeout=TIMEOUT_SECONDS, env=environment,
        )
    except subprocess.TimeoutExpired:
        return unknown("Timed out reaching the repository.")

    if completed.returncode != 0:
        detail = (completed.stderr or "").strip().splitlines()
        return bad(
            "Could not reach the repository with these credentials.",
            detail=detail[-1] if detail else None,
        )
    has_commits = bool(completed.stdout.strip())
    if has_commits:
        return ok("Repository reachable, and it already has commits.", empty=False)
    # An empty repository is the normal state before the overlay is written,
    # but a node cannot be pointed at one: the install resolves an exact commit.
    return ok(
        "Repository reachable, and still empty — the overlay will be its first "
        "commit.",
        empty=True,
    )


_ASKPASS_PATH: str | None = None


def _askpass_helper() -> str:
    """A tiny helper git calls for credentials, so they never land in a URL."""
    global _ASKPASS_PATH
    if _ASKPASS_PATH and os.path.exists(_ASKPASS_PATH):
        return _ASKPASS_PATH
    import tempfile
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
    _ASKPASS_PATH = handle.name
    return _ASKPASS_PATH


# ---------------------------------------------------------------------------
# A LAN machine
# ---------------------------------------------------------------------------

# Asked in one round trip, because each SSH connection costs a second or two.
# The two `df` lines are the load-bearing part: backups must survive the loss
# of the operational disk (docs/adr/0048), and the bootstrap refuses to run if
# they share a device -- three minutes into an install, rather than here.
_REMOTE_PROBE = r"""
printf 'systemd=%s\n' "$( [ -d /run/systemd/system ] && echo yes || echo no )"
printf 'arch=%s\n' "$(uname -m)"
printf 'sudo=%s\n' "$( [ "$(id -u)" = 0 ] && echo root || (sudo -n true 2>/dev/null && echo nopasswd || echo password) )"
device_of() {
  p="$1"
  while [ ! -e "$p" ] && [ "$p" != "/" ]; do p=$(dirname "$p"); done
  df -P "$p" 2>/dev/null | tail -1 | awk '{print $1}'
}
printf 'data_device=%s\n' "$(device_of "$1")"
printf 'backup_device=%s\n' "$(device_of "$2")"
printf 'memory_kb=%s\n' "$(awk '/MemTotal/{print $2}' /proc/meminfo 2>/dev/null)"
"""


def check_local_target(ssh_target: str, data_dir: str, backup_dir: str) -> dict:
    if not ssh_target:
        return bad("No SSH target given.")
    if not shutil.which("ssh") and ssh_target != "localhost":
        return unknown("ssh is not installed, so the machine cannot be probed.")

    command = ["sh", "-s", data_dir, backup_dir]
    if ssh_target != "localhost":
        command = [
            "ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no",
            "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=8",
            ssh_target, "sh", "-s", data_dir, backup_dir,
        ]

    try:
        completed = subprocess.run(
            command, input=_REMOTE_PROBE, capture_output=True, text=True,
            timeout=TIMEOUT_SECONDS,
        )
    except (subprocess.TimeoutExpired, OSError) as error:
        return bad(f"Could not reach {ssh_target}.", detail=str(error))

    if completed.returncode != 0:
        detail = (completed.stderr or "").strip().splitlines()
        return bad(
            f"Could not reach {ssh_target} over SSH with key authentication.",
            detail=detail[-1] if detail else None,
        )

    facts = {}
    for line in completed.stdout.splitlines():
        if "=" in line:
            key, _, value = line.partition("=")
            facts[key.strip()] = value.strip()

    problems, notes = [], []
    if facts.get("systemd") != "yes":
        problems.append("no systemd — k3s is installed as a systemd unit")
    if facts.get("sudo") == "password":
        notes.append("sudo will ask for a password during the install")

    data_device = facts.get("data_device") or ""
    backup_device = facts.get("backup_device") or ""
    same_device = bool(data_device) and data_device == backup_device
    if same_device:
        problems.append(
            f"{data_dir} and {backup_dir} are both on {data_device}. Backups "
            "have to survive losing the operational disk, so the bootstrap "
            "refuses a single device (docs/adr/0048)"
        )
    elif data_device and backup_device:
        notes.append(f"data on {data_device}, backups on {backup_device} — separate devices")

    memory_kb = int(facts.get("memory_kb") or 0)
    if memory_kb and memory_kb < 7 * 1024 * 1024:
        notes.append(
            f"{memory_kb // 1024 // 1024} GB of RAM — enough for the core, tight "
            "once several community applications are selected"
        )

    result = {
        "facts": facts,
        "same_device": same_device,
        "problems": problems,
    }
    if problems:
        return bad("; ".join(problems).capitalize() + ".", **result)
    summary = f"{ssh_target} is reachable and suitable."
    if notes:
        summary += " " + "; ".join(notes).capitalize() + "."
    return ok(summary, **result)
