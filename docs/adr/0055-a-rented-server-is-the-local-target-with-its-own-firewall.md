---
status: accepted
---

# A rented server is the local target with its own firewall

A VPS rented from any provider other than Hetzner — Contabo, OVH, anyone who
hands over a Linux machine with a public address and SSH — is installed through
the existing `local` target, not through a provider-specific one. The operator
orders the machine; from the first SSH connection onward the install is the
same script that installs a mini-PC on a LAN. The one thing a rented server
needs that a LAN machine does not is a firewall, and the bootstrap now sets one
up on the machine itself when asked.

There is deliberately no Contabo Terraform root. Such providers bill by the
month and provision in minutes to hours, so creating the machine through an API
buys little, and a provider-specific root would have to be written again for
the next provider. "Reachable over SSH" is the contract every one of them
meets.

## The host firewall

The Hetzner target has a cloud firewall in front of the node. A VPS elsewhere
has nothing, and k3s then offers the kubelet, every NodePort and anything a
hostNetwork pod binds to the whole internet. With `HOST_FIREWALL=true` the
bootstrap writes an nftables allow-list and a systemd unit that loads it at
boot, before k3s.

- **It filters in prerouting, before destination NAT**, on the public interface
  only. A port is judged by the number the outside dialled, before kube-proxy
  or the CNI rewrites it — which is what closes NodePorts. A filter on the
  input hook would never see them. Pod-to-pod traffic and anything the machine
  initiated are untouched.
- **The allow-list mirrors the Hetzner firewall**: SSH, 80, 443 and 6443, plus
  25/587/993 when Stalwart is selected and 10000/udp when Jitsi is.
- **SSH is found, not assumed.** The port is taken from `sshd -T`, from the
  session the installer came in on and from the listening sockets, and 22 is
  kept regardless. A wrong guess would lock the operator out of a machine they
  can only reach over SSH.
- **It is opt-in and asked only for an exposed machine.** Behind a router it
  adds nothing. A missing `nft` stops the install rather than continuing
  without a firewall.

## What stays manual on a rented server

Ordering the machine and adding an SSH key; delegating the domain to Hetzner
DNS, which certificates and records are managed through wherever the server
is; the reverse-DNS record for mail, which only the provider's panel can set;
and rebuilding the node, which is a reinstall in the provider's panel followed
by a re-run of the installer.

## The photo library directory

A rented server typically has one small fast disk. With Immich (not Gallery,
ADR 0054) the library is what outgrows it, so `LIBRARY_DIR` lets that one
directory live on another disk while everything else stays on the first. The
bootstrap links it into place and refuses to proceed if a library already
exists at the default location: it does not move photos on the operator's
behalf.

## Consequences

- The wizard offers three kinds of machine, but there are still two deployment
  targets; "a server you rent elsewhere" is the local target with public
  exposure, Let's Encrypt and the firewall preselected.
- 6443 and SSH are open to the internet, as they are by default on Hetzner.
  Closing them once the tailnet works means editing the allow-list by hand;
  there is no equivalent of `public_admin_ports = false` yet.
- IPv6 is filtered by the same rules, but the DNS job publishes only A records.

## Verification

The generated ruleset was loaded in a container and shown to admit a listed
port, drop an unlisted one, and leave outbound and loopback traffic alone. It
has not been run on a node with k3s, so the NodePort claim rests on where the
chain is hooked rather than on a test.
