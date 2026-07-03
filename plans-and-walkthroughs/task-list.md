# SmallWorlds: Detailed Implementation Task List

This task list breaks down the SmallWorlds implementation into actionable, iterative steps, starting with the foundation and the Immich pilot.

## Phase 1: Foundation and Immich Pilot (Initial Goal)
*Goal: Establish a robust, automated Kubernetes environment on a European Cloud and deploy a fully functional Immich instance.*

- [ ] **1.1 Infrastructure Provisioning**
  - [ ] Select a European cloud provider (e.g., Hetzner Cloud or Scaleway).
  - [ ] Write Terraform (or OpenTofu) scripts to provision VMs, private networking, and firewall rules.
  - [ ] Automate the bootstrap of a lightweight Kubernetes cluster (K3s/RKE2).
- [ ] **1.2 Core Cluster Services (The "Operating System")**
  - [ ] Install a GitOps Controller (ArgoCD). From this point on, all installations are managed via Git commits.
  - [ ] Install Ingress Controller (e.g., Traefik or Ingress-NGINX).
  - [ ] Install Cert-Manager for fully automated Let's Encrypt TLS certificates.
- [ ] **1.3 Data Persistence & Backing Services**
  - [ ] Deploy S3-compatible Object Storage (Garage) for scalable media storage.
  - [ ] Deploy PostgreSQL Operator (e.g., CloudNativePG) for automated, self-healing databases.
  - [ ] Deploy basic cache layer (Redis).
- [ ] **1.4 Immich Deployment**
  - [ ] Configure Immich Helm chart to use the provisioned S3 storage, Postgres DB, and Redis cache.
  - [ ] Expose Immich via public domain with automated HTTPS.
  - [ ] Test web UI and mobile app connectivity.
  - [ ] Test Machine Learning tasks (facial recognition).

## Phase 2: Security, Identity, and Backup
*Goal: Secure the platform, implement Single Sign-On (SSO), and ensure no data is ever lost.*

- [x] **2.1 Identity & Access Management (IAM)**
  - [x] Deploy Keycloak as the central Identity Provider (IdP).
  - [x] Configure Passkey (FIDO2/WebAuthn) support in Keycloak.
  - [x] Integrate Immich with Keycloak via OIDC for SSO.
- [ ] **2.2 Automated Backups**
  - [ ] Deploy Velero for Kubernetes cluster state backup.
  - [ ] Implement automated Rclone/Borg jobs for encrypted, off-site data backups of S3 buckets and databases.
- [ ] **2.3 Observability**
  - [ ] Deploy Prometheus / Grafana stack for monitoring cluster health.
  - [ ] Deploy Promtail/Loki for centralized log aggregation.

## Phase 3: Expanding the Application Suite
*Goal: Fulfill the "99% coverage" goal by rolling out the remaining prioritized applications via the GitOps pipeline.*

- [ ] **3.1 Collaboration Tools**
  - [ ] Deploy Nextcloud (Files, Calendar, Contacts) connected to Keycloak SSO and S3 storage.
  - [ ] Deploy Collabora Online for document editing.
- [ ] **3.2 Communications**
  - [ ] Deploy Mailcow (or an equivalent K8s native mail architecture).
  - [ ] Deploy Matrix (Synapse/Dendrite) for secure, federated messaging (WhatsApp alternative).
  - [ ] Deploy Jitsi Meet for video conferencing.
- [ ] **3.3 Web & Knowledge Base**
  - [ ] Deploy Wiki.js for community documentation.
  - [ ] Deploy WordPress for standard web hosting needs.
  - [ ] Deploy Forgejo for Git repositories and project tracking.

## Phase 4: Federation and Scaling
*Goal: Connect individual "small worlds" into the wider CitizenNet.*

- [ ] **4.1 Federation Setup**
  - [ ] Configure ActivityPub federation (e.g., Mastodon or Lemmy).
  - [ ] Test cross-cloud messaging and data sharing.
- [ ] **4.2 AI Integration**
  - [ ] Establish connection to (or deploy) shared GPU cloud instances for heavy AI workloads (LLMs, DeepSeek/Llama).
  - [ ] Integrate LLM APIs into Nextcloud and Matrix.

## Phase 5: Complete User Automation
*Goal: Zero-touch provisioning for new communities.*

- [ ] **5.1 Community Bootstrap Boilerplate**
  - [ ] Create a single CLI tool or web portal where a user enters their domain and cloud provider API keys, and the entire Phase 1-3 stack spins up automatically.
- [ ] **5.2 Helpdesk & Community Support**
  - [ ] Deploy Zammad with AI-assisted response models for community self-help.
