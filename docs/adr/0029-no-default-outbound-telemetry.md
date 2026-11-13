---
status: accepted
---

# Send no outbound telemetry by default

> **Still holds; the console parts do not.** No outbound telemetry still holds,
> for the Setup Wizard and every in-cluster service. The launcher and console
> are gone (`docs/adr/0051`, `docs/adr/0062`).

The Bootstrap Launcher and Operator Console will not send analytics, usage telemetry, or automatic crash reports outside the Operator's systems by default. Metrics and logs remain on the Launcher Host or cluster, and support information is shared only through a redacted diagnostics bundle that the Operator reviews. Any future error-reporting integration must be explicit opt-in and disclose its destination and data categories; ordinary signed-release checks are the only routine external metadata requests.
