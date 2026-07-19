# Bootstrap a Local internet-exposed cluster

Status: ready-for-agent

## What to build

Extend the proven Local bootstrap journey to Local internet-exposed mode. The Operator supplies the public-domain inputs, receives explicit router-forwarding instructions, acknowledges manual router work, and completes public certificate/member exposure plus public Headscale coordination while all operator interfaces remain private behind the Private Gateway.

Covers PRD user stories 51, 64–65, 69–75, and 78–80.

## Acceptance criteria

- [ ] The Setup Journey collects and validates public-domain, DNS, and DNS-01 provider inputs without exposing token values.
- [ ] Required router forwarding rules and their purpose are localized and acknowledged explicitly; the launcher performs no UPnP, NAT-PMP, vendor API change, or dedicated forwarding verification.
- [ ] DDNS/public-IP behavior follows the established Local bootstrap contract and delayed DNS/certificate state remains a resumable waiting task.
- [ ] Headscale coordination is publicly reachable as required, while Private Network DNS still routes operator hostnames only to the stable Private Gateway.
- [ ] Operator Console, Grafana, and Argo CD remain absent from public ingress even when member-facing applications are public.
- [ ] Private access is verified from the enrolled Launcher Host before temporary node administration paths are restricted.
- [ ] Mode-specific mail and Jitsi limitations or warnings are preserved and available in English and German.
- [ ] Stable-release evidence records a real router/public-IP test covering certificates, private operator access, and expected public member application access.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
