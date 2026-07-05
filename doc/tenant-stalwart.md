# Stalwart Tenant (`infrastructure/kubernetes/tenants/stalwart/*.yaml`)

Stalwart serves as the mail server for the cluster. Its configuration includes several unique integrations with other cluster components, specifically the database (CloudNativePG), storage (Garage), and identity (Keycloak).

## Key Infrastructure Integrations

### 1. Database & Backups (`cnpg-cluster.yaml`)
Stalwart uses a CloudNativePG (CNPG) cluster for its storage backend.
- **S3 Backups**: The `backup` configuration points to `http://garage.garage-system.svc.cluster.local:3900`. 
- **Credentials**: It consumes the `garage-secret-cnpg` secret. This secret is populated automatically by the `garage-init-job` base, which ensures that CNPG has exclusive access to the `postgres-backups` S3 bucket.

### 2. Stalwart Core Configuration (`stalwart-deployment.yaml`)
- **Database Connection**: The `stalwart-config` ConfigMap points to `database-rw.stalwart.svc.cluster.local`, which is the read-write service automatically created by the CNPG operator.
- **Relay Configuration**: The `relay.json` allows relaying from Kubernetes internal subnets (`10.42.0.0/16`, `10.43.0.0/16`). This is crucial because other applications in the cluster (like Keycloak and Nextcloud) use Stalwart as an SMTP relay to send emails without needing full authentication for internal traffic.
- **Certificates**: It mounts TLS certificates from `stalwart-tls`. This secret is expected to be populated by `cert-manager` via the Ingress resource, allowing secure STARTTLS/IMAPS.

### 3. Mail Provisioner (`mail-provisioner-deployment.yaml` & `mail-provisioner-rbac.yaml`)
Because Keycloak doesn't natively push user creation events to external mail servers in a simple way, a custom sidecar/provisioner deployment is used to synchronize users.
- **Direct Database Sync**: The Python script inside `mail-provisioner-code` connects *directly* to the Keycloak PostgreSQL database (`keycloak-db-rw.keycloak.svc.cluster.local`). 
- **Cross-Namespace Secrets**: To authenticate to the Keycloak DB, the script queries the Kubernetes API to read the `keycloak-db-app` secret from the `keycloak` namespace. This is why `mail-provisioner-rbac.yaml` grants cross-namespace secret reading privileges to the `mail-provisioner` ServiceAccount.
- **Stalwart API Integration**: Once it detects a new user in the Keycloak database, it uses the `stalwart-cli` (authenticating via `STALWART_API_KEY`) to provision the mailbox in Stalwart automatically.
