# Member Invitations (`join.<domain>`) — Design

**Status:** accepted and implemented (phases 1–4); first deployment: smallworlds.network
**Date:** 2026-10-14
**Decision record:** `docs/adr/0058-members-are-invited-through-a-join-page.md`
**Replaces, for everyday use:** step 13 of `doc/install-contabo-datalix.md`
(`admin-tools/bulk-invite.py` stays, as the terminal route)

## 1. Why

Inviting a member works today, but every step of it is harder than it has to be.

**For the Operator.** `bulk-invite.py` needs the Private Network, the `/etc/hosts`
line, a CSV file, `INVITE_SECRET` fished out of `.smallworlds-cache.env`, and Python
with `requests` installed. Its output is a file of bearer links that the Operator
then distributes by hand.

**For the link.**

- An action-token URL is about 600 characters. It cannot be read out or typed, and
  its QR code is dense enough that the script warns to print it at 4 cm or more.
- It expires after **12 hours** (`actionTokenGeneratedByAdminLifespan: 43200`). A
  member who opens it on the weekend meets an error page, and the Operator has to
  run the tool again.
- The Operator picks the username before the member has any say. If two people
  want the same name, the Operator has to sort it out.

**For the member.** The link opens Keycloak's required-action pages with no
introduction. The member sees "passkey" and "recovery codes" for the first time
in English, in the middle of a form. When it is done, the member lands in the
Keycloak account console, not on the Member Dashboard.

## 2. What this is

A small application, `join`, at `join.<domain>`, with two halves:

| Half | Who | Reachable from | Purpose |
| :--- | :--- | :--- | :--- |
| **Join page** (`/`) | the invited member | the internet | welcome, choose username, start passkey setup |
| **Invitation Desk** (`/desk`) | the Operator only | Private Network only | create, hand out, follow and revoke Member Invitations |

**Its one architectural idea:** the **Member Invitation** is separate from the
Keycloak action token. The Desk hands out a short code of its own, valid for 14
days and usable once. The action token is minted only at the moment the member
actually starts. Keycloak's 12-hour lifespan therefore never meets a member, and
the code is short enough to print, read aloud or type.

### It is not

| Not | Because |
| :--- | :--- |
| A user-management console | It creates members and gives them a new passkey. Disabling, deleting and renaming members stay in the Keycloak admin console. |
| Self-registration | Only the Operator invites (ADR 0058). Members cannot invite each other. The `self-registration` onboarding mode stays separate and is not affected. |
| A mailer | Mail is opt-in (ADR 0049). The Desk gives the Operator a link, a QR code and a printable card, and the Operator chooses the channel. |
| A store of member data | Profile data goes straight to Keycloak. `join` keeps only the invitation records (§6). |

## 3. The member's path

1. **Opening the invitation.** The member opens `https://join.<domain>/#K7QFM-2PDX9`
   from a message, scans the QR code, or types the code into the field on
   `join.<domain>`.
2. **Welcome.** The page is in the language the Operator chose for the invitation,
   with a visible DE/EN switch. It explains in three short points what happens:
   - You choose your name.
   - Your device saves a passkey, so there is no password to remember.
   - You get recovery codes. Have paper ready.

   A device check runs in the background
   (`PublicKeyCredential.isUserVerifyingPlatformAuthenticatorAvailable()`). If the
   device has no built-in passkey, the page says that a phone (by QR code) or a
   security key will also work. It is a hint, never a block, because hybrid and
   roaming authenticators are invisible to that check.
3. **About you.** The member fills in:
   - username, checked live: free, taken or not allowed (§7)
   - first name, last name and email address. The realm's user profile requires
     all three for the `user` role, and Keycloak would otherwise ask again at first
     sign-in. As with `bulk-invite.py`, the email is contact metadata, nothing
     verifies it, and nothing is sent to it.

   **Continue** creates the account and redirects at once.
