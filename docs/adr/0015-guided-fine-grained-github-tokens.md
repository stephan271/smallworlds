---
status: accepted
---

# Use guided fine-grained GitHub tokens

> **Still holds; the console parts do not.** Fine-grained GitHub tokens are
> still the rule: the Canary Follower, the Ring 0 Steward and the Update Desk
> each use one scoped to what they touch (`docs/adr/0060`, `0064`, `0065`). The
> launcher's guided token flow is gone (`docs/adr/0051`).

First-class GitHub authentication will use Operator-created fine-grained personal access tokens rather than a centrally operated SmallWorlds OAuth or GitHub App. The Bootstrap Launcher will deep-link to token creation, state and validate the required permissions and expiry, use temporary Administration write authority when it must create a repository, and then guide rotation to a token scoped to the resulting GitOps Overlay with only ongoing contents and pull-request permissions. Tokens are stored only in the Launcher Vault or an appropriate Cluster Secret.
