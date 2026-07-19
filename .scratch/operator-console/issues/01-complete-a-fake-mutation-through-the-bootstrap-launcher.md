# Complete a fake mutation through the Bootstrap Launcher

Status: ready-for-agent

## What to build

Deliver the first complete Bootstrap Launcher journey through the real browser/backend interface. An Operator starts the launcher, exchanges a one-time loopback token for a session, creates a persistent Cluster Profile, reviews and approves a harmless fake Change Plan, follows its durable Workflow Run, and sees evidence-backed verification. Closing and reopening the browser must reconnect to the same launcher and run rather than start conflicting work.

Covers PRD user stories 3–8 and 12–23.

## Acceptance criteria

- [ ] The launcher serves the embedded Svelte 5 client only on loopback and exchanges a single-use launch token for a secure session; unauthenticated API calls are denied and the token cannot be reused.
- [ ] An Operator can create, name, list, reopen, and distinguish multiple durable Cluster Profiles.
- [ ] The Setup Journey recommends the fake Journey Task while still allowing completed inputs to be revisited and revalidated.
- [ ] The fake mutation produces an immutable, secret-free Change Plan with preconditions, effects, risks, and a content digest before approval is possible.
- [ ] Changed preconditions invalidate an approval, and execution persists checkpoints, structured redacted events, cancellation state, and verification evidence.
- [ ] Closing and reopening the browser reconnects to the active Workflow Run through cursor-aware server-sent events without interrupting backend execution.
- [ ] A browser acceptance test exercises the journey through the versioned OpenAPI-described interface in English and German, including keyboard operation and meaningful progress announcements.

## Blocked by

None - can start immediately
