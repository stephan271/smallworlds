# Plane Architecture & Configuration

This document describes how the Plane open-source project management application is integrated into the SmallWorlds GitOps cluster.

## Deployment Details

Plane is deployed using the official community Helm chart (`plane-ce`), vendored under `charts/plane-ce-1.6.0/` and inflated via Kustomize's `helmCharts` as a tenant application.

- **Source Code**: [https://github.com/makeplane/helm-charts](https://github.com/makeplane/helm-charts)
- **Helm Repository**: `https://helm.plane.so/`
- **Application URL**: `https://plan.<domain>`

## Infrastructure Integration

By default, the Plane Helm chart provisions its own Redis, MinIO, RabbitMQ, and PostgreSQL. To ensure consistency, ease of backups, and resource efficiency within SmallWorlds, PostgreSQL and Redis are disabled in favor of dedicated resources provisioned alongside the application; RabbitMQ is left on the chart's bundled StatefulSet since there is no shared broker to point it at.

1. **PostgreSQL Database**
   A dedicated PostgreSQL cluster (`cnpg-cluster.yaml`) is spun up using CloudNativePG. It automatically inherits the `garage` S3 credentials to ensure automated backups of the Plane database.
2. **Redis Cache**
   A dedicated Redis deployment (`redis.yaml`) provides the caching layer Plane requires.
3. **RabbitMQ (Celery broker)**
   `rabbitmq.local_setup: true` (the chart default) deploys the chart's own single-node RabbitMQ StatefulSet. This was initially disabled along with postgres/redis/minio, but unlike those, no replacement was ever wired up — `AMQP_URL` ended up empty and `plane-api`'s startup crashed publishing a Celery task (`register_instance` → `instance_traces.delay()` → `kombu.exceptions.OperationalError: Connection refused`).

### `DATABASE_URL` / `AMQP_URL` are injected directly on containers, not via the chart's secret

The chart's `plane-app-secrets` Secret templates `DATABASE_URL` and `AMQP_URL` using `{{ .Release.Namespace }}` to build in-cluster DNS names (e.g. `plane-rabbitmq.{{ .Release.Namespace }}.svc...`). Kustomize's `helmCharts` inflater runs `helm template` **without** passing `--namespace`, so `.Release.Namespace` resolves to `default` — the *right* namespace only gets applied afterwards by Kustomize's `namespace: plane` transformer, and only to structural `metadata.namespace` fields, not to values baked into template strings. The result is broken connection strings like `plane-rabbitmq.default.svc.cluster.local` pointing at a namespace the service doesn't live in.

Rather than patch the chart, `kustomization.yaml` overrides `DATABASE_URL` and `AMQP_URL` directly as container env vars (with the correct namespace hardcoded) on every workload that needs them: `plane-api-wl`, `plane-worker-wl`, `plane-beat-worker-wl`, and the `plane-api-migrate-1` Job (`DATABASE_URL` only — the migrator doesn't touch Celery). `DATABASE_URL` is built from `PGHOST`/`PGUSER`/`PGPASSWORD`/`PGDATABASE` using Kubernetes' `$(VAR)` env interpolation, which only works for vars listed explicitly in the same container's `env:` (not ones pulled in via `envFrom`).

## Authentication

SmallWorlds deploys the Community Edition (`plane-ce`). It has **no usable
Keycloak/OIDC SSO path**: Plane documents custom OIDC SSO as a Pro/Business
feature configured manually in God Mode (`/god-mode/authentication/oidc`).
Accordingly, Plane is deliberately not included in the Keycloak client-job base,
does not receive Keycloak credentials, and is excluded from Keycloak-scoped E2E
tests. Its normal availability check remains part of the smoke-test runner.

If the deployment moves to an edition that includes OIDC SSO, configure it in
God Mode using the provider's authorize, token, and user-info endpoints, then
reintroduce a dedicated Keycloak client and OIDC test.

## ArgoCD Sync Ordering (`plane-api-migrate-1`)

The chart stamps a fresh `timestamp: {{ now }}` pod-template annotation on every Deployment *and* the migrator Job on every render. A Job's pod template is immutable once created, so under ArgoCD's normal patch-in-place sync, every single sync attempt failed with `field is immutable` — permanently blocking the whole Application (nothing else in the sync could proceed either), which is why the app could sit broken indefinitely even after config fixes landed in git.

Fix, applied as a Kustomize patch on the Job:

```yaml
annotations:
  argocd.argoproj.io/hook: Sync
  argocd.argoproj.io/hook-delete-policy: BeforeHookCreation
  argocd.argoproj.io/sync-wave: "0"
```

- `hook: Sync` + `hook-delete-policy: BeforeHookCreation` makes ArgoCD delete and recreate the Job each sync instead of patching it in place.
- `hook: PreSync` was tried first and rejected: PreSync hooks run *before* the chart's own resources (including the `plane-srv-account` ServiceAccount the Job's pod needs), so the Job spun forever with `serviceaccount "plane-srv-account" not found`.
- The hook shares sync wave 0 with the API. The API waits for migrations before becoming Ready, so placing the migration hook in wave 1 would deadlock ArgoCD while it waits for API health.

## Admin app: nginx trailing-slash redirect leaks internal port (`admin-nginx-configmap.yaml`)

Clicking "Get started" in the web UI (which links to `/god-mode`, no trailing slash) would hang the browser indefinitely. `plane-admin`'s nginx sits behind Traefik, which terminates TLS on 443 and proxies plain HTTP to the pod's port 3000 — nginx itself has no awareness of that. Its built-in trailing-slash redirect (`try_files $uri $uri/ /god-mode/index.html;`, triggered because `$uri/` matches the app's own base path) defaults to building an **absolute** `Location` header from its own scheme/listen-port, producing `http://plan.<domain>:3000/god-mode/`. Port 3000 isn't exposed anywhere outside the cluster, so the browser just timed out.

Fix: `admin-nginx-configmap.yaml` ships a corrected `nginx.conf` (identical to the image's own, plus `absolute_redirect off;`), mounted over `/etc/nginx/nginx.conf` in `plane-admin-wl` via a `volumeMounts`/`volumes` patch. With that directive, the redirect is emitted as a relative `/god-mode/`, which the browser resolves against whatever origin (scheme/host) it actually used.

`plane-space` and `plane-live` aren't affected — they run a Node-based server (`react-router-serve`), not nginx, and already emit relative redirects. `plane-web`'s nginx has the same underlying `try_files $uri $uri/ ...` pattern, but its base path is `/`, which browsers always request with a trailing slash already, so the bug is latent there rather than triggered.
