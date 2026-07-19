---
status: accepted
---

# Guide Private Network client installation and enrollment

The Bootstrap Launcher will detect the official Tailscale client, offer to acquire a pinned and verified installer when absent, request operating-system elevation explicitly, and enroll the Launcher Host with a short-lived single-use Headscale credential. The Private Gateway receives a separate stable identity, and Console Owners can later create short-lived Enrollment Invitations for other Operator Devices. Manual instructions remain a fallback, but private setup is incomplete until connectivity is verified.
