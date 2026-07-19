---
status: accepted
---

# Use OpenAPI JSON HTTP endpoints and Server-Sent Events

The static Svelte client will communicate with Go through versioned JSON HTTP endpoints under `/api/v1`, described by a checked-in OpenAPI 3.1 contract that generates TypeScript request and response types. Server-Sent Events carry one-way Workflow Run and Capability Assessment updates, while planning, approval, cancellation, and queries use ordinary HTTP requests. The first release will not add GraphQL or a general WebSocket protocol because neither matches the predominantly request/response plus event-stream interaction model.
