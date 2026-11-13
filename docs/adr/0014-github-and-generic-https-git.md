---
status: accepted
---

# Support GitHub and generic HTTPS Git first

> **Still holds; the console parts do not.** GitHub is still the first Git
> Provider (`prepare-community-repo.sh`, the Update Desk of `docs/adr/0065`).
> Generic HTTPS Git was never built. The launcher is gone (`docs/adr/0051`).

GitHub will be the first fully managed Git Provider, including private repository creation, initial push, branches, and pull requests without requiring `gh`. Generic HTTPS Git will support validation, clone, commit, and branch push against an existing repository using a username and token, but not provider-specific repository or pull-request creation. Provider adapters may add GitLab or Forgejo later; SSH remotes are outside the first release's cross-platform contract.
