# SmallWorlds Setup Guide

This document outlines the deployment process for a SmallWorlds GitOps cluster. The architecture relies on an upstream foundation repository and a private, user-controlled configuration repository.

> [!TIP]
> Refer to the [SmallWorlds Architecture Diagram](smallworlds_architecture.html) for system topology and data flows.

## System Components

This project is built upon several foundational open-source technologies, core infrastructure services (installed by default), and optional user applications (selectively installed during initialization):

### Infrastructure and Cluster Management

| Name | Source URL | Role in this Project |
| :--- | :--- | :--- |
| **Terraform** | [terraform.io](https://www.terraform.io/) | Infrastructure as Code tool used to provision the underlying cloud resources and bootstrap the cluster. |
| **Kubernetes** | [kubernetes.io](https://kubernetes.io/) | Core container orchestration system that serves as the foundation for the cluster. |
| **Argo CD** | [argoproj.github.io/cd](https://argoproj.github.io/cd/) | GitOps continuous delivery tool that synchronizes cluster state with the configuration repository. |
| **Velero** | [velero.io](https://velero.io/) | Cluster backup and disaster recovery solution. |
| **Grafana** | [grafana.com](https://grafana.com/) | Operational dashboard for cluster monitoring and observability. |
| **CloudNativePG** | [cloudnative-pg.io](https://cloudnative-pg.io/) | High-availability PostgreSQL database clustering. |
| **Garage** | [garagehq.deuxfleurs.fr](https://garagehq.deuxfleurs.fr/) | S3-compatible object storage backend. |
| **Homepage** | [gethomepage.dev](https://gethomepage.dev/) | Application dashboard automatically configured and accessible at `dashboard.<domain>`. |
| **Keycloak** | [keycloak.org](https://www.keycloak.org/) | Identity Provider (IdP) for Single Sign-On (SSO) and WebAuthn/Passkey management. |
| **Stalwart** | [stalw.art](https://stalw.art/) | Self-hosted mail server with OIDC directory integration. |
| **Traefik** | [traefik.io](https://traefik.io/) | Ingress routing and reverse proxy for handling incoming requests. |
| **Cert-Manager** | [cert-manager.io](https://cert-manager.io/) | Automated TLS certificate provisioning and management. |

### End User Applications

| Name | Source URL | Role in this Project |
| :--- | :--- | :--- |
| **Excalidraw** | [excalidraw.com](https://excalidraw.com/) | Virtual collaborative whiteboard tool. |
| **Forgejo** | [forgejo.org](https://forgejo.org/) | Git repository management and software collaboration. |
| **Immich** | [immich.app](https://immich.app/) | High performance photo and video backup. |
| **Jitsi Meet** | [jitsi.org](https://jitsi.org/) | Secure video conferencing and communication platform. |
| **Nextcloud** | [nextcloud.com](https://nextcloud.com/) | File synchronization and collaboration. |
| **Roundcube** | [roundcube.net](https://roundcube.net/) | IMAP webmail client connected to Stalwart. |

---

## Deployment Instructions

### 1. Configuration Repository Initialization
A private Git repository is required to store application state and configuration overrides.

First create a completely empty private repo, e.g. at https://github.com/your-username/my-community-config . Make sure to create a Personal Access Token as a password to be able to push to this repo later.

Then Execute the initialization script from the root of this repository:
```bash
./admin-tools/prepare-community-repo.sh
```
This script handles:
- Interactive selection of optional applications.
- Generation of the corresponding `kustomization.yaml` overlay.
- Initialization of the local Git repository and initial commit.

Make sure to push the state to the remote repo (the script will ask you to do so)

### 2. Infrastructure Provider Setup
SmallWorlds utilizes Hetzner Cloud.
1. Create a Hetzner Cloud account and a new project.
2. Generate an API Token with **Read & Write** permissions. Save this token.

### 3. Cluster Provisioning
Execute the bootstrap script to provision the VM, configure DNS, and install Kubernetes/ArgoCD.

```bash
git clone https://github.com/stephan271/smallworlds.git
cd smallworlds
./smallworlds-init.sh
```
When prompted for Git credentials, provide:
- **URL**: The HTTPS URL of your private configuration repository (SSH URLs are unsupported).
- **Username**: Your Git platform username.
- **Access Token**: A Personal Access Token (PAT) with read-only access to repository contents.

### 4. DNS Configuration
DNS records are automatically managed via the Hetzner API token provided during provisioning. Subdomains are routed to the provisioned server IP.

### 5. Authentication Configuration
By default, registration is invitation-only. To enable self-registration, patch the Keycloak configuration via your `kustomization.yaml`:

```yaml
patches:
  - target:
      kind: Job
      name: keycloak-realm-config
      namespace: keycloak
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/1/value
        value: "self-registration"
```

### 6. Custom Application Deployment
To deploy external applications, add standard Kubernetes manifests to your configuration repository and declare them in your `kustomization.yaml`. ArgoCD will synchronize the state.

---

## Maintenance Operations

### Rebuild (Preserve Data)
This procedure replaces the VM while retaining the persistent volume containing cluster state and data.
```bash
cd infrastructure/terraform
terraform destroy -target=hcloud_server.smallworlds_pilot_node
terraform apply
```

### Clean Rebuild (Wipe Data, Preserve TLS Certificates)
This procedure wipes all cluster data but backups TLS certificates to avoid Let's Encrypt rate limits.
```bash
./admin-tools/prepare-fresh-rebuild.sh
cd infrastructure/terraform
terraform destroy -target=hcloud_server.smallworlds_pilot_node
terraform apply
```

### Upstream Updates
By default, the `kustomization.yaml` targets `ref=HEAD`, pulling continuous updates from the foundation repository. 
To control update timing, pin the reference to a specific version tag (e.g., `ref=v1.2.0`). For automated dependency management, integrate RenovateBot or Dependabot into your configuration repository.