4. **Keycloak.** Keycloak runs two required actions, `webauthn-register-passwordless`
   and `CONFIGURE_RECOVERY_AUTHN_CODES`. Its pages are in the same language,
   because `join` set the member's `locale` attribute (§8). `UPDATE_PROFILE` is no
   longer needed: step 3 already collected everything it would ask for.
5. **Done.** Keycloak returns to `https://join.<domain>/done/<lang>`. This page
   congratulates the member, reminds them to keep the recovery codes somewhere
   safe, and has one button: **To your dashboard**.

**If the member stops halfway.** Suppose they close the tab after step 3, or the
12-hour token runs out. Opening the same invitation again within its 14 days
skips steps 2 and 3 and mints a new token for the **same** account. An invitation
counts as used only once Keycloak holds a passkey for that account (§5).

## 4. The Operator's path

The Desk lives at `https://join.<domain>/desk`. It is reachable only from the
Private Network and requires sign-in as the master-realm `operator` account
(password + TOTP), the same identity that already administers Keycloak (§9).

- **New invitation.** The Operator fills in:
  - a name, for the Operator's own list only (it is not the username)
  - the language (DE/EN)
  - how long it is valid (default 14 days, at most 30)
  - an optional private note

  The Desk then shows the code, the link and a QR code, **once**. Only the
  code's hash is stored, so the Desk cannot show it again later; "new code"
  (below) replaces it instead. Ways to hand it out:
  - **Copy message.** A ready-to-paste text in the invitation's language
    (whatever language the Desk itself is in), with the link and a reminder
    not to forward it, for Signal, WhatsApp or SMS. **Share** opens the
    system's share sheet where the browser offers one.
  - **Show QR code**, for handing over in person.
  - **Print card.** A6, four to an A4 sheet with cut lines. Each card carries
    the name, QR code, the typed code, the address of the join page, short
    instructions in both languages and the expiry date.
- **Several at once.** Paste a list of names (one per line, at most 100) to get
  one invitation each, a message to copy per name, and a sheet of cards. This
  replaces `--qr` and the CSV.
- **List.** One row per invitation, newest first, with these states:

  | State | Meaning | Actions |
  | :--- | :--- | :--- |
  | open | not opened yet | new code, withdraw |
  | started | account created, no passkey yet | new code (same account), withdraw (offers to delete the account) |
  | done | passkey registered | — |
  | expired | validity passed | new code; withdraw if it started an account (offers to delete it) |
  | revoked | withdrawn by the Operator | — |

  "New code" issues a fresh code and validity on the same record, and the old
  code stops working at once. An account is deleted only if this invitation
  created it and it holds no passkey at all; if a passkey has appeared, the
  Desk refuses and records the invitation as done.

- **New passkey for a member.** For a member who has lost their device *and* their
  recovery codes:
  1. The Operator picks the member by username search and gets a **recovery
     invitation**.
  2. "Remove the existing passkeys now" is checked by default, because a lost
     device may be a stolen one. It also ends every session the account has,
     so the lost device is signed out as well.
  3. Any earlier live recovery invitation for the same member is withdrawn, so
     only one code can reach that account at a time.
  4. The member's page skips the form, greets them by username, and runs the same
     two Keycloak actions. This also produces new recovery codes. It counts as
     done only once a passkey registered *after* the invitation exists.

The Operator's own member account is invited in the same way. It is the first
invitation on every new cluster.

## 5. Invitation lifecycle

```
            Desk: create
                 │
                 ▼
   ┌────────── open ──────────┐
   │             │ member submits form
   │             ▼            │
   │         started ─────────┤──── Desk: revoke ──► revoked
   │             │ Keycloak reports a passkey
   │             ▼            │
   │           done           │
   │                          │
   └──── validity passes ─────┴──► expired ── Desk: renew ──► open
```

