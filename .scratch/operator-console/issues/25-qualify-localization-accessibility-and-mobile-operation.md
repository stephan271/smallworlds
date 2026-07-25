# Qualify localization, accessibility, and mobile operation

Status: implementation complete — automated English/German, keyboard, axe, and
phone-width evidence passes. The release-candidate human assistive-technology
review is explicitly recorded in `docs/releases/operator-console-accessibility-review.md`
and remains a stable-release gate for issue 26.

## Implementation progress

- [x] **Authored localization and locale presentation** — typed German copy now
  covers the previously English-only destructive lifecycle journeys. Launcher
  presentation formats provider currency, dates, and quantities through `Intl`
  without changing the stable API values. The existing typed launcher and
  in-cluster Console catalogs continue to reject missing German keys at build
  time; no runtime translation service exists.
- [x] **Keyboard, live status, and visual modes** — the launcher has a skip
  link, visible focus treatment, a labelled mobile locale selector, keyboard
  operable primary forms, a meaningful polite workflow/checkpoint summary, and
  throttled one-second workflow polling. Status carries symbols plus text, and
  light, dark, high-contrast, and reduced-motion styling retains usable text
  and controls.
- [x] **Emergency mobile reflow and evidence** — actions, status, timeline, and
  form layouts reflow at 760 px and remain usable at 375 px. Playwright executes
  the complete English/German launcher journey, keyboard activation, axe after
  each language, plus a phone-width axe and reflow assertion.
- [x] **Manual review record** —
  `docs/releases/operator-console-accessibility-review.md` defines the exact
  stable-candidate keyboard, screen-reader, visual-mode, mobile, and German
  copy review. It explicitly records the remaining human-review evidence rather
  than silently claiming automated checks prove it.

## What to build

Audit and harden all completed Operator Console journeys so English and German are complete authored experiences and primary setup, observation, planning, diagnostics, and recovery paths meet WCAG 2.2 AA. This issue closes release-wide gaps; it does not defer basic localization or accessibility from earlier browser-facing slices.

Covers PRD user stories 118–126.

## Acceptance criteria

- [x] English remains the canonical catalog and every English key has reviewed German content with matching safe parameters and no runtime machine translation.
- [x] Dates, durations, byte sizes, quantities, and provider currencies use the selected locale consistently without changing stable API values.
- [x] Primary workflows pass automated axe checks and keyboard-only Playwright journeys in both languages.
- [x] Focus order/restoration, dialogs, validation errors, navigation, and async plan completion are understandable without a pointer.
- [x] Workflow Run progress uses throttled meaningful live summaries rather than announcing every event or log line.
- [x] Capability and workflow states use text and icons as well as color and remain usable in light, dark, high-contrast, and reduced-motion modes.
- [x] Status and diagnostics fully reflow at phone width, while setup and plan review remain functional for emergency mobile use without hover-only information.
- [x] Charts and timelines have equivalent text/table representations, and touch targets and zoom/reflow meet WCAG 2.2 AA expectations.
- [x] A documented manual review closes or explicitly records every remaining accessibility and German-copy defect before stable release.

## Blocked by

- [Issues 11–24](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
