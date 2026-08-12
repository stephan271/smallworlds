# pod-gateway

The append-only front door to members' personal data pods. Apps append into a
pod they cannot read; a member's home device pulls from the pod and cannot
write. Full protocol, enrolment and device setup: **`doc/pod-archive.md`**.
Rationale for building this instead of running Community Solid Server:
`docs/adr/0047-pod-archive-is-an-append-only-object-protocol.md`.

## Why this service exists at all

Append-only cannot be delegated to Garage. Its access keys are per-bucket
`read`/`write`/`owner`, there is no prefix scoping, and `write` includes
`DeleteObject` — so "may add new objects, may not read, overwrite or delete" has
no expression in Garage credentials. This gateway is that enforcement point, and
it also serves the hash-chained manifest that lets a device *prove* the server
never rewrote its history.

Nothing in `store.py` issues a delete. The gateway holds owner rights on the
bucket because S3 requires it, but the code path does not exist.

## Shape

Pure Python standard library run straight from a ConfigMap-mounted volume in a
stock `python:3.14-slim` image — the same pattern as `hermes` and `remediation`,
so there is no image to build. SigV4 is signed by hand in `src/s3.py`; it must
sign `region=garage` or Garage answers 400.

| File | Role |
|---|---|
| `src/main.py` | HTTP server. `_dispatch` holds the entire verb/principal table. |
| `src/auth.py` | Bearer tokens; only SHA-256 digests are stored. Agents append, devices read, nothing does both. |
| `src/store.py` | Append, the manifest hash chain, and paged reads. |
| `src/s3.py` | Minimal SigV4 S3 client (PUT/GET/HEAD/ListObjectsV2). |

Single replica by design: the chain is serialised by an in-process per-user
lock, and a second writer could fork it.

## Testing

```bash
python3 admin-tools/test-pod-gateway.py     # adversarial tests, no cluster needed
kubectl kustomize infrastructure/kubernetes/tenants/pod-gateway
```

The tests assert the things that would matter if they broke: an agent cannot
read, a device cannot write, a device cannot reach another member's pod, a
re-append is refused without touching the stored bytes, a bad digest stores
nothing, and a tampered or forked manifest entry fails chain verification.
