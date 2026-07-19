# Establish a generic HTTPS GitOps Overlay

Status: complete

## What to build

Let an Operator use an existing generic HTTPS Git repository as the Git Provider. The Setup Journey validates access, initializes an empty repository with the GitOps Overlay, and handles later durable changes through proposal branches with clear manual merge guidance when provider-specific pull requests are unavailable.

Covers PRD user stories 29–35 and credential representation from story 110.

## Acceptance criteria

- [x] The journey accepts an existing HTTPS repository identity and username/token credential, validates safe repository access, and rejects unsupported SSH remotes clearly.
- [x] Credentials are held in the Launcher Vault and are absent from repository URLs, Git history, browser read responses, plans, events, and logs.
- [x] An approved initial plan commits and pushes the deterministic overlay to an empty repository without requiring globally installed Git tooling.
- [x] Repository and commit identities are persisted so retries compare remote state and never force-push or duplicate an ambiguous successful operation.
- [x] Later durable changes show an exact diff, push a named proposal branch, and return translated manual merge instructions without claiming a pull request was created.
- [x] Compatibility and exact release pin checks occur before any remote mutation.
- [x] Contract tests cover authentication failure, non-empty repository conflicts, concurrent ref changes, transient failures, and safe resume.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 04](04-select-cluster-capabilities-and-preview-a-gitops-overlay.md)
