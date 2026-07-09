"""Loki LogQL query client (stdlib urllib).

Reads pod logs shipped by promtail (loki-stack). Range query over the last N
hours; caps the number of returned lines so a chatty workload can't blow the
model's context.
"""
import json
import time
import urllib.error
import urllib.parse
import urllib.request

import config


def query(logql: str, hours: float = 1.0, limit: int = 100) -> dict:
    """Run a LogQL range query over the last `hours`. Returns
    {"status": "success", "lines": ["<line>", ...]} or an error dict. An HTTP
    error (e.g. a 400 for bad LogQL) surfaces the upstream error body so the
    model can correct its query."""
    end = time.time()
    start = end - hours * 3600
    params = {
        "query": logql,
        "start": str(int(start * 1e9)),  # Loki wants nanosecond timestamps
        "end": str(int(end * 1e9)),
        "limit": str(limit),
        "direction": "backward",
    }
    url = f"{config.LOKI_URL}/loki/api/v1/query_range?" + urllib.parse.urlencode(params)
    try:
        with urllib.request.urlopen(url, timeout=20) as resp:
            payload = json.load(resp)
        if payload.get("status") != "success":
            return {"status": "error", "error": str(payload)}
        lines = []
        for stream in payload.get("data", {}).get("result", []):
            for _ts, line in stream.get("values", []):
                lines.append(line)
        return {"status": "success", "lines": lines[:limit]}
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace")[:1000]
        return {"status": "error", "error": f"HTTP {e.code}: {body}"}
    except Exception as e:
        return {"status": "error", "error": f"query failed: {e}"}
