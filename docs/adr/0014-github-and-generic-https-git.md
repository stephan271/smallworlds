---
status: accepted
---

# Support GitHub and generic HTTPS Git first

GitHub will be the first fully managed Git Provider, including private repository creation, initial push, branches, and pull requests without requiring `gh`. Generic HTTPS Git will support validation, clone, commit, and branch push against an existing repository using a username and token, but not provider-specific repository or pull-request creation. Provider adapters may add GitLab or Forgejo later; SSH remotes are outside the first release's cross-platform contract.
