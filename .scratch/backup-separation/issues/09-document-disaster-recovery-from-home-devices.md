# Document disaster recovery from home devices

Status: needs-info

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

- [ ] Operator decisions recorded on enrolment policy and the return path.
- [ ] `doc/storage-and-backup.md` §7.6 documents the full sequence with the
      recency gap and the reconciliation step.
- [ ] `doc/pod-archive.md` states plainly that unenrolled users have no pixel
      copy in this scenario.
- [ ] An alert exists for members whose device has stopped reporting.
- [ ] The sequence is exercised at least once on the lab cluster — rebuild,
      restore the database from the offsite stand-in, restore pixels from a
      simulated device, and reconcile — before it is called a runbook rather
      than a plan.
