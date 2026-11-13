---
status: superseded
---

# Establish the first Console Owner through a one-time claim

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

After Keycloak is healthy, the Setup Journey will request and display a short-lived one-time enrollment link for the first Operator's email, without depending on working mail delivery. The Operator creates or claims their normal identity, registers a passkey, and becomes the first Console Owner; the bootstrap grant permanently disables itself after success. The Keycloak realm administrator remains a separate break-glass identity rather than the routine Operator Console account.
