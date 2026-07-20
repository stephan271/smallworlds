---
status: accepted
---

# Acquire separately signed bootstrap assets from official GitHub Releases

Bootstrap assets will be distributed as separately signed, release-specific
archives rather than embedded in each Bootstrap Launcher binary. For the first
release, the only online source is an attachment on the matching official
`stephan271/smallworlds` GitHub Release. A versioned launcher-owned source
resolves only a compiled catalog entry for a selected compatible SmallWorlds
release, verifies its pinned checksum and signature, and stores it in private
launcher cache storage. This avoids coupling launcher releases to every
OpenTofu, provider, bootstrap, or network-client artifact refresh while
preserving repeatable release manifests and resumable downloads.

## Decision record (OD-001)

| Criterion | Embedded assets | Separately signed archive |
| --- | --- | --- |
| Launcher download size | Grows with every tool and platform | Small, platform-specific assets are fetched only when needed |
| Release cadence | Requires a launcher release for asset refresh | Asset manifest can add a compatible signed release independently |
| Integrity | Binary signature covers contents | Manifest pins archive checksum and signing key verifies provenance |
| Interrupted setup | Re-download launcher | HTTP range resume from private cache |
| Downgrade/substitution | Coupled to binary version | Explicit release/catalog compatibility rejects unknown versions |
| Future Offline Bundle | Requires repackaging the launcher | Can implement the same source interface from a verified bundle |

The separate-archive approach is accepted because it keeps platform-specific
provisioning dependencies outside the browser and launcher executable while
retaining an exact verification boundary. GitHub Releases avoid separate
artifact infrastructure in the first release. It does not make bootstrap
offline-capable: network requirements remain visible until a separately
designed Offline Bundle imports the same signed release attachment.

## Consequences

The production asset catalog must contain only a fixed GitHub Release attachment
URL, checksum, signature, compatible release range, and trusted signing key
material. The downloader may follow a GitHub-controlled release-asset redirect,
but must still verify the signed checksum before committing bytes. The browser
may choose only a release and receive safe status; it may not submit arbitrary
URLs, paths, executables, or keys. An Offline Bundle will later import the same
signed attachment; alternative online hosts are explicitly deferred.
