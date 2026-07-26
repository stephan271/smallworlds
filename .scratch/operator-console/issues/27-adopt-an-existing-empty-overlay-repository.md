# Adopt an Existing Empty Overlay Repository

Status: complete

## What to build

Make GitHub-hosted Overlay establishment resumable when the repository name is already taken. A name that already exists is not a dead end: an empty private repository the Operator created ahead of time — or one left behind by a run that failed between repository creation and the initial commit — is adopted and initialized as if it had just been created. Anything that would be overwritten is refused with a plain-language reason instead.

Follows on from [Issue 05](05-establish-a-github-hosted-gitops-overlay.md), which only ever created a fresh repository and turned GitHub's `422 name already exists` into an opaque bad-gateway failure.

## Acceptance criteria

- [x] Establishing an Overlay whose repository name is already taken adopts that repository when it is private and holds no commits, and records the same stable remote repository and commit identities as the create path.
- [x] A repository that already holds commits is left completely untouched and reported as a conflict — no blob, tree, commit, or ref call is made against it.
- [x] A repository that exists but is public is refused, because the Overlay carries the community's Desired Configuration and must not be world-readable.
- [x] Both refusals reach the Operator as plain language in the Setup Journey, not as an error code.
- [x] Contract tests cover adoption, the non-empty refusal, and the public refusal at both the GitHub adapter and the HTTP boundary.

## Implementation notes

- `github.CreatePrivateRepository` treats `422` as "inspect, then decide": it resolves the owner from the validated token, reads `GET /repos/{owner}/{name}`, and probes `GET /repos/{full_name}/git/ref/heads/{default_branch}`. GitHub answers `404` for a missing ref and `409` while a repository has no commits at all; both mean nothing would be overwritten.
- The refusals are distinct sentinel errors (`ErrRepositoryNotEmpty`, `ErrRepositoryNotPrivate`) so the HTTP layer can map them to `409 github_repository_not_empty` / `409 github_repository_not_private` rather than the generic `502`.
- Emptiness is probed, never assumed: a repository with commits may already be the live Desired Configuration of a running cluster, and overwriting it would pull the configuration out from under it.
- The generic HTTPS Git path ([Issue 06](06-establish-a-generic-https-gitops-overlay.md)) is unchanged — no provider API exists there to create a repository, so it still requires a pre-existing empty remote.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
