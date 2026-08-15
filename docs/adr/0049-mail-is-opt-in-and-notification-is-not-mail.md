---
status: accepted
---

# Mail is an opt-in capability, and system notification is not mail

Stalwart becomes an opt-in Community Application under the capacity-aware selection of
`docs/adr/0037`, and no cluster capability depends on mail unless the Operator selects one
that does. The earlier form of this decision asserted that outbound sending was
unconditional because Keycloak's invitation flow needs it; that is wrong. This repository
already ships an `action-token-link` SPI (`infrastructure/keycloak-spi/`) that mints the
same `ExecuteActionsActionToken` as `execute-actions-email` and returns the URL instead of
mailing it, `admin-tools/bulk-invite.py` now defaults to that path and writes the links to
a `0600` file for the Operator to hand over in person or over a channel they trust,
invitation mode sets `emailVerified` directly rather than mailing a challenge, and the
realm has no password form at all — the browser flow deletes `Username Password Form` and
requires a passkey — so there is no secret to reset and therefore no recovery channel to
provide. Sending is consequently required only when the Operator picks the
`self-registration` onboarding mode, wants member-facing application notifications, or
chooses to route alerts by mail; a cluster that does none of these needs no SMTP in either
direction, and that is expected to be the common case for a LAN deployment. What remains
unconditional is that the cluster must be able to reach its Operator, and that obligation
is discharged by HTTP push rather than by SMTP: Alertmanager and Hermes post to a
notification service — ntfy is the reference choice, being a single self-hostable binary
published to with an ordinary POST — because a push message reaches a phone in seconds,
needs no domain, no SPF, DKIM or DMARC records, no provider account and no deliverability
question, and because Hermes' entire mail surface is twenty-five lines of `smtplib` that
becomes a smaller `urllib` call. The notification endpoint must not run on the cluster it
watches, for the same reason an Operator's address must not: a service that dies with the
node cannot report the node. Alertmanager cannot report its own death either, so the
`Watchdog` alert that is currently discarded is instead routed to an external heartbeat
that alerts on silence; without it nothing detects total cluster loss, which is the
failure that matters most. Where mail *is* selected, the design stands as before: one
endpoint, `smtp-relay.mail.svc.cluster.local:25`, plaintext and unauthenticated for
cluster subnets, behind which sits either an `ExternalName` to Stalwart or a null-client
MTA holding the provider credential in a single Cluster Secret, so that no consumer
manifest branches on the choice and the queue that neither Keycloak nor Alertmanager
possesses exists in one place; transactional mail is sent from a subdomain rather than the
apex, extending the `ENV_EXT` scoping, so an externally relaying cluster and a later
self-hosted Stalwart can hold authentication records simultaneously instead of requiring a
cutover; and `ADMIN_EMAIL` is a destination outside the cluster, rejected when it names the
cluster's own mail domain, with the in-application admin identity that Nextcloud and Immich
consume split into a separate value. Accepted in exchange: a member who loses their only
passkey must reach the Operator rather than self-serving, which makes the Operator an
availability dependency for account recovery and argues for enrolling two passkeys per
member; onboarding links are bearer credentials handled by humans over channels the
cluster cannot audit, bounded only by the admin action-token lifespan; and when mail is
selected, mailbox-hosting providers expose no bounce signal, so a rejected message stays
invisible.
