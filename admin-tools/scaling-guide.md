# SmallWorlds Scaling Guide

This document outlines the procedures for scaling the SmallWorlds cluster.

## Vertical Scaling (Automated via Hermes)

The Hermes agent continuously monitors resource usage and will automatically propose vertical scaling (VM upgrades/downgrades or Volume expansion) via Pull Requests when thresholds are breached. 
- You only need to review the PR, check the cost impact, and merge. 
- Terraform will handle the infrastructure changes, though a server reboot will be required.

## Horizontal Scaling (Manual)

The current architecture is single-node. Eventually, you may need to scale horizontally by adding worker nodes.
This is necessary when:
- The largest available Hetzner VM (e.g., CPX52) is no longer sufficient.
- High Availability (HA) across multiple nodes is required for critical workloads.

### How to Add a Worker Node

1. Open `infrastructure/terraform/main.tf`.
2. Use the provided `worker-node` module:
```hcl
module "worker_1" {
  source       = "./modules/worker-node"
  hcloud_token = var.hcloud_token
  cluster_name = "smallworlds"
  server_type  = "cx43"
  ssh_keys     = [hcloud_ssh_key.default.id]
  k3s_url      = "https://${hcloud_server.control_plane.ipv4_address}:6443"
  k3s_token    = "YOUR_K3S_TOKEN" # Retrieve from control plane: cat /var/lib/rancher/k3s/server/node-token
}
```
3. Run `terraform apply`. The node will boot, install K3s, and automatically join the cluster.

### Architectural Changes Required for Multi-Node

When adding your first worker node, the following configuration changes are necessary:
1. **Traefik/Ingress**: Ensure external DNS points to a Load Balancer or both nodes.
2. **Persistent Volumes**: The `hetzner-local` StorageClass uses local node disks. Workloads tied to local disks cannot migrate between nodes. Consider migrating state to S3 (Garage) or setting up replicated storage (e.g., Longhorn) if pod mobility is required.
3. **Garage**: Garage natively supports multi-node. Add the new node to the Garage topology.
