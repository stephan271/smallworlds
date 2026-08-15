---
status: accepted
---

# Transactional mail is a Platform Service; the mail server is a Community Application

Stalwart becomes an opt-in Community Application alongside the other capacity-aware
selections of `docs/adr/0037`, while the ability to *send* transactional mail becomes a
Platform Service that is present in every cluster regardless of that choice. The two are
routinely conflated and must not be: Keycloak's invitation and e-mail-verification flows,
Alertmanager's only delivery route, and Hermes' incident reports all depend on outbound
mail, and none of them are optional, so a cluster that cannot send mail cannot onboard a
member, cannot report its own failure, and cannot let an Operator recover a password —
whereas member mailboxes, IMAP/JMAP and the Bulwark webmail are an end-user feature a
community may legitimately decline. The seam between the two is a stable in-cluster
submission endpoint, `smtp-relay.mail.svc.cluster.local:25`, unauthenticated and
unencrypted for cluster subnets exactly as Stalwart's internal relay is today; behind it
sits either an `ExternalName` Service resolving to Stalwart, or a null-client MTA that
authenticates to an external provider on 587 with credentials held in a single Cluster
Secret. The indirection is deliberate rather than incidental: Hermes speaks raw `smtplib`
with neither STARTTLS nor AUTH, Alertmanager and Keycloak would each need their own copy
of the provider credential, and none of the three retries a refused message, so
configuring providers per consumer would mean editing the components least able to absorb
it and silently losing mail whenever the provider throttles. Keeping every consumer
manifest byte-identical in both modes also keeps the delivery matrix single-branched for
`admin-tools/test-pr-locally.sh`. Transactional mail is sent from a dedicated subdomain,
never the zone apex, extending the environment scoping that `ENV_EXT` already imposes on
Stalwart's mail domain: apex reputation stays with member mail, an externally relaying
cluster and a later self-hosted Stalwart can hold authentication records simultaneously
rather than requiring a cutover, and relaxed DMARC alignment still passes under a single
apex policy. `ADMIN_EMAIL` is redefined as a destination outside the cluster and is
rejected when it names the cluster's own mail domain, because an alert about a dead node
delivered to a mailbox on that node is not a notification; the in-application admin
identity that Nextcloud and Immich derive from it is a separate value, since a login name
and a monitored destination have no reason to be the same string. Accepted in exchange:
mailbox-hosting providers expose no bounce or suppression signal, so a rejected password
reset remains invisible to the cluster, and the domain still needs a receiving path for
`postmaster@` and DMARC aggregate reports even when no mailboxes are served.
