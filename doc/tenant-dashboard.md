# Dashboard Tenant (`infrastructure/kubernetes/tenants/dashboard/*.yaml`)

The Dashboard tenant runs Homepage (gethomepage.dev), serving as the main entry point and status page for the cluster. Its configuration focuses on auto-discovery of cluster services.

## Key Infrastructure Integrations

### 1. Cluster Auto-Discovery (`dashboard-rbac.yaml`)
Unlike a static dashboard, this setup is completely dynamic.
- **Cluster Role**: The `homepage-role` grants read and watch access to `namespaces`, `pods`, `services`, and `ingresses` across the entire cluster.
- **Why?**: This allows the Homepage pod to scan the cluster for Ingress objects containing specific annotations (e.g., `gethomepage.dev/enabled: "true"`, `gethomepage.dev/group: "Applications"`). 
- When a new tenant (like Immich or Nextcloud) is deployed, its Ingress contains these annotations, causing it to automatically appear on the dashboard without modifying the dashboard's configuration.

### 2. Status Integration (`status-data.yaml`)
- **Status ConfigMap**: This namespace houses the `status-data` ConfigMap.
- **Hermes Integration**: As defined in the Hermes tenant, the automated AI agent directly patches this ConfigMap to update the cluster's public status page when it detects or resolves incidents.
