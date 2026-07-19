---
status: accepted
---

# Distribute operator hostnames through Private Network DNS

In Local LAN-only mode, Headscale's Private Network DNS configuration will make Operator Console, Grafana, and Argo CD hostnames resolve to the Private Gateway on enrolled Operator Devices without permanent hosts-file edits. The launcher may use temporary IP-based addressing during first enrollment. Name resolution for member-facing applications on non-enrolled LAN devices remains a separate router-DNS or hosts-file guidance concern; an implementation spike may choose Headscale extra records or a small authoritative DNS implementation without changing this contract.
