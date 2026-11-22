"""The Kubernetes API, read-only, over urllib.

In-cluster only: the ServiceAccount token and CA are mounted by Kubernetes.
There is deliberately no method that writes: the Board changes nothing
(docs/adr/0068). tests/test_board.py checks that this stays so.
"""
import json
import ssl
import urllib.error
import urllib.request

SA_DIR = "/var/run/secrets/kubernetes.io/serviceaccount"


class Kube:
    def __init__(self, base: str = "https://kubernetes.default.svc", sa_dir: str = SA_DIR,
                 timeout: int = 5):
        self.base = base
        self.sa_dir = sa_dir
        self.timeout = timeout
        self._context = ssl.create_default_context(cafile=f"{sa_dir}/ca.crt")

    def _token(self) -> str:
        # Re-read every call: projected tokens are rotated on disk.
        with open(f"{self.sa_dir}/token") as handle:
            return handle.read().strip()

    def get_or_empty(self, path: str) -> dict:
        """A list whose resource may not exist on this cluster (its CRD is not
        installed): 404 is "none of them", never a failure."""
        try:
            return self.get(path)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                return {"items": [], "absent": True}
            raise

    def get(self, path: str) -> dict:
        request = urllib.request.Request(
            self.base + path, method="GET",
            headers={"Authorization": f"Bearer {self._token()}", "Accept": "application/json"})
        with urllib.request.urlopen(request, timeout=self.timeout, context=self._context) as response:
            return json.load(response)
