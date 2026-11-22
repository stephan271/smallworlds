"""Where this cluster stands against Ring 0 (design §4.6).

Two public reads, no token: Ring 0's Ring State (`ring0.json`, written by the
Canary Follower on the `ring-state` branch) and, for a commit older than the
25 it lists, GitHub's compare API. Both send the cluster's address to GitHub,
as Argo CD's own fetches do; RING_STATE_URL="" turns them off (ADR 0068).
"""
import json
import urllib.request


def _get(url: str, timeout: int) -> dict:
    request = urllib.request.Request(url, headers={"Accept": "application/json",
                                                   "User-Agent": "smallworlds-operator-board"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return json.load(response)


def fetch_ring0(url: str, timeout: int) -> dict:
    answer = _get(url, timeout)
    if not isinstance(answer, dict) or "head" not in answer or "commits" not in answer:
        raise ValueError("ring0.json has no head or commits")
    return answer


def compare(api: str, repository: str, base: str, head: str, timeout: int) -> dict:
    """How far `head` is from `base`: GitHub's status and counts, nothing else."""
    answer = _get(f"{api.rstrip('/')}/repos/{repository}/compare/{base}...{head}", timeout)
    return {"status": answer.get("status"), "ahead_by": answer.get("ahead_by"),
            "behind_by": answer.get("behind_by"),
            "base_date": ((answer.get("base_commit") or {}).get("commit") or {}).get("committer", {}).get("date")}
