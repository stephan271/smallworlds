---
status: accepted
---

# Make the browser the only complete first-release client

> **Still holds; the console parts do not.** The browser-first commitment is
> kept by the Setup Wizard (`docs/adr/0051`). The launcher and its Go modules
> are gone.

The Svelte interface will be the only complete workflow client in the first release. The launcher binary retains operational commands such as `serve`, `version`, `diagnostics`, and Recovery Bundle export/import, while provisioning, planning, approval, and assessment logic remains in reusable Go modules behind the versioned backend interface. A full non-interactive automation CLI is deferred rather than forcing two complete user experiences into the initial implementation.
