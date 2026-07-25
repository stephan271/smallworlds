# Operator Console accessibility and localization review

This checklist is the release record for issue 25. It complements automated
checks; it does not replace review by people using assistive technology.

## Automated release gate

- `operator-console/web`: `npm run check` and `npm run build` must pass.
- Playwright runs the primary Launcher journey in English and German, checks the
  keyboard path, runs axe after each language journey, and repeats axe plus
  keyboard operation at a 375 px viewport.
- The in-cluster Console keeps its typed English/German catalog and date
  formatting contract under TypeScript checking.
- The launcher formats timestamps, quantities, and provider currency at the
  browser boundary with `Intl`; API values remain ISO/number values.
- Workflow status uses a polite summary region and a one-second poll interval.
  The text summary names state and checkpoint; event history remains a dated
  text timeline rather than a color-only chart.

## Manual stable-release review

Perform this against the exact release candidate and record the result in the
release notes. Every unchecked row is a release blocker or a documented known
limitation; do not silently waive it.

| Review | Evidence to record |
| --- | --- |
| Keyboard only | Complete create-profile, vault unlock, planning, approval, Recovery Bundle, and both decommission plans using Tab/Shift+Tab/Enter/Space. Confirm focus is visible, errors are announced, and focus returns to the next meaningful control. |
| Screen reader | Test current macOS VoiceOver and Windows Narrator with English and German. Confirm headings, form labels, live workflow summaries, plan diffs, and destructive typed confirmation are understandable. |
| Visual modes | Test light, dark, forced/high contrast, and reduced-motion modes. Confirm status icons always retain adjacent text and no meaning depends on color. |
| Mobile emergency use | At 320 px and 200% zoom, create a profile, inspect a remote node, open a plan, approve a harmless run, view diagnostics, and export/preview a Recovery Bundle. Confirm no horizontal page overflow and controls are touch-operable. |
| German copy | Review every completed launcher and in-cluster Console journey for terminology, punctuation, parameter placement, and any untranslated backend error code. |

## Current disposition

The automated gate and authored German copy are implemented in the repository.
The manual rows above require a release candidate, real assistive technologies,
and a human reviewer; they must be attached to the stable-release evidence
before issue 26 declares a stable release.
