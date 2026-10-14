"""Defaults, the application catalog, and the dry-run plan.

The wizard never invents an answer the setup scripts would not have used. Every
default here is read back from the same two files those scripts already cache
their answers in, so a second run through the browser offers exactly what a
second run in the terminal would.

Standard library only. See plans-and-walkthroughs/design-setup-wizard.md.
"""

from __future__ import annotations

import importlib.util
import os
import re
import shlex
import subprocess

# The SmallWorlds checkout whose scripts get run. Normally the one this file
# lives in; overridable so the tests can point the wizard at stub scripts, and
# so the wizard can be run against a checkout somewhere else.
REPO_ROOT = os.path.abspath(
    os.environ.get("SMALLWORLDS_REPO_ROOT")
    or os.path.join(os.path.dirname(__file__), "..", ".."))
# Where the two setup scripts cache their answers. Both are overridable so the
# tests can exercise a first-time operator's empty machine without touching the
# operator's real files -- prepare-community-repo.sh already follows $HOME for
# the first, and smallworlds-init.sh writes the second beside itself.
COMMUNITY_CONFIG = os.environ.get("SMALLWORLDS_COMMUNITY_CONFIG") or \
    os.path.expanduser("~/.config/smallworlds/community-setup.conf")
INIT_CACHE = os.environ.get("SMALLWORLDS_INIT_CACHE") or \
    os.path.join(REPO_ROOT, ".smallworlds-cache.env")
UPSTREAM_REPO = "https://github.com/stephan271/smallworlds.git"

# The rules for an S3 endpoint and a bucket prefix belong to the generator that
# writes them into the overlay; the wizard asks it rather than keeping a second
# copy that could accept what the generator then refuses mid-install. Loaded
# from this file's own checkout, not REPO_ROOT, which tests point at stubs.
_storage_spec = importlib.util.spec_from_file_location(
    "generate_storage_patches",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "generate_storage_patches.py"))
storage = importlib.util.module_from_spec(_storage_spec)
_storage_spec.loader.exec_module(storage)


def default_bucket_prefix(domain: str, env_ext: str) -> str:
    """Mirrors DEFAULT_BUCKET_PREFIX in prepare-community-repo.sh."""
    raw = f"sw-{domain}{env_ext}".lower().replace(".", "-")
    return re.sub(r"[^a-z0-9-]", "", raw)[:30].rstrip("-")

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
    {"name": "collabora", "label": "Collabora", "default": False, "weight": "heavy",
     "choice_group": "office", "requires": "nextcloud", "auto_select_requires": True,
     "requires_note": "An Office editor requires Files (Nextcloud).",
     "description": "Mature online editor with strong native OpenDocument support."},
    {"name": "euro-office", "label": "Euro-Office", "default": False, "weight": "heavy",
     "choice_group": "office", "requires": "nextcloud", "auto_select_requires": True,
     "requires_note": "An Office editor requires Files (Nextcloud).",
     "description": "New European editor focused on DOCX, XLSX and PPTX compatibility."},
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

