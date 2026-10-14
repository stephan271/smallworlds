---
status: accepted
---

# Members are invited through a join page that only the Operator can issue from

Members join through a small Platform Service, `join`, at `join.<domain>`. The
Operator creates a **Member Invitation** on its **Invitation Desk** and hands it
over as a message, a QR code or a printed card. The member opens it, chooses a
username, and is sent straight into Keycloak to register a passkey and take down
recovery codes. Only the Operator can invite. The member chooses their own
username. Every page the member sees, Keycloak's included, is available in
German and English. `admin-tools/bulk-invite.py` remains as the terminal route.
The design is `plans-and-walkthroughs/design-member-invitations.md`.

The decision rests on one separation: **a Member Invitation is not a Keycloak
action token.** The action token was the invitation until now, and it carries
three properties that suit Keycloak and fail the people involved:

- It lives 12 hours, because that is the realm's lifespan for admin-issued
  tokens. A member who opens it two days later meets an error page.
- It is some 600 characters long, so it cannot be typed or read out, and its QR
  code has to be printed large.
- It names an account that already exists, so the Operator chose the username
  before the member had any say.

The Member Invitation is a 10-character code of `join`'s own. It is valid for
14 days by default and works once. The action token is minted only when the
member presses **Continue**, for the account just created with the name the
member chose. If the member stops halfway, the same invitation mints a fresh
token for the same account until a passkey exists. The invitation is used up
by the passkey, not by the click.

**Only the Operator invites.** The Desk can create accounts and can give any
member a new passkey, which is the same as being able to take over any member
account. It is therefore protected like the Keycloak admin console (ADR 0038),
with two further layers:

1. It is reachable from the Private Network only.
2. It is served on a port that the public Ingress does not route to, so a
   broken private route fails closed instead of falling through to the internet.
3. It requires sign-in as the master-realm `operator` account, with password and
   TOTP, because the allow-list also admits the node's LAN and cluster addresses.

Letting members invite each other was considered and set aside. It would need
the Desk on the public side, quotas or approval, and a policy on who may invite
whom, and nobody has asked for it. The join page is public only because members
are never on the Private Network (`doc/hardening.md` §1). It acts only on the
account its invitation names, and it never takes an account from the request.

**The member chooses the username**, within the narrowest rules that every
Community Application accepts. Forgejo's are the tightest: no `@`, and no
leading, trailing or doubled separators. Names that Stalwart uses as role
addresses, and the names of the master-realm accounts, are reserved. The live
availability check requires a valid code, so the page cannot be used to list
who is a member.

**Both languages, end to end.** The join page starts in the language the
Operator chose for the invitation and offers a switch. The member's choice is
written to their Keycloak `locale` attribute. For that, the realm turns on
internationalisation with German and English, so the passkey and recovery-code
pages follow the member's choice. As a side effect, every member's sign-in page
gains a language selector.

**The service account is reduced.** `join` signs in to Keycloak as a client of
its own, which holds only `manage-users`, `view-users` and `query-users`. Those
are the roles the action-token SPI checks for. `bulk-invite`'s `realm-admin` is
left as it is, as a separate follow-up.

## Consequences

- `join` is always deployed and is not a Community Application. Its subdomain
  joins the core list in the four places that keep the subdomain map
  (`main.tf`, `generate_domain_patches.py`, `plan.py`, `dns_subdomains()`), and the
  hosts line printed for operator pages gains `join.<domain>`.
- It follows the Hermes pattern: Python standard library, no image build, source
  from a ConfigMap. Its single vendored file is a pinned JavaScript QR encoder,
  served by the app itself and never from a CDN.
- Invitations live in SQLite on a small `local-path` claim, and that claim is
  deliberately not backed up. Losing it loses open invitations, never a member,
  because members exist only in Keycloak.
- After the passkey step Keycloak returns members to `join`'s own done page, and
  from there to the Member Dashboard, instead of to the account console.
- The realm still requires an email address for the `user` role, so the join
  page asks for one, as `bulk-invite.py`'s accounts eventually had to. Nothing is
  mailed to it (ADR 0049). Whether to drop the requirement is left open.
- Until a passkey is registered, an invitation link remains a bearer credential
  for that one account, exactly as the action-token link was. The code travels
  in the URL fragment, so it never reaches an access log.

## Not done

- **Invitations by members.** This needs its own decision, as described above.
- **Sending invitations by mail from the Desk.** Mail is opt-in (ADR 0049). The
  Desk produces a message for the Operator to send through whatever channel
  they already use with that person.
- **Automatic clean-up of started but abandoned accounts.** The Desk lists them
  and offers deletion. Deleting Keycloak users unattended is not worth the
  saving.

## Verification

Deployed to smallworlds.network on 2026-10-14. Observed there:

- `e2e-tests/tests/10-join.spec.ts` issued a real invitation and walked it in
  German. Keycloak accepted the link that `join` minted over the in-cluster
  Service with its narrowed service account, and rendered its next page in
  German (`kc_locale`). Reopening the invitation resumed the same account, and
  the account was deleted afterwards.
- The Desk answers `403` from outside the Private Network on every path, and the
  join page `200`.
- The realm offers German and English on its sign-in page.

The same day, the first real invitation completed. The Operator signed in at the
Desk as `operator`, invited a member, and the member registered a passkey and
recovery codes. Every Keycloak page appeared in German, and `join` recorded the
invitation as done when it saw the passkey. The member reached
`/done/de` through Keycloak's **Back to application** link: Keycloak does not
redirect by itself. It also shows a confirmation page before the first action.
Those extra pages, and a pop-up asking for the passkey's name, led to
`docs/adr/0059`: the Keycloak half now runs through a token handler and a
login theme of our own.
