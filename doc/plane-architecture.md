# Plane Architecture & Configuration

This document describes how the Plane open-source project management application is integrated into the SmallWorlds GitOps cluster.

## Deployment Details

Plane is deployed using the official community Helm chart (`plane-ce`), managed through Kustomize as a tenant application.

- **Source Code**: [https://github.com/makeplane/helm-charts](https://github.com/makeplane/helm-charts)
- **Helm Repository**: `https://helm.plane.so/`
- **Application URL**: `https://plan.<domain>`

## Infrastructure Integration

By default, the Plane Helm chart attempts to provision its own Redis, MinIO, and PostgreSQL components. To ensure consistency, ease of backups, and resource efficiency within SmallWorlds, these are disabled in favor of dedicated resources provisioned alongside the application.

1. **PostgreSQL Database**
   A dedicated PostgreSQL cluster (`cnpg-cluster.yaml`) is spun up using CloudNativePG. It automatically inherits the `garage` S3 credentials to ensure automated backups of the Plane database.
2. **Redis Cache**
   A dedicated Redis deployment (`redis.yaml`) provides the caching layer Plane requires.

## Authentication (OIDC)

Plane delegates authentication to the central Keycloak identity provider via OIDC. 
The OIDC configuration is injected into the application via environment variables in the `values.yaml` Helm chart configuration:

- The standard `keycloak-client-job` base is included in Plane's Kustomize configuration to register the `plan` client with Keycloak.
- The resulting `keycloak-secret` is mounted into the Plane deployment, populating the `OPENID_CLIENT_ID` and `OPENID_CLIENT_SECRET` environment variables.
- The redirect URI `https://plan.<domain>/auth/oidc/callback` is registered during the Keycloak client job execution.