CHOICE_GROUPS = [{
    "name": "office",
    "label": "Office editor inside Files",
    "none_label": "No online office editor",
    "description": "Choose one document server. You can also leave online editing disabled.",
}]


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
        # A machine directly on the internet (docs/adr/0055). The page offers
        # it as a third kind of machine; underneath it is the local target
        # with public exposure and a host firewall.
        "host_firewall": "yes" if cached.get("LOCAL_FIREWALL") == "yes" else "no",
        # Who keeps the DNS records (docs/adr/0056). `local_dns_remembered`
        # lets the page suggest "by hand" for a rented server without
        # overriding an answer this machine already gave.
        "local_dns": "manual" if cached.get("LOCAL_DNS") == "manual" else "hetzner",
        "local_dns_remembered": bool(cached.get("LOCAL_DNS")),
        "library_dir": cached.get("LIBRARY_DIR", ""),
        "photo_backend": stored.get("STORED_PHOTO_BACKEND") or "immich",

        # The overlay's record wins, as it does in smallworlds-init.sh: the
        # overlay is what was actually written for one mode or the other.
        "object_storage": stored.get("STORED_OBJECT_STORAGE")
                          or cached.get("OBJECT_STORAGE") or "garage",
        "s3_endpoint": stored.get("STORED_S3_ENDPOINT") or cached.get("S3_ENDPOINT", ""),
        "s3_region": stored.get("STORED_S3_REGION") or cached.get("S3_REGION", ""),
        "s3_bucket_prefix": stored.get("STORED_S3_BUCKET_PREFIX")
                            or cached.get("S3_BUCKET_PREFIX", ""),
        "s3_access_key_id": cached.get("S3_ACCESS_KEY_ID", ""),
        "s3_backup_endpoint": stored.get("STORED_S3_BACKUP_ENDPOINT")
                              or cached.get("S3_BACKUP_ENDPOINT", ""),
        "s3_backup_region": stored.get("STORED_S3_BACKUP_REGION")
                            or cached.get("S3_BACKUP_REGION", ""),
        "s3_backup_access_key_id": cached.get("S3_BACKUP_ACCESS_KEY_ID", ""),

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
        "has_saved_s3_secret": bool(cached.get("S3_SECRET_ACCESS_KEY")),
        "has_saved_s3_backup_secret": bool(cached.get("S3_BACKUP_SECRET_ACCESS_KEY")),

        "catalog": {
            "always": ALWAYS_INSTALLED,
            "optional": OPTIONAL_APPS,
            "choice_groups": CHOICE_GROUPS,
        },
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

    object_storage = answers.get("object_storage") or "garage"
    external = object_storage == "external"
    if object_storage not in ("garage", "external"):
        problem("object_storage", "Choose where files and backups are stored.")
    if external:
        for field, check in (("s3_endpoint", storage.parse_endpoint),
                             ("s3_bucket_prefix", storage.validate_prefix)):
            try:
                check((answers.get(field) or "").strip())
            except storage.StorageConfigError as error:
                problem(field, str(error)[0].upper() + str(error)[1:] + ".")
        if not (answers.get("s3_region") or "").strip():
            problem("s3_region", "A region is required — the provider documents which.")
        if not (answers.get("s3_access_key_id") or "").strip():
            problem("s3_access_key_id", "The provider's access key ID is required.")
        if not answers.get("s3_secret_access_key") and not answers.get("use_saved_s3_secret"):
            problem("s3_secret_access_key", "The provider's secret access key is required.")
        if (answers.get("s3_backup_endpoint") or "").strip():
            try:
                storage.parse_endpoint(answers["s3_backup_endpoint"].strip())
            except storage.StorageConfigError as error:
                problem("s3_backup_endpoint", str(error)[0].upper() + str(error)[1:] + ".")
            if not (answers.get("s3_backup_region") or "").strip():
                problem("s3_backup_region", "The backup endpoint needs its region too.")
            if not (answers.get("s3_backup_access_key_id") or "").strip():
                problem("s3_backup_access_key_id", "The backup endpoint needs its own access key ID.")
            if not answers.get("s3_backup_secret_access_key") \
                    and not answers.get("use_saved_s3_backup_secret"):
                problem("s3_backup_secret_access_key",
                        "The backup endpoint needs its own secret access key.")

    target = answers.get("target")
    if target == "hetzner":
        if not answers.get("hcloud_token") and not answers.get("use_saved_hcloud_token"):
            problem("hcloud_token", "A Hetzner Cloud API token is required to provision the VM.")
    elif target == "local":
        if not (answers.get("ssh_target") or "").strip():
            problem("ssh_target", "Enter the machine's SSH target, or localhost to use this one.")
        if not (answers.get("data_dir") or "").startswith("/"):
            problem("data_dir", "The data directory must be an absolute path.")
        library_dir = (answers.get("library_dir") or "").strip()
        if library_dir and not library_dir.startswith("/"):
            problem("library_dir", "The photo library directory must be an absolute path.")
        if answers.get("host_firewall") == "yes" and answers.get("local_public") != "yes":
            problem("host_firewall",
                    "A host firewall is for a machine directly on the internet — "
                    "it only applies when the apps are reachable from there.")
        # With an external provider there is no backup directory at all
        # (docs/adr/0053), so nothing about it can be wrong.
        if not external:
            if not (answers.get("backup_dir") or "").startswith("/"):
                problem("backup_dir", "The backup directory must be an absolute path.")
            if answers.get("data_dir") and answers.get("data_dir") == answers.get("backup_dir"):
                problem("backup_dir", "Backups cannot live in the data directory (docs/adr/0048).")
        manual_dns = answers.get("local_dns") == "manual"
        if manual_dns and answers.get("local_public") != "yes":
            problem("local_dns",
                    "Keeping DNS by hand only works for a machine reachable from the "
                    "internet: its certificates are proven by Let's Encrypt fetching a "
                    "file from port 80.")
        needs_dns = (not manual_dns) and (answers.get("local_tls") == "letsencrypt"
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

    if (answers.get("photo_backend") or "immich") not in ("immich", "gallery"):
        problem("photo_backend", "Photos runs either Immich or Gallery.")

    apps = list(answers.get("apps") or [])
    known_apps = {app["name"] for app in OPTIONAL_APPS}
    unknown_apps = sorted(set(apps) - known_apps)
    if unknown_apps:
        problem("apps", "Unknown applications were selected: " + ", ".join(unknown_apps))
    if len(apps) != len(set(apps)):
        problem("apps", "An application was selected more than once.")
    for group in CHOICE_GROUPS:
        selected = [
            app["name"] for app in OPTIONAL_APPS
            if app.get("choice_group") == group["name"] and app["name"] in apps
        ]
        if len(selected) > 1:
            problem("apps", f"Choose only one {group['label'].lower()}.")
    office = next((name for name in ("collabora", "euro-office") if name in apps), None)
    if office and "nextcloud" not in apps:
        problem("apps", f"{office} requires Files (Nextcloud).")

    return problems


# ---------------------------------------------------------------------------
# The dry-run plan
# ---------------------------------------------------------------------------

_SUBDOMAIN_BY_APP = {
    "nextcloud": "files", "immich": "photos", "forgejo": "git",
    "excalidraw": "whiteboard", "jitsi": "meet", "collabora": "office",
    "euro-office": "office",
    "plane": "plan", "stalwart": "mail", "bulwark": "webmail",
    "pod-gateway": "pod",
}


def _hostnames(domain: str, env_ext: str, apps: list[str]) -> list[dict]:
    pairs = [("identity", "Identity"), ("dashboard", "Dashboard"), ("join", "Invitations")]
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
    external = answers.get("object_storage") == "external"
    if external:
        s3_host = storage.parse_endpoint(answers["s3_endpoint"].strip())[1]
        backup_endpoint = (answers.get("s3_backup_endpoint") or "").strip()
        backup_host = storage.parse_endpoint(backup_endpoint)[1] if backup_endpoint else s3_host
        storage_summary = (
            f"at {s3_host}, in buckets named {answers['s3_bucket_prefix'].strip()}-…"
            + (f"; backups at {backup_host}" if backup_host != s3_host else
               "; backups in separate buckets at the same provider"))
    else:
        storage_summary = "on the machine itself (Garage), with backups on its second disk"

    rented = target == "local" and answers.get("host_firewall") == "yes"
    manual_dns = target == "local" and answers.get("local_dns") == "manual"
    wildcard = f"*{env_ext}.{domain}"
    gallery = "immich" in apps and answers.get("photo_backend") == "gallery"
    library_dir = (answers.get("library_dir") or "").strip()
    if "immich" not in apps:
        photos_summary = "not installed"
    elif gallery:
        photos_summary = "Gallery, with the photos in S3 (" + (
            "at the provider" if external else "the in-cluster Garage") + ")"
    else:
        photos_summary = "Immich, with the photos on the machine" + (
            f" in {library_dir}" if library_dir and target == "local" else "")

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
            "Let's Encrypt, proven over HTTP — no DNS provider account needed"
            if answers.get("local_tls") == "letsencrypt" and manual_dns else
            "Let's Encrypt, over a DNS-01 challenge — no port forwarding and "
            "no public IP needed"
            if answers.get("local_tls") == "letsencrypt"
            else "self-signed, which breaks single sign-on between the applications"
        )
        reachable = (
            f"from the internet, through the record {wildcard} that you keep yourself"
            + (", behind a firewall the installer sets up" if rented else "")
            if manual_dns else
            "from the internet, behind a firewall the installer sets up on the machine"
            if rented else
            "from the internet, with an in-cluster job maintaining the A records"
            if answers.get("local_public") == "yes"
            else "from your LAN only, resolved through your router's DNS or /etc/hosts"
        )
        steps.append({
            "id": "provision",
            "title": "Prepare the machine",
            "detail": (
                f"Install k3s and Argo CD on {answers['ssh_target']} over SSH, with "
                f"data in {answers['data_dir']} and backups "
                + ("sent to the S3 provider — no second disk is used."
                   if external else
                   f"in {answers['backup_dir']} on a separate device.")
            ),
        })

    steps.extend([
        {
            "id": "secrets",
            "title": "Generate and plant the credentials",
            "detail": (
                "Generate the Keycloak, Argo CD, Grafana"
                + ("" if external else ", Garage") + " and invite "
                "credentials and place them on the node as Kubernetes Secrets"
                + (", together with your S3 provider key" if external else "") + ". They "
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
    if external:
        warnings.append(
            "Files, photo backups and database backups will be stored at the "
            "S3 provider unencrypted by SmallWorlds — whoever runs that "
            "storage can read them. A provider outage makes files unavailable "
            "even while the cluster itself is up."
        )
        if backup_host == s3_host:
            warnings.append(
                "Backups share the provider with the live data. That survives "
                "losing this machine, but not losing the provider account — "
                "give a separate backup endpoint to cover that too."
            )
        if "pod-gateway" in apps:
            warnings.append(
                "The pod archive will sit in a bucket at the backup provider "
                "instead of on a disk of this cluster, which docs/adr/0047 "
                "avoided on purpose. Members' own devices still pull their copy."
            )
        if target == "hetzner":
            warnings.append(
                "The Hetzner target still creates its second (backup) volume; "
                "it stays empty with external storage and can be removed by hand."
            )
    if target == "local" and answers.get("local_tls") != "letsencrypt":
        warnings.append(
            "Self-signed certificates mean single sign-on does not work at all: "
            "every application validates Keycloak's certificate against its own "
            "trust store with no override, so Files, Photos and Meet cannot "
            "complete OIDC discovery. Choose this only for a throwaway cluster."
        )
    if gallery:
        warnings.append(
            "Gallery is a community fork of Immich. Its releases can change the "
            "database in ways plain Immich cannot read back, so moving to "
            "Immich later is a migration, not a setting. Its storage layer has "
            "not been restore-drilled here the way Immich's has."
        )
        if "pod-gateway" in apps:
            warnings.append(
                "The nightly export of members' photos into their pods reads "
                "the library disk, which Gallery does not use — the export is "
                "switched off and nothing fills the pods."
            )
    if manual_dns:
        warnings.append(
            f"Before installing, create one DNS record at your DNS provider: an A "
            f"record named {wildcard} pointing at the server's public address. The "
            "install checks it first and stops, having changed nothing, if it does "
            "not resolve yet."
        )
        if "stalwart" in apps:
            warnings.append(
                "With DNS kept by hand the mail records (MX, SPF, DKIM, DMARC) are "
                "not created for you. Mail is not delivered until you enter them "
                "yourself — see doc/mail.md."
            )
    if rented:
        warnings.append(
            "The installer will close every inbound port on the machine except "
            "SSH, 80, 443, 6443 and what the selected applications listen on. "
            "It keeps the port your SSH server uses open; check that you can "
            "still open a second SSH session before closing the first."
        )
        if "stalwart" in apps:
            warnings.append(
                "Mail needs a reverse DNS (PTR) record for the server's address "
                "pointing at the mail hostname. Set it in your provider's control "
                "panel — the installer can only do that at Hetzner."
            )
    if target == "local" and answers.get("local_public") == "yes" and not rented and manual_dns:
        warnings.append(
            "Internet exposure needs a fixed public IPv4 (no CGNAT) and these "
            "forwards on your router to the machine: 80/tcp, 443/tcp and "
            "10000/udp. With DNS kept by hand nothing updates the record if "
            "your connection's address changes."
        )
    if target == "local" and answers.get("local_public") == "yes" and not rented and not manual_dns:
        warnings.append(
            "Internet exposure needs a public IPv4 (no CGNAT) and these forwards "
            "on your router to the machine: 80/tcp, 443/tcp and 10000/udp."
        )
    if "stalwart" in apps and target == "local" and not rented:
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
            "target": ("a Hetzner Cloud VM" if target == "hetzner" else
                       f"the rented server {answers['ssh_target']}" if rented
                       else answers["ssh_target"]),
            "photos": photos_summary,
            "certificates": certificates,
            "reachable": reachable,
            "storage": storage_summary,
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
