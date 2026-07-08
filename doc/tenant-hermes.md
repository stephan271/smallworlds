# Hermes Tenant (`infrastructure/kubernetes/tenants/hermes/*.yaml`)

Hermes is an automated, AI-driven operations agent (the "auto-remediator") deployed in the cluster. It watches for alerts, executes runbooks, and can propose or automatically apply infrastructure fixes.

## Key Infrastructure Integrations

### 1. Agent Configuration (`hermes-config.yaml`)
- **LLM Configuration**: Configures the agent to use the `hermes-2-pro-llama-3-8b` model.
- **Policies**: Sets policies such as `minor_updates_require_approval`, dictating when the agent can act autonomously versus when it must page a human.

### 2. Notification Channels (`notification-channels.yaml`)
Hermes communicates its actions and alerts through defined channels:
- **SMTP (Stalwart)**: Configured to send emails directly via the internal Stalwart mail relay (`stalwart-smtp.stalwart.svc.cluster.local:2525`) without needing external API keys. It uses templates (e.g., `approval-required`) to ask administrators to merge PRs.
- **Status Page (Dashboard)**: It automatically updates the cluster's public status page by directly modifying the `status-data` ConfigMap in the `dashboard` namespace when incidents occur or are resolved.

### 3. Runbooks (`runbooks/*.yaml`)
These YAML files define the operational playbooks Hermes can execute. They heavily interact with other cluster components:
- **`secret-rotation.yaml`**: Demonstrates deep cross-tenant integration. When rotating the shared Keycloak OAuth secret, Hermes uses `kubectl` to update the secret in the `keycloak` namespace, and then automatically restarts dependent deployments across the cluster (`nextcloud`, `immich-server`, `dashboard`) so they pick up the new keys.
- **Human-in-the-loop**: For high-risk operations (like `rotate-garage` which rotates S3 storage keys), the runbook is configured to exit with an error. This forces Hermes to pause execution and instead open a Pull Request against the Git repository for manual human approval.

## How Hermes was built up — phase history (from git history)

Hermes was assembled incrementally, each phase adding a layer. This ordering explains why the files exist:
- **Phase 1 — Observability** (`dfa21bf`): `healthz-ingress.yaml` and the initial wiring. Alerting/metrics had to exist before any automated remediation could be triggered.
- **Phase 4 — Hybrid AI remediation** (`db1ae5b`): the core agent — `hermes-config.yaml` (LLM + policies), `hermes-deployment.yaml`, `hermes-rbac.yaml`, and `hermes-sa-secret.yaml`. "Hybrid" = the agent proposes fixes but is gated by approval policies rather than acting freely.
- **Status updates** (`d314855`): `notification-channels.yaml` and the dashboard `status-data` patching integration.
- **Phase 6 — Security hardening** (`fe1c5d1`): added `runbooks/secret-rotation.yaml`.
- **Phase 7 — Scalability management** (`1caa0e0`): added the resource/scaling runbooks.

### Runbook catalogue (`runbooks/*.yaml`)
Each runbook binds Alertmanager triggers to a graded response; risky actions stop and open a PR rather than self-applying:
- **`secret-rotation.yaml`**: rotates CNPG passwords (via a `kubectl-patch` annotation that makes the CNPG operator rotate), the shared Keycloak OAuth secret (then restarts dependents — see §3), and Garage S3 keys. The Garage step deliberately `exit 1`s to force a manual PR.
- **`resource-pressure.yaml`**: reacts to `KubePodCrashLooping (OOM)`, `NodeCPUHigh`, `NodeMemoryHigh`; escalates to a vertical-scaling recommendation when a node is saturated.
- **`resource-rightsizing.yaml`**: analyzes 7-day P95 usage and recommends requests/limits adjustments.
- **`storage-expansion.yaml`**: triggered by `KubePersistentVolumeFillingUp`; predicts and proposes PV expansion.
- **`vertical-scaling.yaml`**: on `SustainedNodeCPUHigh/MemoryHigh`, proposes Hetzner server upgrades/downgrades — gated behind human approval and a maintenance window.
