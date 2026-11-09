# Alerting

How the cluster tells its Operator that something is wrong. Implements the
notification half of `docs/adr/0049` ("system notification is not mail").
Configuration: `infrastructure/kubernetes/apps/alertmanager-config.yaml`.

## Why this exists

On 2026-11-05 an unattended reboot into a new kernel left the Gallery database
unable to start. For 102 hours Prometheus fired `KubePodCrashLooping`,
`KubePodNotReady` and the critical `CNPGBackupStale` — and nobody heard. Every
alert went to an email receiver that pointed at an in-cluster Stalwart (not
installed on that server) and at the placeholder `alerts@example.com`. The
rules were right; delivery was missing. Members found the outage first.

## Shape

```
Prometheus ──► Alertmanager ──┬─► smallworlds-alerts      ──► ntfy topic ──► Operator's phone
                              ├─► smallworlds-heartbeat   ──► healthchecks.io ── (silence) ──► Operator
                              └─► smallworlds-remediation ──► remediation service (Tier 1)
```

Three `AlertmanagerConfig` resources, not one. prometheus-operator gives each
its own top-level route with `continue: true`, so every alert is offered to all
three, and each is validated on its own: a missing notification Secret rejects
push and heartbeat but leaves the remediation hand-off working.

| Config | Receives | Sends to |
|---|---|---|
| `smallworlds-alerts` | every alert except severity `none`/`info` | `alerts-url` (ntfy, `?template=alertmanager`), repeats every 12 h while firing, sends *resolved* |
| `smallworlds-heartbeat` | `Watchdog` only | `heartbeat-url`, every 5 minutes |
| `smallworlds-remediation` | `KubePodCrashLooping` | `remediation` service (in-cluster, `DRY_RUN`) |

Severity `none` is `Watchdog`; `info` is `InfoInhibitor` and similar. Both fire
continuously by design, so they are discarded on the push route.

**Both endpoints live outside the cluster.** A notification service that dies
with the node cannot report the node, and Alertmanager cannot report its own
death — which is what the heartbeat is for: healthchecks.io alerts when the
pings *stop*, so total loss of the server (power, disk, network, kernel) also
reaches the Operator.

## The Secret

The two URLs are credentials (anyone who knows an ntfy topic can read it and
post to it), so they are not in Git. All three configs read
`monitoring/smallworlds-notify`:

| Key | Value |
|---|---|
| `alerts-url` | `https://ntfy.sh/<topic>?template=alertmanager` |
| `heartbeat-url` | `https://hc-ping.com/<uuid>` |

`?template=alertmanager` makes ntfy render Alertmanager's webhook JSON as
"🚨 Alert: KubePodCrashLooping" with status, severity, summary and description,
instead of raw JSON.

**New installs:** `smallworlds-init.sh` asks for both (questions 3a/3b, cached as
`ALERT_PUSH_URL` and `HEARTBEAT_URL`), and the setup wizard has the two fields
on its first panel. The installer appends `template=alertmanager` itself and
plants the Secret with the other cluster secrets. Both are optional, but a topic
without a heartbeat is refused.

**Without the Secret** the push and heartbeat configs are rejected by
prometheus-operator and alerts go nowhere — the honest state of a cluster that
was given no way to reach its Operator.

## Setting it up

1. **ntfy.** Pick a long random topic name, e.g. `openssl rand -hex 16` →
   `sw-<that>`. Install the ntfy app (Android/iOS) and subscribe to
   `https://ntfy.sh/<topic>`. No account is needed.
2. **healthchecks.io.** Create a free account and a check with **period 5
   minutes, grace 10 minutes**. Under *Integrations*, add ntfy with the same
   topic, so "the server went silent" arrives on the same phone. Copy the
   check's ping URL.
3. **Existing cluster:** create the Secret once:

   ```bash
   kubectl -n monitoring create secret generic smallworlds-notify \
     --from-literal=alerts-url='https://ntfy.sh/<topic>?template=alertmanager' \
     --from-literal=heartbeat-url='https://hc-ping.com/<uuid>'
   ```

   prometheus-operator picks it up within a minute; no restart is needed.

## Checking it works

- `kubectl -n monitoring get alertmanagerconfig` lists the three configs.
  Rejected ones are logged by `kube-prometheus-stack-operator`
  (`kubectl -n monitoring logs deploy/kube-prometheus-stack-operator | grep -i alertmanagerconfig`).
- The healthchecks.io check turns green within five minutes of the Secret
  existing — that proves the whole path Prometheus → Alertmanager → outside.
- A test push without waiting for a real fault:

  ```bash
  kubectl -n monitoring exec alertmanager-kube-prometheus-stack-alertmanager-0 -c alertmanager -- \
    amtool alert add alertname=SmallWorldsTestAlert severity=warning namespace=monitoring \
    --annotation=summary='Test alert — delete me' --alertmanager.url=http://localhost:9093
  ```

  It arrives after the 30 s group wait and resolves on its own after five
  minutes.
- `AlertmanagerFailedToSendAlerts` firing means an endpoint refused delivery —
  usually a typo in one of the URLs.

## Not covered yet

- **Hermes** still mails through Stalwart (`doc/mail.md` §2); `docs/adr/0049`
  moves it to the same ntfy topic.
- Priority: every alert arrives at ntfy's default priority. Splitting critical
  alerts into a high-priority receiver needs a second URL in the Secret.
