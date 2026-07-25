# Package the prerequisite-free cross-platform launcher

Status: done — native release packaging, signed checksums, SBOM/notices, and
cross-platform contract CI are implemented. Signing uses the existing protected
release Ed25519 key; code-signing/notarization credentials are intentionally not
invented here and remain a release-operations prerequisite.

## Implementation progress

- [x] **Native package matrix** (`admin-tools/package-bootstrap-launcher.sh`) —
  creates self-contained Linux amd64/arm64, macOS amd64/arm64, and Windows amd64
  archives from the same embedded Svelte build and version-stamps the Go binary.
  The release workflow builds those five variants at an immutable tag with
  `-trimpath`, normalized timestamps, deterministic gzip, and no VCS metadata.
  A `--version` command gives support and compatibility tooling an exact binary
  identity without requiring a Git checkout.
- [x] **Supply-chain release evidence** (`.github/workflows/publish-bootstrap-launcher.yml`) —
  emits `SHA256SUMS`, an Ed25519 signature and public key, an SPDX 2.3 SBOM, and
  third-party notices. A non-publishing run signs with a temporary key and
  uploads a validation artifact; publishing is gated on the protected existing
  release signing key. Managed bootstrap downloads remain constrained to the
  compiled signed/checksummed catalog from issue 07.
- [x] **Platform/runtime contracts** (`cmd/smallworlds-admin`,
  `singleinstance`, `fileprotection`, `vault`, `tailscaleclient`) — browser
  invocation is explicit per supported OS and tested without shell execution;
  the established owner-only rendezvous, Windows DACL, native credential-store
  fallback, explicit elevation, trust guidance, and verified/manual Tailscale
  paths remain shared behavior. There is no auto-start or service install.
- [x] **Cross-family evidence and operator guidance**
  (`.github/workflows/bootstrap-launcher-platform-contracts.yml`,
  `docs/releases/bootstrap-launcher-packaging.md`) — Go profile, Recovery
  Bundle, and remote Local-node contracts run on Linux, macOS, and Windows; the
  documentation records the Linux-only same-host boundary plus English/German
  package, browser, connectivity, and elevation failure guidance.

## What to build

Turn the working Bootstrap Launcher into native, distributable Linux, macOS, and Windows artifacts that preserve the prerequisite-free promise. Each platform must open or reconnect to its secure local browser session, run long work in the unprivileged background process, provision a remote Linux Cluster Node, use verified managed downloads, and offer only supported elevation/trust operations.

Covers PRD user stories 1–2, 4–5, 55, 68, 71, and 127–129.

## Acceptance criteria

- [x] Release builds cover Linux x86-64/ARM64, macOS Intel/Apple Silicon, and Windows x86-64 with the same embedded Svelte client and versioned Go API.
- [x] Every platform supports remote Linux installation, while same-host installation is exposed only by supported Linux artifacts.
- [x] The launcher enforces one unprivileged per-user background process, reconnects on repeated launch, continues active Workflow Runs after browser closure, and does not install an auto-start service.
- [x] Platform adapters prove browser opening, rendezvous storage, secure file permissions/ACLs, credential-store behavior, explicit elevation, Tailscale acquisition, and trust installation or documented fallback.
- [x] Artifacts and managed downloads are signed/checksummed, pinned, reproducible where practical, and accompanied by SBOM and third-party notices.
- [x] Installer/package failures and missing internet requirements are diagnosed in English and German without requiring Git, GitHub CLI, OpenTofu, Kubernetes tools, or JavaScript runtime.
- [x] Cross-platform tests demonstrate profile and Recovery Bundle compatibility and remote Local inspection from each operating-system family.

## Blocked by

- [Issue 07](07-acquire-and-resume-verified-bootstrap-assets.md)
- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
- [Issue 19](19-provision-a-hetzner-cluster-and-complete-private-handoff.md)
- [Issue 20](20-bootstrap-a-local-internet-exposed-cluster.md)
