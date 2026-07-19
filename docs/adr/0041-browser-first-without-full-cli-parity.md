---
status: accepted
---

# Make the browser the only complete first-release client

The Svelte interface will be the only complete workflow client in the first release. The launcher binary retains operational commands such as `serve`, `version`, `diagnostics`, and Recovery Bundle export/import, while provisioning, planning, approval, and assessment logic remains in reusable Go modules behind the versioned backend interface. A full non-interactive automation CLI is deferred rather than forcing two complete user experiences into the initial implementation.
