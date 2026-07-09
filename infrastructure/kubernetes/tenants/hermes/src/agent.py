"""The Hermes agentic loop: Claude Opus 4.8 over the raw Messages API.

On an escalation it runs a bounded tool-use loop — investigate with the
read-only tools, then call `send_report` to email the admin. Uses urllib (no
anthropic SDK) to keep the service dependency-free, matching the cluster's
ConfigMap-source deploy pattern.
"""
import json
import logging
import time
import urllib.error
import urllib.request

import config
import mailer
import tools

log = logging.getLogger("agent")


def _system_prompt() -> str:
    try:
        with open(config.SYSTEM_PROMPT_PATH) as f:
            return f.read()
    except Exception:
        return ("You are Hermes, an autonomous SRE. Investigate the escalated alert "
                "using the tools, then call send_report with your findings.")


def _initial_message(alert: dict, reason: str) -> str:
    labels = alert.get("labels", {})
    annotations = alert.get("annotations", {})
    return (
        "A cluster alert was escalated to you for investigation.\n\n"
        f"Escalation reason: {reason}\n"
        f"Alert labels: {json.dumps(labels)}\n"
        f"Annotations: {json.dumps(annotations)}\n\n"
        "Investigate the root cause with the tools, then call send_report exactly "
        "once with a concrete suggested fix. Do not attempt to change the cluster."
    )


def _call(messages: list) -> dict:
    body = {
        "model": config.MODEL,
        "max_tokens": config.MAX_TOKENS,
        "system": _system_prompt(),
        "messages": messages,
        "tools": tools.TOOLS,
        "thinking": {"type": "adaptive"},
        "output_config": {"effort": config.EFFORT},
    }
    req = urllib.request.Request(
        f"{config.ANTHROPIC_API}/v1/messages",
        data=json.dumps(body).encode(),
        headers={
            "x-api-key": config.ANTHROPIC_API_KEY,
            "anthropic-version": config.ANTHROPIC_VERSION,
            "content-type": "application/json",
        },
        method="POST",
    )
    # Retry transient failures (429 rate-limit, 529 overloaded, 5xx, timeouts) —
    # routine for a busy Opus tier — with exponential backoff. A 4xx (bad
    # request, 401) is not retryable and raises immediately.
    last = None
    for attempt in range(config.API_RETRIES):
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                return json.load(resp)
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="replace")
            if e.code not in (429, 529) and e.code < 500:
                raise RuntimeError(f"Anthropic API {e.code}: {detail}") from None
            last = RuntimeError(f"Anthropic API {e.code}: {detail}")
        except Exception as e:
            last = e
        time.sleep(2 ** attempt)
    raise last


def _notify_failure(alert: dict, alertname: str, detail: str) -> None:
    """Best-effort: tell the admin an escalation could not be investigated, so a
    dropped investigation is never silent."""
    try:
        mailer.send(f"[Hermes] {alertname}: investigation FAILED",
                    f"Hermes could not investigate the escalated alert "
                    f"{json.dumps(alert.get('labels', {}))}.\n\nReason: {detail}\n\n"
                    f"This needs manual investigation.")
    except Exception:
        log.exception("could not send failure notification")


def run(alert: dict, reason: str) -> None:
    """Investigate one escalated alert end to end."""
    alertname = alert.get("labels", {}).get("alertname", "?")

    if config.DRY_RUN:
        log.info("[DRY_RUN] would investigate %s with %s (reason: %s); no API call, no email",
                 alertname, config.MODEL, reason)
        return

    messages = [{"role": "user", "content": _initial_message(alert, reason)}]
    for i in range(config.MAX_ITERATIONS):
        try:
            resp = _call(messages)
        except Exception as e:
            log.exception("Claude call failed on iteration %d for %s", i, alertname)
            _notify_failure(alert, alertname, f"Claude API error: {e}")
            return

        stop = resp.get("stop_reason")
        content = resp.get("content", []) or []
        log.info("iter=%d stop=%s blocks=%s", i, stop,
                 [b.get("type") for b in content])

        if stop == "refusal":
            log.warning("Claude refused to investigate %s: %s", alertname,
                        resp.get("stop_details"))
            _notify_failure(alert, alertname, "Claude declined to investigate (refusal).")
            return

        messages.append({"role": "assistant", "content": content})

        if stop == "tool_use":
            results = []
            terminal = None
            for block in content:
                if block.get("type") != "tool_use":
                    continue
                # Only run the first send_report of a turn; a second one would
                # send a duplicate email before we return.
                if block.get("name") == "send_report" and terminal is not None:
                    results.append({"type": "tool_result", "tool_use_id": block["id"],
                                    "content": '{"status": "already reported"}'})
                    continue
                out, term = tools.dispatch(block["name"], block.get("input", {}), alert)
                results.append({"type": "tool_result", "tool_use_id": block["id"],
                                "content": out})
                if term is not None:
                    terminal = term
            messages.append({"role": "user", "content": results})
            if terminal is not None:
                log.info("report delivered for %s (confidence=%s)",
                         alertname, terminal.get("confidence"))
                return
            continue

        # No send_report. Truncation (max_tokens) means the investigation is
        # incomplete — flag it in the subject rather than passing it off as a
        # finished report. Either way, email the text so nothing is lost.
        truncated = " [TRUNCATED — hit max_tokens]" if stop == "max_tokens" else ""
        text = "".join(b.get("text", "") for b in content if b.get("type") == "text")
        if text.strip():
            mailer.send(f"[Hermes] {alertname}: investigation (no structured report){truncated}", text)
        else:
            log.warning("%s ended (%s) with no report and no text", alertname, stop)
            _notify_failure(alert, alertname, f"investigation ended ({stop}) with no output")
        return

    log.warning("hit MAX_ITERATIONS (%d) investigating %s without a report",
                config.MAX_ITERATIONS, alertname)
    _notify_failure(alert, alertname,
                    f"reached the {config.MAX_ITERATIONS}-step limit without a report")
