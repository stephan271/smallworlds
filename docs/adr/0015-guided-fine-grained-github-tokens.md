---
status: accepted
---

# Use guided fine-grained GitHub tokens

First-class GitHub authentication will use Operator-created fine-grained personal access tokens rather than a centrally operated SmallWorlds OAuth or GitHub App. The Bootstrap Launcher will deep-link to token creation, state and validate the required permissions and expiry, use temporary Administration write authority when it must create a repository, and then guide rotation to a token scoped to the resulting GitOps Overlay with only ongoing contents and pull-request permissions. Tokens are stored only in the Launcher Vault or an appropriate Cluster Secret.
