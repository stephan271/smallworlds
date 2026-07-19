---
status: accepted
---

# Use a static SvelteKit client with an exclusively Go backend

The Operator Console will use Svelte 5 through SvelteKit and `adapter-static`, while Go exclusively owns HTTP endpoints, workflow execution, persistence, credentials, and infrastructure or cluster access. This preserves SvelteKit's routing and application structure without introducing a second server runtime, and lets the same compiled browser client be embedded in both the Bootstrap Launcher and the in-cluster Operator Console binaries.

## Consequences

The browser client must not contain provisioning logic or secrets. Browser-to-backend communication needs an explicit versioned interface, and features that normally depend on SvelteKit server rendering or server actions will instead use the Go backend.
