"""Defaults, the application catalog, and the dry-run plan.

The wizard never invents an answer the setup scripts would not have used. Every
default here is read back from the same two files those scripts already cache
their answers in, so a second run through the browser offers exactly what a
second run in the terminal would.

Standard library only. See plans-and-walkthroughs/design-setup-wizard.md.
"""

from __future__ import annotations

import os
import re
import shlex
import subprocess

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
# Where the two setup scripts cache their answers. Both are overridable so the
# tests can exercise a first-time operator's empty machine without touching the
# operator's real files -- prepare-community-repo.sh already follows $HOME for
# the first, and smallworlds-init.sh writes the second beside itself.
COMMUNITY_CONFIG = os.environ.get("SMALLWORLDS_COMMUNITY_CONFIG") or \
    os.path.expanduser("~/.config/smallworlds/community-setup.conf")
INIT_CACHE = os.environ.get("SMALLWORLDS_INIT_CACHE") or \
    os.path.join(REPO_ROOT, ".smallworlds-cache.env")
UPSTREAM_REPO = "https://github.com/stephan271/smallworlds.git"

# ---------------------------------------------------------------------------
# The application catalog
#
# Defaults follow docs/adr/0049 (mail is opt-in: nothing in the cluster depends
# on Stalwart, onboarding hands out action-token links out of band and alerting
# pushes over HTTP) and docs/adr/0037 (capacity-aware opt-in applications).
# ---------------------------------------------------------------------------

ALWAYS_INSTALLED = [
    {"name": "keycloak", "label": "Identity",
     "description": "Single sign-on for every other application. Not optional."},
    {"name": "dashboard", "label": "Dashboard",
     "description": "The landing page members see, linking the apps they can reach."},
]

OPTIONAL_APPS = [
    {"name": "nextcloud", "label": "Files", "default": True, "weight": "heavy",
     "description": "File sync and sharing, with a database and its own storage."},
    {"name": "immich", "label": "Photos", "default": True, "weight": "heavy",
     "description": "Photo and video library with machine-learning search."},
    {"name": "forgejo", "label": "Git", "default": True, "weight": "medium",
     "description": "Git hosting, issues and pull requests."},
    {"name": "excalidraw", "label": "Whiteboard", "default": True, "weight": "light",
     "description": "Shared drawing board. No database, no storage."},
    {"name": "jitsi", "label": "Meet", "default": False, "weight": "heavy",
     "description": "Video meetings. Needs UDP 10000 reachable to work well."},
    {"name": "collabora", "label": "Office", "default": False, "weight": "heavy",
     "description": "Online document editing inside Files. Memory-hungry."},
    {"name": "plane", "label": "Plan", "default": False, "weight": "heavy",
     "description": "Project and issue planning across several services."},
    {"name": "stalwart", "label": "Mail", "default": False, "weight": "medium",
     "description": "A real mail server. Opt-in: nothing else in the cluster "
                    "needs it, and delivery from a home connection is unreliable "
                    "— ISPs block port 25 and home addresses sit on blocklists."},
    {"name": "bulwark", "label": "Webmail", "default": False, "weight": "light",
     "description": "Webmail client. Hosts no mail itself, so it needs a mail "
                    "server — either Mail above, or one elsewhere that you "
                    "point it at in your overlay.",
     "requires": "stalwart",
     "requires_note": "Webmail was selected without Mail. It deploys, and no "
                      "member can log in until you patch JMAP_SERVER_URL, "
                      "OIDC_AUDIENCE_MAPPER and STALWART_FEATURES to a mail "
                      "server outside the cluster (doc/tenant-other.md). "
                      "Pointing it at a mail server elsewhere is a legitimate "
                      "choice, and often the better one."},
    {"name": "pod-gateway", "label": "Personal data pods", "default": False, "weight": "medium",
     "description": "Append-only personal archives members can keep on their own "
                    "hardware at home (docs/adr/0047)."},
]


def default_apps() -> list[str]:
    return [app["name"] for app in OPTIONAL_APPS if app["default"]]


# ---------------------------------------------------------------------------
# Reading back what the setup scripts cached
# ---------------------------------------------------------------------------

