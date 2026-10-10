---
status: accepted
---

# DNS can be kept by hand, with certificates proven over HTTP

An installation that is reachable from the internet no longer needs a Hetzner
account. The operator may keep the DNS records themselves — one wildcard A
record, `*.<domain>`, at whatever DNS provider they already use — and
certificates are then obtained with Let's Encrypt's HTTP-01 challenge instead
of DNS-01. Managed DNS through Hetzner remains, and remains the default for
the terminal route; this is a second answer to the same question, not a
replacement.

The reason is who installs. Hetzner DNS did every DNS job the install has —
creating the zone, the A records, the challenge TXT records, the mail records,
the tailnet's `admin` record — which was natural while the server was also at
Hetzner. For someone renting a server elsewhere (ADR 0055) it meant opening a
second cloud account and moving their domain's nameservers purely to obtain
certificates. That is the largest remaining piece of the install that an
operator has to do by hand and understand, and none of it is necessary when
the machine can answer on port 80.

## How it works

- **One record replaces all of them.** Every application lives one label below
  the domain (or below the environment extension), so a single wildcard covers
  the set, including applications added later. The in-cluster job that
  maintains A records is not deployed.
- **HTTP-01 instead of DNS-01.** The issuer keeps its name, `letsencrypt-prod`,
  so no Ingress changes; only its solver differs. cert-manager answers each
  challenge through a short-lived Ingress of its own. Traefik redirects port 80
  to 443 for every host; Let's Encrypt follows that redirect without validating
  the certificate it meets there.
- **The record is checked before anything is touched.** The installer asks the
  server for its public address, resolves `identity.<domain>` itself, and stops
  with the exact record to create if they differ. A wrong record would
  otherwise surface as certificates that never arrive, and failed attempts
  count against Let's Encrypt's limits. `SW_SKIP_DNS_CHECK=1` skips the
  comparison.
- **No Hetzner token reaches the cluster**, even if an earlier install left one
  in the cache file.

## What it cannot do

- **A LAN-only cluster cannot use it.** HTTP-01 is Let's Encrypt fetching a
  file from the machine; a machine that is not reachable cannot answer. Both
  setup routes offer the choice only for an exposed machine, and DNS-01 through
  Hetzner stays the way a LAN cluster gets real certificates.
- **Nothing follows a changing address.** With managed DNS a home connection's
  address is tracked every five minutes. By hand, the operator changes the
  record. That is a non-issue for a rented server and a real cost at home.
- **Mail records are not created.** MX, SPF, DKIM and DMARC are written by
  Stalwart's init job through the Hetzner API; without a token it skips them.
  Both routes warn when Stalwart is selected.
- **The tailnet's `admin` record is manual too**, and has to be an explicit
  record: the wildcard would otherwise answer for it with the public address.
  `setup-vpn.sh` prints the record and waits.
- **No wildcard certificate.** HTTP-01 cannot issue one; each hostname gets its
  own certificate, as before.

## Not done

Support for other DNS providers' APIs (Cloudflare first) would keep full
automation without Hetzner. It was set aside: every provider is code to
maintain and test across five separate DNS clients, and the manual record
removes the account requirement with none. It is the next step if operators
ask for automation at a specific provider.

## Verification

The installer's refusal and its continuation are covered by
`admin-tools/test-noninteractive-install.sh`, and the wizard's side by its own
suite. The issuer manifest was rendered for all three cases and uses a field
supported by the pinned cert-manager. **No certificate has been issued this
way yet**: that Let's Encrypt's validation, and cert-manager's own reachability
check from inside the cluster, both pass through Traefik's redirect is expected
from their documented behaviour, not observed here.
