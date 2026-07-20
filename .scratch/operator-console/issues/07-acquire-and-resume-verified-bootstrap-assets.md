# Publish and acquire verified GitHub Release bootstrap assets

Status: ready-for-agent

## Implementation note

OD-001 is accepted in ADR 0046 and the Launcher now has the closed, signed,
resumable asset-source boundary. The repository does not yet publish a signed
release artifact manifest and trusted release key, so the production catalog
deliberately rejects acquisition rather than substituting an ambient tool or an
arbitrary URL. Publishing that release-engineering material is still required
before this issue can be marked complete.

### 2026-07-20 — First release source narrowed to GitHub Releases

The official `stephan271/smallworlds` GitHub Release is the sole online asset
source for the first release. A release attachment is the same signed archive
that a future Offline Bundle will import; the Operator never chooses a host,
URL, executable, or key. This avoids operating a separate artifact service or
asking an Operator to discover upstream tooling URLs. GitHub's controlled
release-asset redirect is permitted only after the Launcher has selected the
compiled, release-pinned GitHub URL and still verifies the archive checksum and
signature. Alternative online sources remain out of scope for the first
release.

### 2026-07-19 — Release payload packaging foundation

`admin-tools/build-bootstrap-assets.sh` now creates a deterministic Linux amd64
release payload only from explicit, checksum-verified K3s and Argo CD inputs.
It is tested for reproducible bytes and rejects mutable/credential-bearing URLs.
It does not replace the required published signed archive manifest and trusted
release public key, and the later Local Node bootstrap issue will consume the
payload; this issue remains incomplete until the real release material exists.

## What to build

Publish and acquire one managed bootstrap archive through the official
SmallWorlds GitHub Release. An Operator sees the GitHub Release destination,
downloads a release-pinned asset, safely resumes interruption, and receives
evidence that its version and integrity match the selected SmallWorlds release.
The release process produces the archive, checksum, signature, and compiled
catalog entry; the Operator supplies none of those values. The design must
leave a clean future seam for importing this same archive as an Offline Bundle
without claiming offline support now.

Covers PRD user stories 1, 33–34, and 127–130.

## Acceptance criteria

- [x] OD-001 records the accepted separately signed-archive decision in ADR 0046 before production acquisition behavior is merged.
- [ ] A release maintainer can attach the signed archive, checksum, and signature to the matching official SmallWorlds GitHub Release without operating separate artifact storage.
- [ ] The launcher diagnoses the official GitHub Release destination and clearly distinguishes prerequisite-free setup from offline setup.
- [ ] A selected release resolves only explicit compatible asset versions and refuses downgrade, substitution, or incompatible mutation.
- [ ] Downloads begin only from compiled official GitHub Release attachment URLs; only GitHub-controlled asset redirects are followed, and the archive is checksum/signature verified, cached in private application storage, and resumed after interruption without trusting partial content.
- [ ] Cache and download status are visible through the Setup Journey without exposing arbitrary URL or executable input surfaces.
- [ ] Managed assets are never selected from ambient `PATH` except through an explicit developer-only override.
- [ ] The versioned asset-source boundary can later accept an Offline Bundle, and the UI labels that path as future work rather than a disabled working control.

## Blocked by

- [Issue 01](01-complete-a-fake-mutation-through-the-bootstrap-launcher.md)
