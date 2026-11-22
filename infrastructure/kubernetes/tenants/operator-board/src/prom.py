"""Prometheus's instant queries (design §4.3): disk space and uptime, with the
expressions apps/node-disk-alerts.yaml uses, so the numbers match the alerts."""
import json
import urllib.parse
import urllib.request

FS = 'fstype!~"tmpfs|overlay|squashfs|ramfs"'
DISKS = 'mountpoint=~"/|/mnt/smallworlds-data"'

# name -> expression; each answers one number per series.
QUERIES = {
    "free_percent": f"100 * node_filesystem_avail_bytes{{{DISKS},{FS}}} / node_filesystem_size_bytes{{{DISKS},{FS}}}",
    # Days until full at the last 12 h's fill rate; negative or missing when it is not filling.
    "days_left": f"node_filesystem_avail_bytes{{{DISKS},{FS}}} / -deriv(node_filesystem_avail_bytes{{{DISKS},{FS}}}[12h]) / 86400",
    "pv_free_percent": "min(100 * kubelet_volume_stats_available_bytes / kubelet_volume_stats_capacity_bytes)",
    "uptime_seconds": "max(time() - node_boot_time_seconds)",
}


def query(base_url: str, expression: str, timeout: int) -> list[tuple[dict, float]]:
    url = f"{base_url.rstrip('/')}/api/v1/query?query={urllib.parse.quote(expression)}"
    with urllib.request.urlopen(urllib.request.Request(url, headers={"Accept": "application/json"}),
                                timeout=timeout) as response:
        answer = json.load(response)
    if answer.get("status") != "success":
        raise ValueError(f"Prometheus answered {answer.get('status')}: {answer.get('error', '')[:200]}")
    return [(r.get("metric", {}), float(r["value"][1])) for r in answer["data"]["result"]]


def fetch(base_url: str, timeout: int) -> dict:
    """Every query, by name. One that fails fails the whole reading: half a disk
    picture would look calmer than it is."""
    return {name: [{"metric": m, "value": v} for m, v in query(base_url, expression, timeout)]
            for name, expression in QUERIES.items()}
