---
status: accepted
---

# Keep router forwarding manual without a dedicated verifier

For Local internet-exposed mode, the Setup Journey will show the required router forwarding rules and their purpose, then accept Operator acknowledgement that they were configured. The launcher will not use UPnP, NAT-PMP, or vendor-specific router APIs, and the first release will not include an explicit external forwarding-verification task. Later Capability Assessments may still surface naturally observed access failures without making router probing a bootstrap requirement.
