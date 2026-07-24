# Enroll and revoke Operator Devices

Status: in-progress

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
- [ ] Web UI device-access view (EN/DE).

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
