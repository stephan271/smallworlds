# Establish a GitHub-hosted GitOps Overlay

Status: ready-for-agent

## What to build

Let an Operator establish a private GitHub-hosted GitOps Overlay without installing Git or GitHub CLI. The Setup Journey guides creation of a fine-grained token, validates its effective authority and expiry, creates and initializes the repository, then guides rotation from temporary repository-creation authority to narrower repository-scoped ongoing authority. Later Desired Configuration changes must be represented by branches, diffs, and pull requests rather than hidden live-cluster mutation.

Covers PRD user stories 24–28 and 31–35, plus credential representation from story 110.

## Acceptance criteria

- [ ] The journey explains and deep-links to the required fine-grained token settings and validates owner, permissions, repository scope, and expiry before planning mutation.
- [ ] The token is stored through the Launcher Vault and no secret value reaches Git, browser read responses, logs, plans, or Activity Records.
- [ ] An approved plan creates a private repository when requested and pushes a deterministic initial GitOps Overlay without invoking globally installed `git` or `gh`.
- [ ] The resulting repository pins an exact compatible SmallWorlds release and records stable remote repository and commit identities in the Cluster Profile.
- [ ] A Journey Task requires rotation to repository-scoped ongoing authority and verifies the replacement before retiring temporary creation authority.
- [ ] A subsequent configuration change displays the exact diff, creates a branch and pull request, never force-pushes or merges automatically, and reports the remote commit and proposal URL.
- [ ] Contract tests cover permission failures, expiry, pagination, conflicts, rate limiting, ambiguous partial completion, and safe retry/reinspection.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 04](04-select-cluster-capabilities-and-preview-a-gitops-overlay.md)
