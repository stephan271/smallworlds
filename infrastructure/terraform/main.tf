terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.54"
    }
  }
}

# The Hetzner Cloud API Token must be provided via the HCLOUD_TOKEN environment variable.
provider "hcloud" {}

variable "ssh_public_key_path" {
  description = "Path to the public SSH key used to access the instance"
  type        = string
  default     = "~/.ssh/id_ed25519.pub"
}

# Upload your local SSH key to Hetzner so it can be injected into the VM
resource "hcloud_ssh_key" "smallworlds_admin" {
  name       = "SmallWorlds Admin Key"
  public_key = file(var.ssh_public_key_path)
}

# Create a secure firewall for the node
resource "hcloud_firewall" "k8s_firewall" {
  name = "smallworlds-firewall"

  # Allow HTTP
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "80"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow HTTPS
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "443"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow SSH
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "22"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }
  
  # Allow Kubernetes API (K3s default)
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "6443"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow Email (SMTP)
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "25"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow Email Submission
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "587"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow IMAP
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "143"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow IMAPS
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "993"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

  # Allow JMAP / Webmail
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "8080"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }
}

# Create random passwords for applications
resource "random_password" "keycloak_admin" {
  length  = 32
  special = false
}

resource "random_password" "nextcloud_admin" {
  length  = 32
  special = false
}

resource "random_password" "immich_admin" {
  length  = 32
  special = false
}

resource "random_password" "stalwart_admin" {
  length  = 32
  special = false
}

resource "random_password" "forgejo_admin" {
  length  = 32
  special = false
}

# Fetch the static Primary IP created in the Hetzner Cloud Console
data "hcloud_primary_ip" "main_ip" {
  name = "Meine-Small-World-Cluster-IP"
}

# Create the Hetzner DNS zone automatically for your domain
resource "hcloud_zone" "smallworlds_zone" {
  name = var.domain_name
  mode = "primary"
  ttl  = 3600
}

# Automatically create the A records for the root domain and all service subdomains
resource "hcloud_zone_rrset" "app_records" {
  for_each = toset([
    "@",
    "identity",
    "files",
    "photos",
    "git",
    "mail",
    "webmail"
  ])

  zone = hcloud_zone.smallworlds_zone.id
  name = each.value
  type = "A"
  ttl  = 3600

  records = [
    {
      value = data.hcloud_primary_ip.main_ip.ip_address
    }
  ]
}

# Automate Reverse DNS (PTR Record) for your server's primary IP
resource "hcloud_rdns" "main_ip_ptr" {
  primary_ip_id = data.hcloud_primary_ip.main_ip.id
  ip_address    = data.hcloud_primary_ip.main_ip.ip_address
  dns_ptr       = "mail.${var.domain_name}"
}

# Provision the actual VM
resource "hcloud_server" "smallworlds_pilot_node" {
  name        = "cc-pilot-node-01"
  image       = "ubuntu-24.04"
  server_type = "cpx32" # 4 vCPU (AMD), 8 GB RAM. Recommended x86 architecture for K3s and ML.
  location    = "fsn1" # Falkenstein, Germany. Or nbg1 (Nuremberg), hel1 (Helsinki).

  ssh_keys = [hcloud_ssh_key.smallworlds_admin.id]
  firewall_ids = [hcloud_firewall.k8s_firewall.id]
  
  user_data = templatefile("${path.module}/cloud-init.yaml.tpl", {
    domain_name              = var.domain_name
    git_url               = var.git_url
    git_username          = var.git_username
    git_password          = var.git_password

    admin_email              = var.admin_email
    keycloak_admin_password  = var.keycloak_admin_password != "" ? var.keycloak_admin_password : random_password.keycloak_admin.result
    nextcloud_admin_password = var.nextcloud_admin_password != "" ? var.nextcloud_admin_password : random_password.nextcloud_admin.result
    immich_admin_password    = var.immich_admin_password != "" ? var.immich_admin_password : random_password.immich_admin.result
    stalwart_admin_password  = random_password.stalwart_admin.result
    forgejo_admin_password   = random_password.forgejo_admin.result
    server_ip                = data.hcloud_primary_ip.main_ip.ip_address
    hcloud_token        = var.hcloud_token
  })

  public_net {
    ipv4         = data.hcloud_primary_ip.main_ip.id
    ipv6_enabled = true
  }


  lifecycle {
    ignore_changes = [user_data]
    prevent_destroy = false # Protects the node from accidental terraform destroy in the future
  }
}

# ------------------------------------------------------------------------------
# Persistent Network Volume — survives VM deletion and re-creation.
# All critical stateful data is stored here: Garage S3 and Immich photo library.
# Hetzner re-attaches this volume automatically when the VM is re-created.
# ------------------------------------------------------------------------------
resource "hcloud_volume" "smallworlds_data" {
  name     = "smallworlds-data"
  size     = 200 # GB — covers Garage (100 GB) + Immich library (50 GB) + room to grow
  location = "fsn1"
  format   = "ext4"

  lifecycle {
    ignore_changes = [user_data]
    prevent_destroy = true # CRITICAL: never destroy this volume, it holds all user data
  }
}

resource "hcloud_volume_attachment" "smallworlds_data" {
  volume_id = hcloud_volume.smallworlds_data.id
  server_id = hcloud_server.smallworlds_pilot_node.id
  automount = true

  depends_on = [hcloud_server.smallworlds_pilot_node]
}

output "server_ipv4" {
  value       = hcloud_server.smallworlds_pilot_node.ipv4_address
  description = "The public IP address of the node. Point your domain A record here."
}

output "data_volume_linux_device" {
  value       = hcloud_volume.smallworlds_data.linux_device
  description = "The Linux device name for the persistent data volume (e.g., /dev/sdb). Used for mount configuration."
}

output "keycloak_admin_password" {
  value       = random_password.keycloak_admin.result
  description = "The admin password for Keycloak. Use with username 'admin'."
  sensitive   = true
}

output "nextcloud_admin_password" {
  value       = random_password.nextcloud_admin.result
  description = "The admin password for Nextcloud. Use with username 'admin'."
  sensitive   = true
}

output "immich_admin_password" {
  value       = random_password.immich_admin.result
  description = "The admin password for Immich. Use with the configured admin_email."
  sensitive   = true
}

output "stalwart_admin_password" {
  value       = random_password.stalwart_admin.result
  description = "The admin password for Stalwart Mail. Use with username 'admin'."
  sensitive   = true
}

output "forgejo_admin_password" {
  value       = random_password.forgejo_admin.result
  description = "The admin password for Forgejo Git. Use with username 'admin'."
  sensitive   = true
}
