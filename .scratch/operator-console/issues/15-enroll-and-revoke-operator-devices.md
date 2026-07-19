# Enroll and revoke Operator Devices

Status: ready-for-agent

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
