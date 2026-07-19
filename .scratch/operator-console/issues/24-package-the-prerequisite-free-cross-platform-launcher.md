# Package the prerequisite-free cross-platform launcher

Status: ready-for-agent

## What to build

Turn the working Bootstrap Launcher into native, distributable Linux, macOS, and Windows artifacts that preserve the prerequisite-free promise. Each platform must open or reconnect to its secure local browser session, run long work in the unprivileged background process, provision a remote Linux Cluster Node, use verified managed downloads, and offer only supported elevation/trust operations.

Covers PRD user stories 1–2, 4–5, 55, 68, 71, and 127–129.

## Acceptance criteria

- [ ] Release builds cover Linux x86-64/ARM64, macOS Intel/Apple Silicon, and Windows x86-64 with the same embedded Svelte client and versioned Go API.
- [ ] Every platform supports remote Linux installation, while same-host installation is exposed only by supported Linux artifacts.
- [ ] The launcher enforces one unprivileged per-user background process, reconnects on repeated launch, continues active Workflow Runs after browser closure, and does not install an auto-start service.
- [ ] Platform adapters prove browser opening, rendezvous storage, secure file permissions/ACLs, credential-store behavior, explicit elevation, Tailscale acquisition, and trust installation or documented fallback.
- [ ] Artifacts and managed downloads are signed/checksummed, pinned, reproducible where practical, and accompanied by SBOM and third-party notices.
- [ ] Installer/package failures and missing internet requirements are diagnosed in English and German without requiring Git, GitHub CLI, OpenTofu, Kubernetes tools, or JavaScript runtime.
- [ ] Cross-platform tests demonstrate profile and Recovery Bundle compatibility and remote Local inspection from each operating-system family.

## Blocked by

- [Issue 07](07-acquire-and-resume-verified-bootstrap-assets.md)
- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
- [Issue 19](19-provision-a-hetzner-cluster-and-complete-private-handoff.md)
- [Issue 20](20-bootstrap-a-local-internet-exposed-cluster.md)
