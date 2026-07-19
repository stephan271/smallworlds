---
status: accepted
---

# Acquire separately signed bootstrap assets through a versioned source

Bootstrap assets will be distributed as separately signed, release-specific archives rather than embedded in each Bootstrap Launcher binary. A versioned launcher-owned asset source resolves only catalogued artifacts for a selected compatible SmallWorlds release, verifies their pinned checksum and signature, and stores them in private launcher cache storage. This avoids coupling launcher releases to every OpenTofu, provider, bootstrap, or network-client artifact refresh while preserving repeatable release manifests and resumable downloads.

## Decision record (OD-001)

| Criterion | Embedded assets | Separately signed archive |
| --- | --- | --- |
| Launcher download size | Grows with every tool and platform | Small, platform-specific assets are fetched only when needed |
| Release cadence | Requires a launcher release for asset refresh | Asset manifest can add a compatible signed release independently |
| Integrity | Binary signature covers contents | Manifest pins archive checksum and signing key verifies provenance |
| Interrupted setup | Re-download launcher | HTTP range resume from private cache |
| Downgrade/substitution | Coupled to binary version | Explicit release/catalog compatibility rejects unknown versions |
| Future Offline Bundle | Requires repackaging the launcher | Can implement the same source interface from a verified bundle |

The separate-archive approach is accepted because it keeps platform-specific provisioning dependencies outside the browser and launcher executable while retaining an exact verification boundary. It does not make bootstrap offline-capable: network requirements remain visible until a separately designed Offline Bundle is implemented.

## Consequences

The production asset manifest must be published with pinned URLs, checksums, signatures, compatible release range, and trusted signing key material. The browser may choose only a release and receive safe status; it may not submit arbitrary URLs, paths, executables, or keys. The asset source interface has a future Offline Bundle implementation point, but the first release exposes that only as roadmap text.