_ASSIGNMENT = re.compile(r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$')


def _read_shell_assignments(path: str) -> dict:
    """Both cache files are shell fragments of simple assignments. Parsed, not
    sourced: this must never execute whatever happens to be in the file."""
    values = {}
    if not os.path.isfile(path):
        return values
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as handle:
            for line in handle:
                if line.lstrip().startswith("#"):
                    continue
                match = _ASSIGNMENT.match(line)
                if not match:
                    continue
                key, raw = match.group(1), match.group(2).strip()
                try:
                    parts = shlex.split(raw, comments=True)
                except ValueError:
                    continue
                values[key] = parts[0] if parts else ""
    except OSError:
        return values
    return values


def _app_preference_key(app_name: str) -> str:
    """prepare-community-repo.sh sanitises app names into shell identifiers:
    pod-gateway becomes STORED_APP_pod_gateway."""
    return "STORED_APP_" + re.sub(r"[^A-Za-z0-9_]", "_", app_name)


_CACHED_TAG: str | None = None


def latest_upstream_tag() -> str:
    """Newest published release, preferring local tags so a wizard started
    without a network still offers something sensible."""
    global _CACHED_TAG
    if _CACHED_TAG is not None:
        return _CACHED_TAG
    _CACHED_TAG = ""
    for command in (
        ["git", "-C", REPO_ROOT, "tag", "--list", "v*", "--sort=-v:refname"],
        ["git", "ls-remote", "--tags", "--sort=-v:refname", UPSTREAM_REPO],
    ):
        try:
            completed = subprocess.run(
                command, capture_output=True, text=True, timeout=15,
                env={**os.environ, "GIT_TERMINAL_PROMPT": "0"},
            )
        except (subprocess.TimeoutExpired, OSError):
            continue
        if completed.returncode != 0:
            continue
        found = re.findall(r"v\d+\.\d+\.\d+", completed.stdout)
        if found:
            _CACHED_TAG = found[0]
            break
    return _CACHED_TAG


def initial_state() -> dict:
    """What the browser opens with: remembered answers where they exist, and
    defensible defaults everywhere else."""
    stored = _read_shell_assignments(COMMUNITY_CONFIG)
    cached = _read_shell_assignments(INIT_CACHE)

    remembered_apps = [
        app["name"] for app in OPTIONAL_APPS
        if stored.get(_app_preference_key(app["name"])) == "y"
    ]
    has_memory = any(_app_preference_key(a["name"]) in stored for a in OPTIONAL_APPS)

    env_ext = cached.get("ENV_EXT", stored.get("STORED_ENV_EXT", ""))
    domain = cached.get("DOMAIN") or stored.get("STORED_TARGET_DOMAIN", "")
    tag = latest_upstream_tag()

    return {
        "domain": domain,
        "admin_email": cached.get("ADMIN_EMAIL", ""),
        "env_ext": env_ext,
        "onboarding_mode": cached.get("ONBOARDING_MODE") or "invitation",

        "target": cached.get("DEPLOY_TARGET") or "hetzner",
        "ssh_target": cached.get("LOCAL_SSH_TARGET", ""),
        "data_dir": cached.get("DATA_DIR") or "/var/lib/smallworlds-data",
        "backup_dir": cached.get("BACKUP_DIR") or "/var/lib/smallworlds-backup",
        "local_tls": cached.get("LOCAL_TLS") or "letsencrypt",
        "local_public": cached.get("LOCAL_PUBLIC") or "no",

        "git_mode": "existing" if stored.get("STORED_REMOTE_URL") else "create",
        "git_username": cached.get("GITOPS_REPO_USER", ""),
        "remote_url": stored.get("STORED_REMOTE_URL", "") or cached.get("GITOPS_REPO_URL", ""),
        "repo_name": os.path.basename(stored.get("STORED_REPO_PATH", "")) or
                     f"my-community{env_ext}-config",
        "repo_path": stored.get("STORED_REPO_PATH", "") or
                     os.path.join(os.path.dirname(REPO_ROOT), f"my-community{env_ext}-config"),
        "version": stored.get("STORED_VERSION") or tag,

        "apps": remembered_apps if has_memory else default_apps(),

        # Secrets are never sent to the browser, not even ones this machine
        # already holds. The wizard reports only whether one is remembered, so
        # a field can offer "use the saved token" without carrying it.
        "has_saved_hcloud_token": bool(cached.get("HCLOUD_TOKEN")),
        "has_saved_git_token": bool(cached.get("GITOPS_REPO_TOKEN")),

        "catalog": {"always": ALWAYS_INSTALLED, "optional": OPTIONAL_APPS},
        "repo_root": REPO_ROOT,
        "latest_tag": tag,
    }


# ---------------------------------------------------------------------------
# Validation of a complete set of answers
# ---------------------------------------------------------------------------

_DOMAIN_PATTERN = re.compile(
    r"^(?!-)[A-Za-z0-9-]{1,63}(?<!-)(\.(?!-)[A-Za-z0-9-]{1,63}(?<!-))+$")
_EMAIL_PATTERN = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")


def validate_answers(answers: dict) -> list[dict]:
    """Field-level problems that stop a plan being produced. Distinct from the
    checks in checks.py, which reach the network and mostly advise."""
    problems = []

    def problem(field: str, message: str):
        problems.append({"field": field, "message": message})

    domain = (answers.get("domain") or "").strip().lower()
    if not domain:
        problem("domain", "A domain is required — every application is named under it.")
    elif not _DOMAIN_PATTERN.match(domain):
        problem("domain", f"'{domain}' is not a valid domain name.")

    email = (answers.get("admin_email") or "").strip()
    if not email:
        problem("admin_email", "An admin email address is required.")
    elif not _EMAIL_PATTERN.match(email):
        problem("admin_email", f"'{email}' is not a valid email address.")

    env_ext = (answers.get("env_ext") or "").strip()
    if env_ext and not env_ext.startswith("."):
        problem("env_ext", "An environment extension must start with a dot, for example .dev")

    if answers.get("onboarding_mode") not in ("invitation", "self-registration"):
        problem("onboarding_mode", "Choose either invitation or self-registration.")

    target = answers.get("target")
    if target == "hetzner":
        if not answers.get("hcloud_token") and not answers.get("use_saved_hcloud_token"):
            problem("hcloud_token", "A Hetzner Cloud API token is required to provision the VM.")
    elif target == "local":
        if not (answers.get("ssh_target") or "").strip():
            problem("ssh_target", "Enter the machine's SSH target, or localhost to use this one.")
        if not (answers.get("data_dir") or "").startswith("/"):
            problem("data_dir", "The data directory must be an absolute path.")
        if not (answers.get("backup_dir") or "").startswith("/"):
            problem("backup_dir", "The backup directory must be an absolute path.")
        if answers.get("data_dir") and answers.get("data_dir") == answers.get("backup_dir"):
            problem("backup_dir", "Backups cannot live in the data directory (docs/adr/0048).")
        needs_dns = (answers.get("local_tls") == "letsencrypt"
                     or answers.get("local_public") == "yes")
        if needs_dns and not answers.get("hcloud_token") and not answers.get("use_saved_hcloud_token"):
            problem(
                "hcloud_token",
                "Let's Encrypt certificates and internet exposure both write "
                "records into Hetzner DNS, so a token is required.",
            )
    else:
        problem("target", "Choose where the cluster runs.")

    if not answers.get("git_token") and not answers.get("use_saved_git_token"):
        problem("git_token",
                "A Git access token is required — Argo CD reads the overlay with it.")
    if answers.get("git_mode") == "existing" and not (answers.get("remote_url") or "").strip():
        problem("remote_url", "Enter the repository URL, or let the wizard create one.")
    if answers.get("git_mode") == "create" and not (answers.get("repo_name") or "").strip():
        problem("repo_name", "Give the new repository a name.")
    if not (answers.get("version") or "").strip():
        problem("version", "Choose which upstream release to pin.")

    return problems


# ---------------------------------------------------------------------------
# The dry-run plan
# ---------------------------------------------------------------------------

_SUBDOMAIN_BY_APP = {
    "nextcloud": "files", "immich": "photos", "forgejo": "git",
    "excalidraw": "whiteboard", "jitsi": "meet", "collabora": "office",
    "plane": "plan", "stalwart": "mail", "bulwark": "webmail",
    "pod-gateway": "pod",
}


def _hostnames(domain: str, env_ext: str, apps: list[str]) -> list[dict]:
    pairs = [("identity", "Identity"), ("dashboard", "Dashboard")]
    labels = {app["name"]: app["label"] for app in OPTIONAL_APPS}
    for app in apps:
        if app in _SUBDOMAIN_BY_APP:
            pairs.append((_SUBDOMAIN_BY_APP[app], labels.get(app, app)))
    return [{"host": f"{sub}{env_ext}.{domain}", "app": label} for sub, label in pairs]


def build_plan(answers: dict) -> dict:
    """What would happen, in plain language, plus the exact commands. Produces
    no side effects whatsoever — Phase 2 is what executes it."""
    problems = validate_answers(answers)
    if problems:
        return {"valid": False, "problems": problems}

    domain = answers["domain"].strip().lower()
    env_ext = (answers.get("env_ext") or "").strip()
    apps = list(answers.get("apps") or [])
    target = answers["target"]

    if answers.get("git_mode") == "create":
        repository = f"a new private repository named {answers['repo_name']}"
    else:
        repository = answers["remote_url"]

    steps = [{
        "id": "repository",
        "title": "Write the settings repository",
        "detail": (
            f"Create the Kustomize overlay in {answers['repo_path']}, pinned to "
            f"upstream {answers['version']}, and push it to {repository}. This "
            "repository — not this wizard — is what the cluster follows "
            "from then on."
        ),
    }]

    if target == "hetzner":
        steps.append({
            "id": "provision",
            "title": "Provision the machine",
            "detail": (
                f"Create a Hetzner Cloud VM and the DNS zone for {domain}, then let "
                "cloud-init install k3s and Argo CD on first boot."
            ),
        })
        certificates = "Let's Encrypt, once the DNS records resolve"
        reachable = "from the internet"
    else:
        certificates = (
            "Let's Encrypt, over a DNS-01 challenge — no port forwarding and "
            "no public IP needed"
            if answers.get("local_tls") == "letsencrypt"
            else "self-signed, which breaks single sign-on between the applications"
        )
        reachable = (
            "from the internet, with an in-cluster job maintaining the A records"
            if answers.get("local_public") == "yes"
            else "from your LAN only, resolved through your router's DNS or /etc/hosts"
        )
        steps.append({
            "id": "provision",
            "title": "Prepare the machine",
            "detail": (
                f"Install k3s and Argo CD on {answers['ssh_target']} over SSH, with "
                f"data in {answers['data_dir']} and backups in "
                f"{answers['backup_dir']} on a separate device."
            ),
        })

    steps.extend([
        {
            "id": "secrets",
            "title": "Generate and plant the credentials",
            "detail": (
                "Generate the Keycloak, Argo CD, Grafana, Garage and invite "
                "credentials and place them on the node as Kubernetes Secrets. They "
                "are shown to you once, at the end, and never committed to Git."
            ),
        },
        {
            "id": "converge",
            "title": "Wait for the applications",
            "detail": (
                f"Argo CD installs {len(apps) + 2} applications in dependency order. "
                "This takes five to fifteen minutes."
            ),
        },
    ])

    warnings = []
    if "bulwark" in apps and "stalwart" not in apps:
        warnings.append(next(
            a["requires_note"] for a in OPTIONAL_APPS if a["name"] == "bulwark"
        ))
    if target == "local" and answers.get("local_tls") != "letsencrypt":
        warnings.append(
            "Self-signed certificates mean single sign-on does not work at all: "
            "every application validates Keycloak's certificate against its own "
            "trust store with no override, so Files, Photos and Meet cannot "
            "complete OIDC discovery. Choose this only for a throwaway cluster."
        )
    if target == "local" and answers.get("local_public") == "yes":
        warnings.append(
            "Internet exposure needs a public IPv4 (no CGNAT) and these forwards "
            "on your router to the machine: 80/tcp, 443/tcp and 10000/udp."
        )
    if "stalwart" in apps and target == "local":
        warnings.append(
            "Mail delivery from a home connection is unreliable — ISPs block "
            "port 25, there is no PTR record, and home addresses sit on "
            "blocklists. Consider an SMTP relay for outbound mail."
        )

    return {
        "valid": True,
        "summary": {
            "domain": domain,
            "environment": "production" if not env_ext else env_ext.lstrip("."),
            "target": "a Hetzner Cloud VM" if target == "hetzner" else answers["ssh_target"],
            "certificates": certificates,
            "reachable": reachable,
            "onboarding": answers["onboarding_mode"],
            "repository": repository,
            "version": answers["version"],
            "application_count": len(apps) + 2,
        },
        "steps": steps,
        "hostnames": _hostnames(domain, env_ext, apps),
        "warnings": warnings,
        "commands": [
            {
                "command": "admin-tools/prepare-community-repo.sh",
                "environment": "SMALLWORLDS_NONINTERACTIVE=1",
                "purpose": "writes and pushes the settings repository",
            },
            {
                "command": "./smallworlds-init.sh",
                "environment": "SMALLWORLDS_NONINTERACTIVE=1",
                "purpose": "provisions the machine and installs the cluster",
            },
        ],
        "config_files": [
            {"path": COMMUNITY_CONFIG, "purpose": "answers for the repository script"},
            {"path": INIT_CACHE,
             "purpose": "answers and generated credentials for the installer"},
        ],
    }
