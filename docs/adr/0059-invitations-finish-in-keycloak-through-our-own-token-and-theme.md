---
status: accepted
---

# Invitations finish in Keycloak through our own action token and login theme

The Keycloak half of a Member Invitation (ADR 0058) now runs through two
things this repository owns, both in the provider jar Keycloak already loads
(`infrastructure/keycloak-spi/action-token-generator`):

- **an action token type of our own, `smallworlds-join`, with its handler**;
- **a login theme, `smallworlds`**, set as the realm's login theme.

A member now sees two Keycloak pages instead of four, is never asked a
question the browser poses in a pop-up, and arrives on the join page's done
page already signed in. Both pages look like the join page and speak in the
same voice. `admin-tools/bulk-invite.py` keeps Keycloak's own token and flow.

## What the first real invitation showed

The first invitation on smallworlds.network (2026-10-14,
`docs/passkey-screenshot-1.png` to `-5.png`) went through:

1. a confirmation page, "Führen Sie die folgende(n) Aktion(en) aus", with a
   link to continue;
2. the passkey page, whose button opened a `window.prompt` asking for a name
   for the passkey before the browser's own dialog;
3. the recovery codes, under a second warning box, with a "sign out from other
   devices" checkbox that means nothing to a new member;
4. "Konto aktualisiert", with a link back to the application.

It worked, but the first and last pages each cost a click and an explanation.
The prompt in step 2 looks like a phishing dialog. And the whole sequence used
"Sie", while the join page uses "du". Keycloak's source (26.6.4) shows where
each comes from:
- The confirmation page is shown by `ExecuteActionsActionTokenHandler`
  whenever the link opens in a browser without an authentication session,
  which is always the case for an invitation.
- The last page comes from the `END_AFTER_REQUIRED_ACTIONS` note that
  `AbstractActionTokenHandler` puts on every such session.
- The prompt is hard-coded in the base theme's `webauthnRegister.js`.

## The decision

**Our own token handler** (`JoinActionTokenHandler`), modelled on Keycloak's
execute-actions handler, differs in two ways:

- **It skips the confirmation page.** That page guards against acting on an
  account through a link someone else sent. Our link is never sent: `join`
  mints it and hands it to the browser that has just entered the invitation
  code and seen the username. The theme's passkey page names the account once
  more, so the check the confirmation page provided now sits where the member
  acts.
- **It finishes with a sign-in.** It removes `END_AFTER_REQUIRED_ACTIONS`, so
  Keycloak ends with an ordinary OIDC redirect to `join`'s `/done/<lang>`. The
  member is signed in at Keycloak, and the first application opens without
  another passkey prompt. Keycloak's own organisation-invitation handler does
  the same.

The token is single-use: Keycloak invalidates it once the actions are done, and
`join` mints a new one whenever the invitation is opened again.

**Our own login theme** extends `keycloak.v2`, the maintained PatternFly 5
theme, instead of the older `keycloak` theme the realm used. It changes:
- **Look:** the join page's colours, type, card and language pill, in light
  and dark mode.
- **Texts:** the du form in German, and the same wording as the join page in
  English. Only member-facing keys are overridden; everything else falls back
  to Keycloak.
- **Passkey page:** it names the account, says what the device will ask for,
  and names the passkey after the day it was made without asking. There is no
  "sign out from other devices" checkbox on the passkey page or the codes page.
- **A PatternFly fix:** the template still uses PatternFly 4's screen-reader
  class, which made "Warning alert:" visible. It is now hidden again.

The theme applies to every sign-in to the realm, not only to invitations.

## Rejected

**Doing the passkey ceremony on the join page and storing the result through
the admin API** was considered first, as the most complete simplification. It
fails on three counts:
- **Domain binding.** A passkey is bound to `identity.<domain>`, and a page on
  `join.<domain>` may not create one for it. Widening the binding to
  `<domain>` would invalidate every existing passkey, and would let any
  subdomain ask for passkey assertions.
- **No supported API.** Keycloak has no supported endpoint for importing a
  WebAuthn or recovery-code credential, so `join` would depend on Keycloak's
  internal storage format.
- **Security-critical code.** It would mean writing attestation verification
  in standard-library Python.

**Explaining the extra pages on the join page instead** was the cheap
alternative; it fixes none of them.

## Consequences

- **The extension now carries code that tracks Keycloak's internals.** The
  handler uses `AbstractActionTokenHandler`, documented for extensions but not
  promised to be stable. The theme overrides two files copied from Keycloak:
  `webauthn-register.ftl` and `resources/js/webauthnRegister.js`. On every
  Keycloak upgrade:
  - diff the two copied files against their new originals;
  - rebuild with `build.sh`, which takes the version from `pom.xml`;
  - run `tenants/join/tests/live-keycloak.sh`, which walks a whole invitation
    against the new Keycloak with a virtual passkey.
- **A new jar now restarts Keycloak.** The jar's ConfigMap keeps its name, so
  `build.sh` writes the jar's checksum into a pod annotation in
  `tenants/keycloak/values.yaml`, and a test fails if the two disagree.
  Deploying a new extension is therefore a short Keycloak outage.
- **Rollout order does not matter.** An older jar ignores the `tokenType` that
  `join` sends and mints Keycloak's own token: the old flow, still working. A
  realm whose login theme cannot be found falls back to Keycloak's built-in
  theme.

## Verification

`tenants/join/tests/live-keycloak.sh` runs Keycloak 26.6.4 with this
repository's realm and jar. It walks an invitation in German (light) and in
English (dark) with Chromium's virtual authenticator and checks each step:
- Keycloak goes straight to the passkey page, which names the account.
- No pop-up appears, and a passkey is registered.
- The twelve recovery codes are shown, without the sessions checkbox.
- The member lands on the done page, with the authorization code removed from
  the address.
- A silent authorization request succeeds, so the member is signed in.
- The invitation counts as used.

All of this passed on 2026-10-14. The screenshots are in `doc/tenant-join.md`.
