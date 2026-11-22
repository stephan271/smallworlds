"""The link hub (design §4.8): operator tools found on their Ingresses, plus a
fixed list of what lives outside the cluster.

An operator interface says how it appears here with annotations on its
`-private` Ingress, the one on the `operators` entrypoint:

    smallworlds.network/board-name:  "Argo CD"        required
    smallworlds.network/board-group: "Operate"        one of GROUPS
    smallworlds.network/board-path:  "/applications"  optional, default "/"
    smallworlds.network/board-order: "10"             optional, within the group
    smallworlds.network/board-id:    "argocd"         optional; the Board links
                                                      Application cards to "argocd"

The URL is built from the Ingress's own host, so the overlay's domain patches
carry over, and a tool that is not installed does not appear.
"""
import re

PREFIX = "smallworlds.network/board-"
GROUPS = ("Operate", "Members", "Alerts", "Updates", "Reference")

UPDATE_DESK_COMMAND = "./smallworlds-update --overlay <checkout> --kubeconfig <file>"

_GITHUB = re.compile(r"^(?:https://github\.com/|git@github\.com:|ssh://git@github\.com/)"
                     r"([^/\s]+/[^/\s]+?)(?:\.git)?/?$")


def github_web(repo_url: str | None) -> str | None:
    """https://github.com/<owner>/<repo> for a GitHub clone URL, else None."""
    match = _GITHUB.match(repo_url or "")
    return f"https://github.com/{match.group(1)}" if match else None


def _order(text: str | None) -> int:
    try:
        return int(text or "50")
    except ValueError:
        return 50


def from_ingresses(items: list[dict]) -> list[dict]:
    links, seen = [], set()
    for item in items:
        annotations = item.get("metadata", {}).get("annotations") or {}
        name = annotations.get(PREFIX + "name")
        rules = item.get("spec", {}).get("rules") or []
        host = rules[0].get("host") if rules else None
        if not name or not host:
            continue
        group = annotations.get(PREFIX + "group", "Operate")
        if group not in GROUPS:
            group = "Operate"
        path = annotations.get(PREFIX + "path", "/")
        if not path.startswith("/"):
            path = "/" + path
        key = (group, name)
        if key in seen:
            continue
        seen.add(key)
        tool = annotations.get(PREFIX + "id")
        order = _order(annotations.get(PREFIX + "order"))
        links.append({"id": tool, "name": name, "group": group, "url": f"https://{host}{path}",
                      "order": order})
        if tool == "grafana":
            # Loki's logs are read in Grafana; one Ingress, two entries.
            links.append({"id": "logs", "name": "Logs (Grafana Explore)", "group": group,
                          "url": f"https://{host}/explore", "order": order + 1})
    return links


def fixed(base_repo_url: str, base_revision: str | None, overlay_repo_url: str | None,
          ring0_board_url: str) -> list[dict]:
    """What lives outside the cluster. The ntfy topic and the heartbeat's ping
    URL are credentials (doc/alerting.md) and never appear here."""
    base = github_web(base_repo_url)
    links = []
    overlay = github_web(overlay_repo_url)
    if overlay:
        links.append({"id": "overlay", "name": "GitOps Overlay", "group": "Operate", "url": overlay,
                      "order": 90})
    links += [
        {"id": "healthchecks", "name": "healthchecks.io", "group": "Alerts",
         "url": "https://healthchecks.io/", "order": 10},
        {"id": "ntfy", "name": "ntfy app on your phone", "group": "Alerts", "url": None,
         "note": "the topic is a credential and is not shown here", "order": 20},
        {"id": "ring0", "name": "Ring 0 Board", "group": "Updates", "url": ring0_board_url, "order": 10},
    ]
    if base:
        # Docs at the commit this cluster runs, so they match what is deployed.
        ref = base_revision or "main"
        links += [
            {"id": "queue", "name": "Updates waiting on Ring 0", "group": "Updates",
             "url": f"{base}/pulls?q=is%3Apr+is%3Aopen+label%3Aring0-queue", "order": 20},
            {"id": "alerting", "name": "How alerts reach you", "group": "Reference",
             "url": f"{base}/blob/{ref}/doc/alerting.md", "order": 10},
            {"id": "backups", "name": "Storage and backups", "group": "Reference",
             "url": f"{base}/blob/{ref}/doc/storage-and-backup.md", "order": 20},
            {"id": "hardening", "name": "What is public, what is not", "group": "Reference",
             "url": f"{base}/blob/{ref}/doc/hardening.md", "order": 30},
            {"id": "after-install", "name": "Steps after an install", "group": "Reference",
             "url": f"{base}/blob/{ref}/doc/install-contabo-datalix.md", "order": 40},
        ]
    links.append({"id": "update-desk", "name": "Update Desk", "group": "Updates", "url": None,
                  "command": UPDATE_DESK_COMMAND, "note": "runs on your own machine", "order": 30})
    return links


def ordered(links: list[dict]) -> list[dict]:
    return sorted(links, key=lambda l: (GROUPS.index(l["group"]), l["order"], l["name"]))
