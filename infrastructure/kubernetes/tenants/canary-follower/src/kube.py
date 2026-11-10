"""The few Kubernetes API calls the follower needs, over urllib.

In-cluster only: the ServiceAccount token and CA are mounted by Kubernetes.
"""
import json
import ssl
import urllib.request

SA_DIR = "/var/run/secrets/kubernetes.io/serviceaccount"


class Kube:
    def __init__(self, base: str = "https://kubernetes.default.svc", sa_dir: str = SA_DIR):
        self.base = base
        self.sa_dir = sa_dir
        self._context = ssl.create_default_context(cafile=f"{sa_dir}/ca.crt")

    def _token(self) -> str:
        # Re-read every call: projected tokens are rotated on disk.
        with open(f"{self.sa_dir}/token") as handle:
            return handle.read().strip()

    def _request(self, method: str, path: str, body: dict | None = None,
                 content_type: str = "application/json") -> dict:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(
            self.base + path, data=data, method=method,
            headers={"Authorization": f"Bearer {self._token()}",
                     "Accept": "application/json",
                     "Content-Type": content_type})
        with urllib.request.urlopen(request, timeout=30, context=self._context) as response:
            return json.load(response)

    def get(self, path: str) -> dict:
        return self._request("GET", path)

    def merge_patch(self, path: str, body: dict) -> dict:
        return self._request("PATCH", path, body, "application/merge-patch+json")
