# Domain Docs

This is a single-context repository. Engineering skills should consume its domain documentation as follows.

## Before exploring, read these

- `CONTEXT.md` at the repository root
- Relevant decisions under `docs/adr/`

If either is absent, proceed silently. Domain-modeling skills create these files lazily when terms or decisions are resolved.

## Use the glossary's vocabulary

When output names a domain concept in an issue, proposal, hypothesis, test, or implementation, use the canonical term from `CONTEXT.md`. Do not drift to synonyms that the glossary explicitly marks as avoided.

If a needed concept is missing, reconsider whether the term belongs to the project or note the gap for a domain-modeling session.

## Flag ADR conflicts

If work would contradict an existing ADR, surface the conflict explicitly rather than silently overriding the decision.
