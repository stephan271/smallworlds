"""Tool surface for the Hermes agent.

Read-only diagnostics (Prometheus, Loki, Kubernetes) plus one terminal action,
`send_report`, which emails the admin and ends the loop. No tool mutates the
cluster — this first cut proposes nothing directly; it investigates and reports.
`open_pr` (an overlay right-sizing PR, like Tier 1) is the planned next tool.
"""
import json
import logging

import k8s
import loki
import mailer
import prometheus

log = logging.getLogger("tools")

# JSON-schema tool definitions sent to the Messages API.
TOOLS = [
    {
        "name": "query_prometheus",
        "description": "Run an instant PromQL query against the cluster's Prometheus. "
                       "Use for resource usage, saturation, and alert-expression checks "
                       "(e.g. container_memory_working_set_bytes, kube_pod_container_status_last_terminated_reason).",
        "input_schema": {
            "type": "object",
            "properties": {"query": {"type": "string", "description": "A PromQL expression"}},
            "required": ["query"],
        },
    },
    {
        "name": "query_loki",
        "description": "Run a LogQL range query against Loki to read pod logs "
                       '(e.g. {namespace="immich",pod=~"immich-server.*"}). Returns the '
                       "most recent matching log lines.",
        "input_schema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "description": "A LogQL selector/expression"},
                "hours": {"type": "number", "description": "Look-back window in hours (default 1)"},
                "limit": {"type": "integer", "description": "Max lines to return (default 100)"},
            },
            "required": ["query"],
        },
    },
    {
        "name": "get_pod_status",
        "description": "Get a pod's phase, per-container ready/restart state, and last "
                       "termination reason (OOMKilled/Error/exit code) from the Kubernetes API.",
        "input_schema": {
            "type": "object",
            "properties": {
                "namespace": {"type": "string"},
                "pod": {"type": "string"},
            },
            "required": ["namespace", "pod"],
        },
    },
    {
        "name": "get_recent_events",
        "description": "Get recent Kubernetes events in a namespace (scheduling failures, "
                       "image-pull errors, OOM kills, probe failures).",
        "input_schema": {
            "type": "object",
            "properties": {"namespace": {"type": "string"}},
            "required": ["namespace"],
        },
    },
    {
        "name": "send_report",
        "description": "Deliver your final incident report to the cluster administrator by "
                       "email and finish. Call this exactly once, when you have a root cause "
                       "and a concrete suggested fix (or have determined you cannot diagnose it).",
        "input_schema": {
            "type": "object",
            "properties": {
                "subject": {"type": "string", "description": "One-line email subject"},
                "root_cause": {"type": "string", "description": "What is actually wrong and why"},
                "suggested_fix": {"type": "string", "description": "The concrete change you recommend (e.g. a specific limit bump, config edit), or 'needs human investigation'"},
                "confidence": {"type": "string", "enum": ["high", "medium", "low"]},
            },
            "required": ["subject", "root_cause", "suggested_fix", "confidence"],
        },
    },
]


def _report_body(alert: dict, i: dict) -> str:
    labels = alert.get("labels", {})
    return (
        f"Hermes (Tier 2) investigated an escalated alert.\n\n"
        f"Alert: {labels.get('alertname')}\n"
        f"Namespace: {labels.get('namespace', '?')}   Pod: {labels.get('pod', '?')}\n"
        f"Confidence: {i.get('confidence', '?')}\n\n"
        f"ROOT CAUSE\n{i.get('root_cause', '(none provided)')}\n\n"
        f"SUGGESTED FIX\n{i.get('suggested_fix', '(none provided)')}\n\n"
        f"(Hermes proposes changes for a human to apply; it does not modify the cluster.)"
    )


def dispatch(name: str, args: dict, alert: dict) -> tuple[str, dict | None]:
    """Execute a tool. Returns (result_json_for_the_model, terminal_report_or_None).
    A non-None second element means the loop should stop (send_report ran)."""
    try:
        if name == "query_prometheus":
            return json.dumps(prometheus.query(args["query"])), None
        if name == "query_loki":
            return json.dumps(loki.query(args["query"], args.get("hours", 1.0),
                                         args.get("limit", 100))), None
        if name == "get_pod_status":
            return json.dumps(k8s.pod_status(args["namespace"], args["pod"])), None
        if name == "get_recent_events":
            return json.dumps(k8s.recent_events(args["namespace"])), None
        if name == "send_report":
            # send_report is terminal: one delivery attempt, then stop — whether
            # or not the email succeeds. Returning an error here (non-terminal)
            # would make the model call send_report again → duplicate emails.
            subject = args.get("subject", "[Hermes] incident report")
            try:
                mailer.send(subject, _report_body(alert, args))
                return json.dumps({"status": "report delivered"}), args
            except Exception as e:
                log.exception("send_report delivery failed")
                return json.dumps({"status": "delivery failed", "error": str(e)}), args
        return json.dumps({"status": "error", "error": f"unknown tool {name}"}), None
    except Exception as e:
        log.exception("tool %s failed", name)
        return json.dumps({"status": "error", "error": str(e)}), None
