---
status: superseded
---

# Guide Private Network client installation and enrollment

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

The Bootstrap Launcher will detect the official Tailscale client, offer to acquire a pinned and verified installer when absent, request operating-system elevation explicitly, and enroll the Launcher Host with a short-lived single-use Headscale credential. The Private Gateway receives a separate stable identity, and Console Owners can later create short-lived Enrollment Invitations for other Operator Devices. Manual instructions remain a fallback, but private setup is incomplete until connectivity is verified.
