# Enroll and revoke Operator Devices

Status: done — acceptance criteria 1–7 met against injected seams; the live
device directory, invitation issuer (Headscale pre-auth keys), and device
revoker are deferred to the live-cluster integration exactly like issue 11/14's
in-cluster console adapters (the console is not yet wired into
`cmd/smallworlds-admin`), tracked as outstanding integration evidence, not a code
dependency.

## Implementation progress

- [x] **operator-device domain** (`internal/operatordevice`) — the pure,
  table-tested core. `Invitation` is the secret-free, single-use, attributable
  Enrollment Invitation record (it stores only the SHA-256 fingerprint of the
  one-time join key, never the key or any reusable cluster/Headscale admin
  credential); its lifetime is clamped into `[MinInvitationTTL, MaxInvitationTTL]`
  so it is always short-lived, and `Redeem`/`Revoke`/`State` give expired, reused,
  and revoked invitations distinct, fail-closed errors (criteria 2, 7).
  `EnrollmentGuidance` derives the ordered enrollment path (verified Tailscale
  acquisition + elevation, Private Network join, MagicDNS, Cluster CA install only
  where the Deployment Mode requires it, gateway-reachability verification) —
  criteria 3, 4. `AssessRevocation` inspects the device inventory, counts
  alternative Owner access, labels lockout risk (last-owner-device dominant over
  self-revocation), and records the affected stable device identity (criterion 5).
  `gofmt`/`go vet`/`go test` clean.
- [x] **Console device-administration endpoints + seams**
  (`internal/console/deviceadmin.go`, `console.go`) — covers criteria 1, 5, 6. All
  device routes sit behind the server-side `Administer` permission, so only a
  Console Owner is admitted (Observers/Operators 403, anonymous 401) — criterion
  1. `POST /administration/invitations` mints a single-use join key through the
  injected `InvitationIssuer`, records the issuance in the Activity Record by
  fingerprint (never the key), and returns the one-time key plus the enrollment
  guidance. `GET /administration/access` lists the devices from the injected
  `DeviceDirectory` with an owner-access summary so alternative Owner access is
  visible before acting. `POST /administration/revocations/plan|{id}/approve|{id}/execute`
  is the bounded Runtime Action: plan assesses lockout and binds the affected
  stable identity into the plan digest, approve binds the Owner's consent, and
  execute re-inspects the inventory (409 on drift), removes exactly the selected
  device through the injected `DeviceRevoker`, and records whether loss of access
  was verified in a redacted Activity Record (criteria 5, 6). The three seams
  default to honest-refusal (503 directory/invitation/revocation_unavailable),
  deferred to the live-cluster integration like the console's other adapters.
  Owner-level (device) Activity Records are filtered out of the Operator-visible
  `/proposals` workspace. httptest-verified; `gofmt`/`go vet`/`go test ./...` pass.
- [x] **Web UI device-access view** (`web/src/lib/console.ts`,
  `web/src/lib/console-i18n.ts`, `web/src/routes/console/+page.svelte`) — an
  `Access` view shown only when the session holds the `administer` permission
  (server-side authz remains the enforcement; the UI merely hides controls). It
  lists the current devices with online/Owner-access/this-device cues and an
  owner-access summary, offers a single-input enrollment form that surfaces the
  one-time join key with a "shown once" warning plus the deployment-mode-aware
  guidance steps (elevation badges, Cluster CA notice), and drives the revoke
  journey plan→lockout-labeled-assessment→approve→execute with a redacted result
  and a recent-activity list. EN/DE parity is enforced by the
  `Record<ConsoleMessageKey,string>` German catalog; `step_*` and `lockout_*`
  keys localize the backend enums. `npm run check` (0 errors) and `npm run build`
  both pass.

## What to build

Give a Console Owner an accountable device-access journey. Owners create short-lived single-use Enrollment Invitations for additional Operator Devices, those devices join through the documented Private Network and trust path, and a lost device can be revoked through an inspected, lockout-aware Runtime Action rather than direct Headscale administration.

Covers PRD user stories 71–77, 81, and 84.

## Acceptance criteria

- [ ] Only a Console Owner can create an Enrollment Invitation or plan device revocation; authorization is enforced server-side.
- [ ] Invitations are short-lived, single-use, attributable, and display no reusable cluster or Headscale administrator credential.
- [ ] Enrollment guides verified Tailscale-client acquisition, explicit elevation, Private Network DNS, and Cluster CA trust where the Deployment Mode requires it.
- [ ] A newly enrolled device reaches operator hostnames through the Private Gateway and is absent from public routes.
- [ ] Revocation inspects current devices and alternative Owner access, labels lockout risk, requires approval, and records the affected stable device identity.
- [ ] Execution removes only the selected device, verifies loss of access, and produces a redacted Activity Record.
- [ ] Expired, reused, revoked, and malformed invitations fail safely and clearly.

## Blocked by

- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
