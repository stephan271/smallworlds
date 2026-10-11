terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.54"
    }
  }
}

provider "hcloud" {
  token = var.hcloud_token
}

# Add the ephemeral SSH key
resource "hcloud_ssh_key" "staging_key" {
  name       = "staging-ephemeral-key"
  public_key = file(var.ssh_public_key_path)
}

# Create a secure firewall for the ephemeral node
resource "hcloud_firewall" "k8s_firewall_staging" {
  name = "smallworlds-firewall-staging"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "80"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "6443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

# Most recent golden image built by admin-tools/build-golden-image.sh
data "hcloud_image" "golden" {
  count             = var.use_golden_image ? 1 : 0
  with_selector     = "smallworlds-golden=true"
  with_architecture = "x86"
  most_recent       = true
}

# Provision the staging VM
# What a new node installs before Argo CD takes over — the K3s version, the K3s
# install script and the Argo CD manifest, each with its SHA-256 — is read from
# the one file every bootstrap path shares (docs/adr/0057). Parsed here rather
# than passed in, so a bare `terraform apply` installs the same thing.
locals {
  bootstrap_pins = {
    for line in split("\n", file("${path.module}/../bootstrap-pins.env")) :
    regex("^([A-Z0-9_]+)=(.*)$", trimspace(line))[0] => regex("^([A-Z0-9_]+)=(.*)$", trimspace(line))[1]
    if can(regex("^[A-Z0-9_]+=", trimspace(line)))
  }
}

resource "hcloud_server" "smallworlds_staging_node" {
  name  = "cc-staging-node-01"
  image = var.use_golden_image ? tostring(data.hcloud_image.golden[0].id) : "ubuntu-24.04"
  # 16 GB minimum: 8GB nodes saturate when the full app suite deploys — probe
  # timeouts cascade into CNPG failovers and OOM crashloops
  server_type  = var.server_type
  location     = var.location
  firewall_ids = [hcloud_firewall.k8s_firewall_staging.id]
  ssh_keys     = [hcloud_ssh_key.staging_key.id]

  public_net {
    ipv4_enabled = true
    ipv6_enabled = true
  }

  user_data = templatefile("${path.module}/../cloud-init/k3s-node.yaml.tpl", {
    k3s_version            = local.bootstrap_pins["K3S_VERSION"]
    k3s_installer_url      = local.bootstrap_pins["K3S_INSTALLER_URL"]
    k3s_installer_sha256   = local.bootstrap_pins["K3S_INSTALLER_SHA256"]
    argocd_manifest_url    = local.bootstrap_pins["ARGOCD_MANIFEST_URL"]
    argocd_manifest_sha256 = local.bootstrap_pins["ARGOCD_MANIFEST_SHA256"]
    golden_image           = var.use_golden_image
    node_name              = "cc-staging-node-01"
    server_ip              = "" # dynamic — discovered at runtime on the node
    domain_name            = var.domain_name
    env_ext                = ""
    acme_email             = "" # self-signed issuer; no DNS, so ACME can't validate
    hcloud_token           = "" # unused: self-signed branch never reaches the DNS01 secret
    root_app_git_url       = "" # test-pr-locally.sh applies the Applications itself
    persistent_volume      = var.separate_backup_volumes
    data_volume_device     = var.separate_backup_volumes ? hcloud_volume.staging_data[0].linux_device : ""
    backup_volume_device   = var.separate_backup_volumes ? hcloud_volume.staging_backup[0].linux_device : ""
  })
}

# Restore drills use the same failure-domain split as a real cluster. Both
# volumes are disposable, but losing or clearing the operational side must not
# also remove the Recovery Point being exercised.
resource "hcloud_volume" "staging_data" {
  count    = var.separate_backup_volumes ? 1 : 0
  name     = "smallworlds-staging-data"
  size     = var.data_volume_size
  location = var.location
  format   = "ext4"
}

resource "hcloud_volume" "staging_backup" {
  count    = var.separate_backup_volumes ? 1 : 0
  name     = "smallworlds-staging-backup"
  size     = var.backup_volume_size
  location = var.location
  format   = "ext4"
}

resource "hcloud_volume_attachment" "staging_data" {
  count     = var.separate_backup_volumes ? 1 : 0
  volume_id = hcloud_volume.staging_data[0].id
  server_id = hcloud_server.smallworlds_staging_node.id
  automount = false
}

resource "hcloud_volume_attachment" "staging_backup" {
  count     = var.separate_backup_volumes ? 1 : 0
  volume_id = hcloud_volume.staging_backup[0].id
  server_id = hcloud_server.smallworlds_staging_node.id
  automount = false
}

output "server_ipv4" {
  value       = hcloud_server.smallworlds_staging_node.ipv4_address
  description = "The public IP address of the staging node"
}

output "data_volume_id" {
  value       = var.separate_backup_volumes ? hcloud_volume.staging_data[0].id : null
  description = "Disposable operational volume used by a restore drill."
}

output "backup_volume_id" {
  value       = var.separate_backup_volumes ? hcloud_volume.staging_backup[0].id : null
  description = "Disposable physically separate Recovery Point volume."
}
