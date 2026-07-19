# Qualify localization, accessibility, and mobile operation

Status: ready-for-agent

## What to build

Audit and harden all completed Operator Console journeys so English and German are complete authored experiences and primary setup, observation, planning, diagnostics, and recovery paths meet WCAG 2.2 AA. This issue closes release-wide gaps; it does not defer basic localization or accessibility from earlier browser-facing slices.

Covers PRD user stories 118–126.

## Acceptance criteria

- [ ] English remains the canonical catalog and every English key has reviewed German content with matching safe parameters and no runtime machine translation.
- [ ] Dates, durations, byte sizes, quantities, and provider currencies use the selected locale consistently without changing stable API values.
- [ ] Primary workflows pass automated axe checks and keyboard-only Playwright journeys in both languages.
- [ ] Focus order/restoration, dialogs, validation errors, navigation, and async plan completion are understandable without a pointer.
- [ ] Workflow Run progress uses throttled meaningful live summaries rather than announcing every event or log line.
- [ ] Capability and workflow states use text and icons as well as color and remain usable in light, dark, high-contrast, and reduced-motion modes.
- [ ] Status and diagnostics fully reflow at phone width, while setup and plan review remain functional for emergency mobile use without hover-only information.
- [ ] Charts and timelines have equivalent text/table representations, and touch targets and zoom/reflow meet WCAG 2.2 AA expectations.
- [ ] A documented manual review closes or explicitly records every remaining accessibility and German-copy defect before stable release.

## Blocked by

- [Issues 11–24](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
