"""Prometheus instant-query client (stdlib urllib)."""
import json
import urllib.error
import urllib.parse
import urllib.request

import config


def query(expr: str) -> dict:
    """Run an instant PromQL query. Returns a compact dict the model can read:
    {"status": "success", "resultType": ..., "results": [...]} or
    {"status": "error", "error": "..."}. On an HTTP error the query returned
    (e.g. a 400 for bad PromQL) the upstream error body is surfaced so the model
    can correct its own query."""
    url = f"{config.PROMETHEUS_URL}/api/v1/query?" + urllib.parse.urlencode({"query": expr})
    try:
        with urllib.request.urlopen(url, timeout=20) as resp:
            payload = json.load(resp)
        if payload.get("status") != "success":
            return {"status": "error", "error": payload.get("error", "unknown")}
        data = payload.get("data", {})
        rtype = data.get("resultType")
        result = data.get("result", [])
        if rtype in ("vector", "matrix"):
            results = [{"metric": r.get("metric", {}),
                        "value": (r.get("value") or [None, None])[1]}
                       for r in result if isinstance(r, dict)]
        else:  # scalar / string: result is [ts, value]
            results = [{"value": result[1]}] if len(result) == 2 else []
        return {"status": "success", "resultType": rtype, "results": results}
    except urllib.error.HTTPError as e:
        body = e.read().decode(errors="replace")[:1000]
        return {"status": "error", "error": f"HTTP {e.code}: {body}"}
    except Exception as e:
        return {"status": "error", "error": f"query failed: {e}"}
