# GitOps Apps Directory

This directory is monitored by the **ArgoCD Root Application**. 

Any Kubernetes YAML file placed in this directory will be automatically synchronized and deployed to the SmallWorlds cluster. 

We use this to define our entire suite of applications using the `Application` Custom Resource Definition provided by ArgoCD.

## Current Services
- `traefik.yaml`: The Ingress Controller (handles routing external traffic into the cluster).
- `cert-manager.yaml`: Automatically provisions Let's Encrypt TLS certificates for secure HTTPS.
- `keycloak.yaml`: The central Identity Provider for SSO and Passkey authentication across the CitizenNet network.

## Next Steps (Phase 1.3)
We need to add the storage operators and database operators here:
1. CloudNativePG (PostgreSQL Operator)
2. Garage (S3 Object Storage)
3. Redis (Cache)
