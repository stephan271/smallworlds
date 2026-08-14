# A return path from member devices

Status: needs-triage

## What to build

Decide, and record as an ADR, how a member's home device gets its copy **back**
into a rebuilt cluster after a total loss (`doc/storage-and-backup.md` §7.6).

Today the only supported path is physical: the member brings or ships the disk
and the operator runs `restore-immich-originals.py --from-device`. That works,
needs no new credential, and is what §7.6 documents. For a community whose
members are not in the same town it is impractical.

## Why it is not just "add an upload endpoint"

The gateway's whole shape is that **no principal can both read and write**, and
devices have no write verb at all. That is what stops a compromised cluster
reaching into members' homes, and it should survive a restore rather than be
suspended for one.

The obvious shortcut — hand the member an agent token — is worse than it looks:
an agent may `PUT` into **any** pod, so a restore credential in a member's hands
would let them write into every other member's archive. Agent tokens are scoped
by *source*, not by user.

So a return path needs at least one of:

- a new principal kind: append-only, scoped to a single `user_id`, ideally
  expiring — a real change to `auth.py` and the token document;
- or an operator-mediated transfer where the bytes never authenticate to the
  gateway at all (the member uploads to somewhere the operator controls, and the
  operator writes them in).

Both are decisions about the trust model, which is why this is an ADR and not an
implementation detail.

## Questions to settle

1. Does a restore credential expire, and who revokes it?
2. Is a restore push allowed into a pod that already has objects, or only into
   an empty one? Re-appending an existing key returns `409`, which makes a
   partial re-push safe — but it also means a device cannot correct an object
   the cluster holds wrongly.
3. Does the device push to the same gateway, or to a separate one-shot intake
   that never shares a bucket with live pods?
4. What proves the bytes are the member's own? The manifest chain proves the
   *server's* history was not rewritten; it does not authenticate a device that
   is now the source of truth.

## Acceptance criteria

- [ ] An ADR records the chosen trust model and why the alternatives were
      rejected.
- [ ] `doc/pod-archive.md` gains the principal table entry if a new kind is
      introduced.
- [ ] `admin-tools/test-pod-gateway.py` covers the new boundary — in particular
      that a restore credential cannot touch another member's pod.
- [ ] §7.6 replaces the physical-collection paragraph with the built path.
