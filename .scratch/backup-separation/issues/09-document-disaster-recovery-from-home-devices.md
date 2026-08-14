# Document disaster recovery from home devices

Status: complete

## What to build

The procedure for the case this whole architecture is shaped around: **both
volumes are gone.** Operational data and the local Recovery Points are lost
together — a destroyed node, a compromised cluster, a terminated account.

What survives is the offsite copy (databases, Velero, Nextcloud and the other
application buckets — but *not* pods, by design) and members' home devices,
holding the Immich pixels. Recovery is therefore: rebuild the cluster, restore
the databases from offsite, restore application buckets from offsite, and then
collect the pixels from members.

That last step is the one with no tooling, and this issue is `needs-info`
because two questions belong to the operator, not the implementer.

**Is enrolment mandatory?** Pods cover exactly the users listed in
`immich-pod-users`. An unenrolled member's photos exist on the node disk and
nowhere else, so in this scenario they are simply gone. If home devices are the
disaster tier, enrolment has to be effectively mandatory, and there should be a
metric and an alert for members without a reporting device — the gateway already
exposes `pod_gateway_device_heartbeat_age_seconds`, which is the right signal for
"this member's copy has silently stopped existing".

**How does data come back from a device?** The agent is deliberately pull-only
and outbound-only, and the gateway gives devices no write verb at all — that is
the design's spine, and the reason a compromised cluster cannot reach into
members' homes. Recovery therefore means either collecting disks physically, or
building an upload path that puts a write-capable credential in members' hands.
The second option needs its own decision record; it is not an implementation
detail.

Also worth writing down: **the recency gap.** Export is nightly and the database
backup is daily, so after a total loss the two will have drifted. Some restored
rows will point at files no device ever received, and some archived objects will
belong to assets the restored database does not know about. The procedure should
say how to reconcile — the exporter's inventory join is the natural basis.

## Acceptance criteria

- [x] Enrolment policy decided: **mandatory**. The return path is NOT decided —
      split out as issue 10, because handing a member an agent token would let
      them write into every other member's pod, so it needs a trust-model ADR
      rather than an implementation. §7.6 documents physical collection, which
      works today.
- [x] `doc/storage-and-backup.md` §7.6 documents the full sequence with the
      recency gap and the reconciliation step.
- [x] `doc/pod-archive.md` states plainly that unenrolled users have no pixel
      copy in this scenario.
- [x] An alert exists for members whose device has stopped reporting.
- [ ] The sequence is exercised at least once on the lab cluster — rebuild,
      restore the database from the offsite stand-in, restore pixels from a
      simulated device, and reconcile. **Not done.** The individual restores
      have been drilled (§7.1, §7.4) but not the rebuild-from-nothing, and
      §7.6 says so rather than presenting itself as a proven runbook.

## Comments

Enrolment being mandatory is only worth anything if a missing device is
*noticed*, and two gaps meant it would not have been:

**Nothing scraped the gateway.** `doc/pod-archive.md` has always said to alert on
`pod_gateway_device_heartbeat_age_seconds`, but there was no ServiceMonitor — the
metrics were reachable only by hand with curl. Added, plus
`apps/pod-archive-alerts.yaml`.

**A dead device disappeared instead of alerting.** The heartbeat table is
in-process, so after a gateway restart a device that stopped reporting months ago
has no series at all, and an age-based alert stays silent about exactly the case
that matters. The gateway now exports `pod_gateway_device_enrolled` for every
enrolled device from the token store, and `PodArchiveDeviceNeverReported` alerts
on enrolled-without-heartbeat. Verified: after a restart both lab devices appear
in the gauge with no heartbeat series.

**The exporter skipped unenrolled owners silently** (`export.py:152`). It now
counts them, names them with asset counts, and fails the run when
`REQUIRE_ENROLMENT` is set (default on) — a CronJob has no metrics endpoint, so
failing is the only signal that reaches Alertmanager. Verified end to end: a
second Immich member with 3 assets and no device produced

    UNPROTECTED: 3 asset(s) belong to 1 user(s) with no enrolled device
      be9025ca-...: 3 asset(s)
    Failing the run because REQUIRE_ENROLMENT is set.

and enrolling that member turned the next run green, appending their 3 assets.
