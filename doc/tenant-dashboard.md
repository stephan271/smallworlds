# Dashboard Tenant (`infrastructure/kubernetes/tenants/dashboard/*.yaml`)

The Dashboard tenant runs Homepage (gethomepage.dev), serving as the main entry point and status page for the cluster. Its configuration focuses on auto-discovery of cluster services.

## Key Infrastructure Integrations

### 1. Cluster Auto-Discovery (`dashboard-rbac.yaml`)
Unlike a static dashboard, this setup is completely dynamic.
- **Cluster Role**: The `homepage-role` grants read and watch access to `namespaces`, `pods`, `services`, and `ingresses` across the entire cluster.
- **Why?**: This allows the Homepage pod to scan the cluster for Ingress objects containing specific annotations (e.g., `gethomepage.dev/enabled: "true"`, `gethomepage.dev/group: "Applications"`). 
- When a new tenant (like Immich or Nextcloud) is deployed, its Ingress contains these annotations, causing it to automatically appear on the dashboard without modifying the dashboard's configuration.

### 2. No status data
A `status-data` ConfigMap used to live here (`d314855`), meant for Hermes to patch with incidents for a status panel. Hermes never had the rights to write it and Homepage never read it; it was removed with Hermes (`docs/adr/0063`).

## Notable changes per file (from git history)

### `dashboard-deployment.yaml` — fighting Homepage's writable-config assumptions
Homepage expects to write into its own config directory and hostname allow-list at runtime, which clashes with a hardened/immutable mount. Several fixes landed:
- **Mount config files individually** (`7a1c156`): instead of mounting the whole config directory (which made it read-only), each config file is mounted individually so Homepage can still write the rest of the directory at runtime.
- **Read-only logs & host validation** (`d0a5a85`): resolved errors from Homepage trying to write logs and rejecting the request host — the fix supplies the allowed host and a writable log path.
- **Custom pod-selector annotations** (`b0d4329`): configured to resolve Kubernetes API errors during service auto-discovery (Homepage querying pods it wasn't scoped to).

### `dashboard-config.yaml`
- **Empty bookmarks override** (`ba57c88`): Homepage ships default demo bookmarks; they are overridden with an empty list so the dashboard shows only real cluster services.

### `dashboard-rbac.yaml`, `dashboard-service.yaml`, `dashboard-ingress.yaml`
- **Dynamic dashboard introduction** (`86bcb16`, "Create dynamic dashboard using the homepage tool"): these files were introduced together when the cluster switched to the auto-discovering Homepage dashboard described in §1.

### Managed by the `dashboard` Application only (`a0113c7`)
The root `infrastructure/kubernetes/kustomization.yaml` used to include `tenants/dashboard` *directly* in addition to `apps/dashboard.yaml` — so the same manifests were applied twice, once by the root app and once by the dashboard Application, and the two owners fought over sync status. The direct inclusion was removed; like every other tenant, the dashboard now reaches the cluster only through its ArgoCD `Application`.
