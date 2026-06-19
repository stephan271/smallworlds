# Walkthrough: Status Panel for SmallWorlds Dashboard

## Summary

Added a dynamic, community-facing status panel to the existing SmallWorlds dashboard. The status panel shows per-app health indicators, active incident timelines, and planned maintenance windows — all driven by a `status.json` file that Hermes Agent (or an admin) can update via `kubectl patch` without Git commits.

## Files Changed

### New Files

#### [status-configmap.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/status-configmap.yaml)
A Kubernetes ConfigMap containing `status.json` — the single source of truth for system status. Initial state has all 4 services (Files, Photos, Git, Auth) as `operational` with no incidents or maintenance windows.

**How to update it:**
```bash
kubectl patch configmap status-data -n dashboard --type merge \
  -p '{"data":{"status.json":"{\"lastUpdated\":\"2026-06-10T22:00:00Z\",\"overall\":\"degraded\",\"services\":[...],\"incidents\":[...],\"maintenance\":[...]}"}}'
```

---

### Modified Files

#### [dashboard-configmap.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/dashboard-configmap.yaml)
The main change. The dashboard HTML, CSS, and a new JavaScript section were updated:

- **HTML**: Each app card now has a `data-service-id` attribute and a `<span class="status-dot">` for at-a-glance health. A fixed banner appears at the top only during active incidents or when maintenance is < 24h away. A "System Status" section below the app grid shows incident timelines and maintenance cards.
- **CSS**: New status-specific styles (dot colors with glow, pulsing animations for degraded/outage, glassmorphism incident/maintenance cards, timeline with dot markers, severity badges, responsive adjustments).
- **JavaScript**: A small inline IIFE that fetches `/api/status.json` on load and polls every 60 seconds. All dynamic HTML is XSS-safe via a `textContent`-based escape function. Gracefully degrades to "Status information temporarily unavailable" if the fetch fails.

#### [dashboard-deployment.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/dashboard-deployment.yaml)
Added a second volume mount (`status-volume`) at `/usr/share/nginx/html/api` pointing to the `status-data` ConfigMap. This makes `status.json` available at the URL `/api/status.json`.

#### [kustomization.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/kustomization.yaml)
Added `status-configmap.yaml` to the resources list.

## Validation

- All 6 YAML files in the dashboard tenant directory pass `yaml.safe_load()` validation.

## How Hermes Agent Will Use This

1. Hermes Agent runs scheduled health checks (pinging each service URL)
2. When status changes, it patches the `status-data` ConfigMap directly via `kubectl`
3. The dashboard picks up the change within 60 seconds (next poll)
4. If there's an active incident, the banner appears automatically for all users
5. The same `status.json` can also be consumed by a static fallback page hosted externally
