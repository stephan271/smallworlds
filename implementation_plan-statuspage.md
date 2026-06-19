# Status Panel for SmallWorlds Dashboard

Add a dynamic, community-facing status panel to the existing SmallWorlds dashboard at `smallworlds.network`. The panel shows per-app health, active incidents, and planned maintenance windows. Status data is served from a separate ConfigMap (`status.json`) that Hermes Agent (or an admin) can update via `kubectl patch` without Git commits.

## Proposed Changes

### Data Model

The `status.json` file drives the entire status panel. It is the single source of truth that both the dashboard and (eventually) a static fallback page will consume.

```json
{
  "lastUpdated": "2026-06-10T20:00:00Z",
  "overall": "operational",
  "services": [
    {
      "id": "files",
      "name": "Files",
      "subtitle": "Nextcloud",
      "status": "operational"
    },
    {
      "id": "photos",
      "name": "Photos",
      "subtitle": "Immich",
      "status": "operational"
    },
    {
      "id": "git",
      "name": "Git",
      "subtitle": "Forgejo",
      "status": "operational"
    },
    {
      "id": "auth",
      "name": "Auth",
      "subtitle": "Keycloak",
      "status": "operational"
    }
  ],
  "incidents": [
    {
      "id": "inc-20260610-001",
      "title": "Photo uploads intermittently failing",
      "status": "monitoring",
      "severity": "minor",
      "affectedServices": ["photos"],
      "createdAt": "2026-06-10T14:30:00Z",
      "updates": [
        {
          "timestamp": "2026-06-10T16:00:00Z",
          "status": "monitoring",
          "message": "A fix has been deployed. We are monitoring for recurrence."
        },
        {
          "timestamp": "2026-06-10T14:45:00Z",
          "status": "identified",
          "message": "Root cause identified as a full temporary upload volume."
        },
        {
          "timestamp": "2026-06-10T14:30:00Z",
          "status": "investigating",
          "message": "We are investigating reports of failed photo uploads."
        }
      ]
    }
  ],
  "maintenance": [
    {
      "id": "maint-20260612-001",
      "title": "PostgreSQL upgrade to v16",
      "scheduledStart": "2026-06-12T02:00:00Z",
      "scheduledEnd": "2026-06-12T04:00:00Z",
      "affectedServices": ["files", "photos"],
      "description": "Scheduled database upgrade. Files and Photos will be briefly unavailable."
    }
  ]
}
```

**Status values:**
- Services: `operational` | `degraded` | `outage` | `maintenance`
- Overall: `operational` | `degraded` | `major_outage` | `maintenance`
- Incidents: `investigating` | `identified` | `monitoring` | `resolved`
- Severity: `minor` | `major` | `critical`

---

### Kubernetes Resources

#### [NEW] [status-configmap.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/status-configmap.yaml)

A new ConfigMap containing `status.json`. This is the file Hermes Agent updates via `kubectl patch configmap status-data -n dashboard --type merge -p '{"data":{"status.json":"..."}}'`. Separated from the dashboard content ConfigMap so it can be updated independently without redeploying the dashboard.

The initial state has all services as `operational` with no incidents or maintenance windows.

---

#### [MODIFY] [dashboard-deployment.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/dashboard-deployment.yaml)

Mount the new `status-data` ConfigMap as a second volume at `/usr/share/nginx/html/api/`. This makes `status.json` available at the URL path `/api/status.json`, keeping it cleanly separated from the dashboard's static HTML/CSS.

```diff
       volumeMounts:
         - name: html-volume
           mountPath: /usr/share/nginx/html
+        - name: status-volume
+          mountPath: /usr/share/nginx/html/api
       volumes:
         - name: html-volume
           configMap:
             name: dashboard-content
+        - name: status-volume
+          configMap:
+            name: status-data
```

---

#### [MODIFY] [kustomization.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/kustomization.yaml)

Add `status-configmap.yaml` to the resources list.

---

### Dashboard UI Changes

#### [MODIFY] [dashboard-configmap.yaml](file:///home/egli/development/smallworlds/infrastructure/kubernetes/tenants/dashboard/dashboard-configmap.yaml)

Three changes to the dashboard content:

**1. HTML — Add status indicators and sections**

- **Status dot on each app card**: A small colored indicator (green/yellow/red) on each app card showing its health at a glance. Visible immediately without scrolling.
- **Status banner**: A dismissable banner that appears at the top *only* when there's an active incident or upcoming maintenance within 24 hours. Hidden when everything is operational (no clutter in the happy path).
- **Status details section**: Below the app grid, a section with:
  - Active incidents: title, severity badge, affected services, chronological update timeline
  - Planned maintenance: upcoming windows with schedule, affected services, and description
  - "All systems operational" message when there's nothing to report
- **Last updated timestamp**: Shows when the status data was last refreshed, so users can gauge freshness.

**2. CSS — Status styles matching the existing design**

- Status dot colors using existing CSS variable palette (green for operational, amber for degraded, red for outage, blue for maintenance)
- Status banner with subtle glassmorphism matching the card style
- Incident timeline with a vertical line and timestamp markers
- Severity badges (minor/major/critical) with appropriate colors
- Smooth reveal animations consistent with the existing `fadeInUp` / `fadeInDown` patterns
- Responsive adjustments for mobile

**3. JavaScript — Fetch and render logic**

A small inline `<script>` at the bottom of the HTML that:
1. Fetches `/api/status.json` on page load
2. Polls every 60 seconds for updates
3. Updates the status dots on each app card
4. Renders the banner, incidents, and maintenance sections
5. Gracefully degrades if the fetch fails (shows "Status unavailable" rather than breaking the page)

The JS is intentionally minimal — no frameworks, no build step, no external dependencies. It maps service IDs from `status.json` to the existing app cards using `data-service-id` attributes.

## Verification Plan

### Manual Verification
1. Apply the manifests to the cluster and verify the dashboard loads correctly at `smallworlds.network`
2. Verify that when all services are `operational`, the status section shows a clean "All systems operational" message and no banner
3. Patch the `status-data` ConfigMap with a test incident and verify the banner appears and the incident timeline renders
4. Patch with a test maintenance window and verify it shows in the upcoming maintenance section
5. Verify responsive layout on mobile viewport
6. Verify graceful degradation when `/api/status.json` is unreachable (e.g., temporarily delete the ConfigMap)
