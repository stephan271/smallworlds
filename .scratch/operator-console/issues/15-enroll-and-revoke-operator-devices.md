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
- [ ] Console device-administration endpoints + seams (criteria 1, 5, 6).
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