- **`started → done` is observed, not reported.** `join` asks Keycloak whether
  the account has a `webauthn-passwordless` credential. It asks when the
  invitation is opened again, when the Desk lists invitations, and when `/done`
  is reached. A member who never returns to `/done` is still recorded correctly.
- **A started invitation that expires** leaves an account without a passkey. The
  Desk lists it and offers to delete it. Nothing is deleted automatically: a
  passkey-less account can sign in to nothing, and an automatic delete of
  Keycloak users is not something to get wrong.
- **Renewing** issues a new code and invalidates the old one. A renewed `started`
  invitation keeps its account.

## 6. Architecture

```
infrastructure/kubernetes/tenants/join/
  kustomization.yaml          configMapGenerator for src/ and static/ (flat), keycloak-client-job base
  join-config.yaml            hosts, Keycloak Service, DB path
  join-deployment.yaml        python:3.14-slim, python -u /app/main.py, read-only root, uid 10001
  join-service.yaml           8080 `http` (public) and 8081 `desk`
  join-ingress.yaml           public Ingress → http, edge-protection middlewares
  join-desk-ingress.yaml      private Ingress /desk → desk, ipAllowList, priority 1000
  join-pvc.yaml               64 Mi local-path, holds invitations.db
  join-keycloak-setup-job.yaml  service account + master-realm desk client (§9)
  join-networkpolicy.yaml     phase 4
  src/
    main.py                   the two http.server listeners
    web.py                    what both share: headers, responses, static files, JSON
    config.py                 settings from the environment
    codes.py                  Crockford base32 codes, normalization, hashing
    usernames.py              rules and reserved names (§7)
    store.py                  sqlite3: invitations, state transitions
    keycloak.py               admin REST + action-token-link SPI, urllib only
    member.py                 the public API: lookup, username check, redeem
    manage.py                 create/list/revoke from a shell, inside the pod
    desk.py                   the Desk: OIDC sign-in, sessions, its API
  static/
    index.html  done.html     the join page and the page Keycloak returns to
    i18n.js                   every string, de and en, and the language switch
    join.js  done.js  style.css
    desk.html  desk.css       the Desk, and the print view of its cards
    desk-text.js  desk.js     the Desk's strings (merged into i18n.js's) and logic
    qrcode.js                 vendored qrcode-generator 2.0.4, MIT, unmodified
  tests/                      not shipped: fake Keycloak, unittest, Playwright walk
infrastructure/kubernetes/apps/join.yaml   sync-wave 1
```

It follows the Hermes and Remediation pattern: **Python 3.14 standard library
only, no Dockerfile, source mounted from a ConfigMap.** The page is plain
HTML/CSS/JS with no framework and no bundler, like the Setup Wizard.

