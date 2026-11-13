---
status: accepted
---

# Automate Hetzner project preparation

> **Still holds; the console parts do not.** Hetzner project preparation is
> automated by `smallworlds-init.sh` (Primary IP, DNS zone, SSH key, placement;
> `CLAUDE.md`, "Node placement and DNS"). The launcher is gone
> (`docs/adr/0051`).

After an Operator supplies a read/write Hetzner project token, the Bootstrap Launcher will validate access and automate discovery, explicit adoption, or creation of the Primary IP, DNS zone, SSH public key, firewall, server, persistent volume, DNS records, and reverse DNS. It will surface naming and location conflicts and show the planned recurring resources before apply. Account/project creation, token issuance, domain registration, and nameserver delegation remain guided external actions because they are outside the project API, but delegation will be verified before provisioning public modes.
