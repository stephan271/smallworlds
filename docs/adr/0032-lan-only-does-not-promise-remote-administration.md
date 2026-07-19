---
status: accepted
---

# LAN-only does not promise remote administration

In Local LAN-only mode, Headscale coordination and the Private Gateway are reachable only from the LAN, so Operator Devices must be on that network to enroll and reliably administer the cluster. The Private Network still isolates operator interfaces, but remote access is a property of Local internet-exposed or Hetzner mode. The launcher will not silently expose a router port and change the security meaning of LAN-only.
