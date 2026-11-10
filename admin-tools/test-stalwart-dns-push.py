#!/usr/bin/env python3
"""The Stalwart init job's DNS push, run against a fake Hetzner API.

The script is taken out of tenants/stalwart/stalwart-init-job.yaml as it is
deployed, so these tests cover the real thing. Nothing leaves this machine.

Covers the two faults found on Ring 0 (2026-11-10): records were published
pointing at Stalwart's pod name, and a wrong record could never be corrected
because existing records past the first page were not seen.
"""
import io
import json
import os
import pathlib
import textwrap
import unittest
import urllib.error

JOB = (pathlib.Path(__file__).resolve().parent.parent
       / "infrastructure/kubernetes/tenants/stalwart/stalwart-init-job.yaml")


def push_script() -> str:
    lines = JOB.read_text().splitlines()
    start = next(i for i, l in enumerate(lines) if "> /tmp/push_dns.py" in l) + 1
    end = next(i for i in range(start, len(lines)) if lines[i].strip() == "EOF")
    return textwrap.dedent("\n".join(lines[start:end]))


class FakeHetzner:
    def __init__(self, existing):
        self.pages = [existing[i:i + 100] for i in range(0, max(len(existing), 1), 100)]
        self.writes = []

    def urlopen(self, request, *args, **kwargs):
        url, method = request.full_url, request.get_method()
        if method != "GET":
            self.writes.append((method, url.split("/rrsets", 1)[1],
                                json.loads(request.data) if request.data else None))
            if method == "POST" and any(r["name"] == json.loads(request.data)["name"]
                                        and r["type"] == json.loads(request.data)["type"]
                                        for page in self.pages for r in page
                                        if not self._deleted(r)):
                raise urllib.error.HTTPError(url, 409, "Conflict", {}, None)
            return self._response({})
        if url.endswith("/v1/zones"):
            return self._response({"zones": [{"name": "smallworlds.network", "id": "z1"}]})
        page = int(url.split("page=")[-1])
        next_page = page + 1 if page < len(self.pages) else None
        return self._response({"rrsets": self.pages[page - 1],
                               "meta": {"pagination": {"next_page": next_page}}})

    def _deleted(self, rrset):
        return ("DELETE", f'/{rrset["name"]}/{rrset["type"]}', None) in self.writes

    @staticmethod
    def _response(body):
        class R(io.BytesIO):
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False
        return R(json.dumps(body).encode())


def zone(target):
    return f"""
canary.smallworlds.network. IN MX 10 {target}.
_imaps._tcp.canary.smallworlds.network. IN SRV 0 1 993 {target}.
autoconfig.canary.smallworlds.network. IN CNAME {target}.
canary.smallworlds.network. IN TXT "v=spf1 mx -all"
"""


def filler(n):
    return [{"name": f"other{i}", "type": "A", "records": [{"value": "192.0.2.1"}]} for i in range(n)]


class DnsPush(unittest.TestCase):
    def run_push(self, zone_file, existing):
        api = FakeHetzner(existing)
        env = {"HCLOUD_TOKEN": "t", "DOMAIN": "smallworlds.network", "ENV_EXT": ".canary",
               "MAIL_DOMAIN": "canary.smallworlds.network",
               "MAIL_HOSTNAME": "mail.canary.smallworlds.network"}
        import sys
        import urllib.request
        saved = (urllib.request.urlopen, sys.stdin, sys.stdout, dict(os.environ))
        urllib.request.urlopen, sys.stdin, sys.stdout = api.urlopen, io.StringIO(zone_file), io.StringIO()
        os.environ.update(env)
        code = 0
        try:
            exec(compile(push_script(), "push_dns.py", "exec"), {"__name__": "__main__"})
        except SystemExit as e:
            code = e.code or 0
        finally:
            output = sys.stdout.getvalue()
            urllib.request.urlopen, sys.stdin, sys.stdout, env_saved = saved
            os.environ.clear()
            os.environ.update(env_saved)
        return code, output, api.writes

    def test_records_pointing_at_the_pod_are_never_published(self):
        code, out, writes = self.run_push(zone("stalwart-mail-74bbc9c9b-b4mtq"), filler(3))
        self.assertEqual(1, code)
        self.assertIn("Refusing to push DNS", out)
        self.assertIn("canary MX -> stalwart-mail-74bbc9c9b-b4mtq.", out)
        self.assertEqual([], writes, "nothing may be written")

    def test_a_wrong_record_on_a_later_page_is_replaced(self):
        wrong = {"name": "canary", "type": "MX", "records": [{"value": "10 stalwart-mail-74bbc9c9b-b4mtq."}]}
        code, out, writes = self.run_push(zone("mail.canary.smallworlds.network"), filler(150) + [wrong])
        self.assertEqual(0, code, out)
        self.assertIn(("DELETE", "/canary/MX", None), writes)
        posted = [w[2] for w in writes if w[0] == "POST" and w[2]["type"] == "MX"]
        self.assertEqual([[{"value": "10 mail.canary.smallworlds.network."}]], [p["records"] for p in posted])
        self.assertNotIn("409", out)

    def test_records_already_right_on_a_later_page_are_left_alone(self):
        right = [{"name": "canary", "type": "MX", "records": [{"value": "10 mail.canary.smallworlds.network."}]},
                 {"name": "_imaps._tcp.canary", "type": "SRV",
                  "records": [{"value": "0 1 993 mail.canary.smallworlds.network."}]},
                 {"name": "autoconfig.canary", "type": "CNAME",
                  "records": [{"value": "mail.canary.smallworlds.network."}]},
                 {"name": "canary", "type": "TXT", "records": [{"value": '"v=spf1 mx -all"'}]}]
        code, out, writes = self.run_push(zone("mail.canary.smallworlds.network"), filler(120) + right)
        self.assertEqual(0, code, out)
        self.assertEqual([], writes)
        self.assertIn("The zone holds 124 RRsets.", out)


if __name__ == "__main__":
    unittest.main()