**The one exception is `qrcode.js`.** QR encoding is not in either standard
library, and writing an encoder is several hundred lines of Reed–Solomon code. A
single vendored file (Kazuhiko Arase's `qrcode-generator`, MIT), pinned and
checked into `static/`, is served from the app itself and never from a CDN, so
the page still works offline. Encoding happens in the Operator's browser. The
server never renders a code.

**Storage.** `invitations.db` is SQLite on a 64 Mi `local-path` claim. Each
invitation row holds:

- `id`, `kind` (`new` / `recovery`)
- `code_hash` (SHA-256; the code itself is never stored)
- `label`, `note`, `locale`
- `created_at`, `expires_at`, `revoked_at`, `completed_at`
- `keycloak_user_id`, `username`

Names, email addresses and credentials live only in Keycloak. The claim is
**deliberately not backed up**. Losing it loses the open invitations and the
Desk's history, never a member: every account is in Keycloak's CNPG database,
which is backed up.

**Keycloak access.** `join` talks to Keycloak over the in-cluster Service, not
through Traefik, exactly as the init jobs do. The action-token links still carry
the public issuer, because `KC_HOSTNAME` is a full URL and Keycloak 26 then fixes
every frontend URL to it.

This was observed on 2026-10-14 on smallworlds.network. The discovery document,
fetched through a port-forward to `keycloak-keycloakx-http` (so with
`127.0.0.1` as the request's host), named
`https://identity.smallworlds.network/realms/smallworlds` as issuer. The SPI
builds its link and the token's `iss` from the same frontend URI.

`join` still refuses any link that does not start with
`https://identity.<domain>/realms/smallworlds/`. A link with the wrong issuer
would fail only at the moment the member clicks it, and a change to the hostname
settings must not turn that into a silent failure.

**Calls made, all as the `join` service account:**

| Purpose | Endpoint |
| :--- | :--- |
| username free? | `GET /admin/realms/smallworlds/users?username=…&exact=true` |
| create account | `POST /admin/realms/smallworlds/users` (with `attributes.locale`) |
| passkey present? | `GET /admin/realms/smallworlds/users/{id}/credentials` |
| remove passkeys (recovery) | `DELETE /admin/realms/smallworlds/users/{id}/credentials/{cid}` |
| delete started account | `DELETE /admin/realms/smallworlds/users/{id}` |
| mint the link | `POST /realms/smallworlds/action-token-link/generate-link` |

The `generate-link` request carries `clientId: join` and
`redirectUri: https://join.<domain>/done/<lang>`. The language is a path
segment rather than a query, so matching it against the registered
`https://join.<domain>/*` never depends on how Keycloak treats query strings.
The `join` client registers that
redirect, so Keycloak accepts it when the member finishes. Today's links send
the member to the account console, because `bulk-invite.py` names the `account`
client.

## 7. Usernames

The member chooses their username. The rules are the narrowest set that every
Community Application accepts, so a name that `join` allows never fails later
in Forgejo, Nextcloud or Stalwart:

- 3 to 32 characters
- `^[a-z0-9]+([._-][a-z0-9]+)*$`: lowercase letters and digits, with single `.`,
  `_` or `-` between them. No `@`, because Forgejo rejects it. No leading,
  trailing or doubled separators, because Forgejo rejects those as well.
- Input is lowercased and trimmed before it is checked, and the form says so as
  the member types.
- **Reserved:** `admin`, `administrator`, `operator`, `root`, `system`, `postmaster`,
  `hostmaster`, `webmaster`, `abuse`, `noreply`, `no-reply`, `mailer-daemon`,
  `security`, `support`, `info`. These are Stalwart's role addresses, the
  master-realm accounts' names, and names someone could use to impersonate the
  Operator.

**The live check needs a valid code.** The check endpoint is
`POST /api/username` with `{code, username}`, rate-limited per invitation. A
stranger without a code therefore cannot probe which members exist.

The Desk can pre-fill a suggestion, but the member decides.

## 8. Two languages

- **Choosing the language.** The invitation carries the Operator's choice
  (`locale`), and the page starts in it. A DE/EN switch is always visible, and
  the member's choice there wins. An opening without a code (the bare
  `join.<domain>` code field) follows `Accept-Language` and falls back to English.
- **Where the text lives.** Every string is in `static/i18n.js`, in both
  languages. The server answers with message keys only, never prose, so
  switching language re-renders whatever is on screen, errors included.
- **Keycloak's pages follow.** When `join` creates the account, it writes the
  member's final choice into the user attribute `locale`. That requires the realm
  to allow it: `internationalizationEnabled: true`,
  `supportedLocales: ["de", "en"]`, `defaultLocale: "en"`. These are set in
  `smallworlds-realm.json` and by `realm-config-job`, so existing realms get them
  too.
  - *Side effect:* the sign-in page gains a language selector for everyone.
  - **Both channels are used.** Keycloak's locale selector takes `kc_locale` from
    the query string first, then the known user's `locale` attribute, then the
    cookie, then `Accept-Language`. `join` therefore appends `kc_locale=<choice>`
    to the action-token link as well as setting the attribute:
    - The parameter makes the passkey pages right on the first visit.
    - The attribute keeps every later sign-in and the account console in the
      same language.
  - That the required actions honour either one inside an action-token flow is
    expected, not yet observed. It is the first check of phase 2.
- **Desk and card.** The Desk itself is bilingual too. The printed card carries
  both languages at once, because the Operator may not know which one the member
  reads.

## 9. Security

The Desk can create accounts and give any member a new passkey. That makes it
equivalent to taking over any member account, so it gets the same protection as
the Keycloak admin console, plus one layer more.

**The Desk.**

1. **Private Network only.** A second Ingress carries the same `ipAllowList`
   middleware as `keycloak-admin-ingress.yaml` and Argo CD (ADR 0038).
2. **Separate port.** This is the extra layer. The desk is served only on port
   8081, and the public Ingress points at 8080, where `/desk` does not exist.
   Traefik drops a router whose middleware is missing (`doc/hardening.md` §2). If
   the private Ingress ever broke, the public route would otherwise fall through
   to `/desk`; here it finds a 404.
3. **Operator sign-in.** OIDC authorization code flow with PKCE against the master
   realm, client `join-desk` (public). The `operator` account brings password and
   TOTP. `join` accepts a session only when the access token carries the master
   realm role `admin`.
   - The allow-list alone is not enough: it also admits the node's LAN and every
     cluster address (`doc/hardening.md` §1).
   - The tokens come directly from the token endpoint over the in-cluster
     Service, so `join` does not check their signature. OIDC Core allows that
     for tokens received over TLS from the token endpoint. Here the channel is
     the Service in plain HTTP, which is the same trust every init job already
     places in it, narrowed by the NetworkPolicy below.
4. **Session.** `HttpOnly; Secure; SameSite=Strict` cookie on `/desk`, 8 hours,
   held in memory, so a restart signs the Operator out. State-changing requests
   are JSON POSTs that need the header `X-Join-Desk: 1`, as CSRF protection.
   - The callback answers with a page that moves on to `/desk/` by itself, not
     with a redirect. A redirect chain that began at Keycloak would not carry a
     `Strict` cookie on its next request in every browser.
   - The login state and PKCE verifier are kept server-side for ten minutes;
     the browser holds only the state, in a `Lax` cookie scoped to
     `/desk/callback`.
   - Sign-out ends the session here and at Keycloak, which returns to
     `/desk/signed-out` (registered on `join-desk`).

**The join page.**

- **The code is in the fragment, not the path.** It is `join.<domain>/#K7QFM-2PDX9`,
  not `/K7QFM-2PDX9`. A browser never sends the fragment, so the code never appears
  in Traefik's access log, in Loki, or in a `Referer`. The page reads it and POSTs it.
- **The code.** 10 characters of Crockford base32 (no `I L O U`, case-insensitive,
  shown as two groups of five), which is 50 bits. Only its hash is stored. The page
  also sets `Referrer-Policy: no-referrer` and a strict CSP with no external origins.
- **Brute force.** Beyond `edge-ratelimit`, `join` allows 20 failed code lookups per
  source address per hour. 50 bits make guessing pointless anyway; the limit keeps
  the log quiet.
- **Account IDs never come from the browser.** The public half acts only on the
  account recorded in the invitation, which is stored server-side. Given a valid
  code, it can create one account (`new`) or act on exactly one existing account
  (`recovery`), and nothing else.
- **The remaining exposure is the same as today.** Until a passkey is registered,
  anyone holding the link can take over that one invitation. The Desk's message
  text says so, and the Operator should send links one to one, never to a group.

**The service account.** The `join` client in the `smallworlds` realm gets only
`realm-management` roles `manage-users`, `view-users` and `query-users`, which is
what the action-token SPI requires and nothing more. `bulk-invite` holds
`realm-admin`; reducing that is a separate follow-up.

**Network.** A NetworkPolicy allows ingress from Traefik only, and egress to
Keycloak's Service and DNS only.

## 10. How it is deployed

- **It is a Platform Service, not a Community Application.** It is always
  deployed, like the dashboard, because without it there is no ordinary way to
  get members.
  - It is not in `OPTIONAL_APPS` and not in the wizard's catalog.
  - `join` joins the always-deployed subdomains in all four places that list
    them: `local.core_subdomains` in `main.tf`, `generate_domain_patches.py`,
    `plan.py`, and `dns_subdomains()` in `smallworlds-init.sh`.
  - With manual DNS (ADR 0056), the wildcard already covers it.
- **Sync wave 1.** It depends on Keycloak (wave 0). Its setup Job polls until
  Keycloak answers, like the other init jobs.
- **Keycloak clients.**
  - `bases/keycloak-client-job` creates the `smallworlds` client `join` and its
    `keycloak-secret`, which `join` reads by the project-wide contract
    (`clientId`, `client-secret`). Redirect URIs: `https://join.<domain>/*`.
  - `join-keycloak-setup-job.yaml` runs after it. It turns on the client's service
    account, assigns the three roles, and creates the master-realm public client
    `join-desk` with redirect `https://join.<domain>/desk/callback`.
- **Operator devices.** The `/etc/hosts` line that the installer and
  `setup-vpn.sh` print for operator pages gains `join.<domain>`, so the Desk is
  reached through the tailnet address. Members resolve the public address as
  usual.
- **Dashboard.** `gethomepage.dev/enabled: "false"` on both Ingresses. Members
  never need to come back to `join`.

## 11. Testing

- **`admin-tools/test-join.sh`** runs both halves below. The tests live in
  `tenants/join/tests/`, beside the code like the Setup Wizard's, and are not
  shipped in the ConfigMap.
- **`tests/test_join.py`** (stdlib `unittest`). It runs against a fake
  Keycloak built on `http.server` (`tests/fake_keycloak.py`) and covers:
  - the state machine (§5)
  - the username rules and reserved names
  - code hashing and the fragment-only protocol
  - the rule that the public half never accepts an account ID
  - issuer checking of minted links
  - the 404 for `/desk` on the public port
- **`tests/test_desk.py`**, against the same fake, which also plays the master
  realm's authorization and token endpoints (PKCE checked). It covers:
  - sign-in: state cookie, single-use code, foreign issuer, and refusal of an
    account without the `admin` role
  - every API needing a session and the CSRF header; sign-out
  - that neither listener serves any route of the other
  - issuing, bulk, new code, withdrawing, and never deleting an account that
    holds a passkey
  - member search, recovery with passkey removal and session sign-out, and one
    live recovery code per member
  - that every file under `src/` and `static/` is listed in the ConfigMaps
- **Browser.** `tests/test-interface.mjs` walks the join page against
  `tests/serve.py` in both languages:
  - language switching and live username feedback
  - field-by-field refusals
  - arrival at Keycloak with `kc_locale`
  - the resumed, recovery and failed cases, and the done page
  - no horizontal scrolling at 320 px
  - no console errors, which is how a CSP violation would show

  `tests/test-desk.mjs` walks the Desk on `https://join.example.test`, with
  every request to that origin handed to the local listeners. That way
  `Secure` and `SameSite` cookies and the clipboard behave as in production. It
  covers:
  - sign-in through the fake Keycloak, and the session cookie's attributes
  - issuing one invitation: QR code, message, copy to the clipboard, and that
    the code opens the member's page
  - several at once, and their cards in the print area
  - withdrawing, and the language switch
  - recovery
  - 360 px width, and sign-out

  During development the printed cards were also rendered to PDF, and all four
  QR codes on a sheet decoded (OpenCV) to their links.
- **Live cluster.** `e2e-tests/tests/10-join.spec.ts` issues an invitation
  with `manage.py` through `kubectl exec`; the Desk's sign-in needs the
  Operator's TOTP, so the Desk is only checked for never answering `200`
  without a session. The spec then:
  - opens the invitation in German and submits the form
  - asserts that Keycloak accepted the link and renders in German
  - checks that reopening the invitation resumes the same account
  - deletes the account again (`manage.py revoke --delete-account`)
  - The full passkey roundtrip uses Chromium's virtual authenticator. It runs
    only on the lab cluster, because Chrome disables WebAuthn on a page with a
    certificate error, so staging cannot do it.
- **From outside**, after the first deployment: `https://join.<domain>/desk` must
  answer `403`, like `deploy.<domain>`.

## 12. Phases

1. **Keycloak groundwork — done:**
   - realm i18n (de/en) in `smallworlds-realm.json` and `realm-config-job`
   - `tenants/join/` with `keycloak-client-job` and `join-keycloak-setup-job.yaml`:
     the `join` client's service account with the three roles, and the
     `join-desk` master client
   - the issuer check (§6)

   `tenants/join/` is not referenced by any Application yet, so only the realm
   change deploys.

   Phase 4's domain patch must set both hosts that phase 1 hard-codes:
   - `REDIRECT_URIS` on `keycloak-client-init`
   - `JOIN_HOST` on `join-keycloak-setup`
2. **The join page — done:**
   - store, member flow, both languages, `/done/<lang>`
   - Deployment, Service, PVC and public Ingress
   - tests, and a check of the real image under the pod's restrictions
     (read-only root, uid 10001)

   `src/manage.py` (`create`, `list`, `revoke`), run inside the pod with
   `kubectl exec`, issues invitations from a shell. It stayed after phase 3 as
   the shell route, as `bulk-invite.py` does for the terminal.
3. **The Invitation Desk — done:**
   - sign-in, sessions and its API on port 8081 (`desk.py`); `web.py` shared by
     both listeners
   - create, bulk, list, new code, withdraw (with account deletion), recovery
     (with passkey removal and session sign-out)
   - message, share, QR code and printed cards
   - both languages
   - `join-desk-ingress.yaml` with the allow-list, and the `desk` Service port
   - `join-desk` registers `/desk/signed-out` for Keycloak's sign-out
4. **Wiring and documentation — done:**
   - `apps/join.yaml` (wave 1) in the master kustomization, the namespace
   - `join-networkpolicy.yaml`: in from Traefik only, out to DNS and Keycloak only
   - `join` as a core subdomain in all four maps, both CoreDNS host lists, the
     DDNS default and the hosts snippet
   - the `join` block in `generate_domain_patches.py`
   - `prepare-community-repo.sh` treats `join` like `dashboard`, and on re-run
     adds the routing an older overlay lacks
   - `doc/tenant-join.md`; step 13 of `install-contabo-datalix.md` now uses the
     Desk; README, `mail.md`, `hardening.md`, `tenant-keycloak.md`,
     `local-deployment.md`, `argocd-apps.md` and `CLAUDE.md`
   - `e2e-tests/tests/10-join.spec.ts`

   An existing cluster does not get the DNS record by itself on the `local`
   target: the DDNS job's names were fixed at install. There, `join` is added
   to `RECORD_NAMES` in `/var/lib/rancher/k3s/server/manifests/ddns.yaml` on
   the node. The `hetzner` target gets it from the next `terraform apply`.

## 13. Not decided here

- Whether the realm should stop requiring an email address. Many members have one,
  some will not want to give it, and invitations no longer need it. That is a
  realm user-profile question, not a `join` one.
- Reducing `bulk-invite`'s `realm-admin` to the same three roles.
- Inviting by members. ADR 0058 rules it out for now. If it returns, it needs a
  member-facing Desk on the public side and a quota or approval step. This design
  does not prepare for it.
