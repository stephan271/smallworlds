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
  count      = var.env_ext == "" ? 1 : 0
  name       = "SmallWorlds Admin Key"
  public_key = file(var.ssh_public_key_path)
}

data "hcloud_ssh_key" "existing_admin" {
  count = var.env_ext != "" ? 1 : 0
  name  = "SmallWorlds Admin Key"
}

locals {
  ssh_key_id = var.env_ext == "" ? hcloud_ssh_key.smallworlds_admin[0].id : data.hcloud_ssh_key.existing_admin[0].id
  # Hetzner resource names use a dash form of the extension (".dev" -> "-dev")
  # since dots belong to DNS, not resource naming. DNS names use var.env_ext
  # verbatim: env_ext=".dev" yields identity.dev.<domain> etc.
  env_slug = replace(var.env_ext, ".", "-")
}

# Create a secure firewall for the node
resource "hcloud_firewall" "k8s_firewall" {
  name = "smallworlds-firewall${local.env_slug}"

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

  # Allow Kubernetes API (für externen kubectl Zugriff)
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

  # Allow Jitsi Videobridge (UDP)
  rule {
    direction = "in"
    protocol  = "udp"
    port      = "10000"
    source_ips = [
      "0.0.0.0/0",
      "::/0"
    ]
  }

}



# Fetch the static Primary IP created in the Hetzner Cloud Console.
# NOTE: Primary IPs are datacenter-bound — create it in the same location
# as var.location (e.g. Nuremberg/nbg1), or the server cannot attach it.
data "hcloud_primary_ip" "main_ip" {
  name = "Meine-Small-World-Cluster-IP${local.env_slug}"
}

# Create the Hetzner DNS zone automatically for your domain
resource "hcloud_zone" "smallworlds_zone" {
  count = var.env_ext == "" ? 1 : 0
  name  = var.domain_name
  mode  = "primary"
  ttl   = 3600
}

data "hcloud_zone" "existing_zone" {
  count = var.env_ext != "" ? 1 : 0
  name  = var.domain_name
}

locals {
  zone_id = var.env_ext == "" ? hcloud_zone.smallworlds_zone[0].id : data.hcloud_zone.existing_zone[0].id
}

# Automatically create the A records for the root domain and all service subdomains
resource "hcloud_zone_rrset" "app_records" {
  for_each = toset([
    for r in [
      "@",
      "identity",
      "dashboard",
      "files",
      "photos",
      "git",
      "mail",
      "webmail",
      "monitoring",
      "whiteboard",
      "meet",
      "office",
      "plan",
      "deploy"
    ] : r if r != "@" || var.env_ext == ""
  ])

  zone = local.zone_id
  name = each.value == "@" ? "@" : "${each.value}${var.env_ext}"
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
  dns_ptr       = "mail${var.env_ext}.${var.domain_name}"
}

# Most recent golden image built by admin-tools/build-golden-image.sh
data "hcloud_image" "golden" {
  count             = var.use_golden_image ? 1 : 0
  with_selector     = "smallworlds-golden=true"
  with_architecture = "x86"
  most_recent       = true
}

# Provision the actual VM
resource "hcloud_server" "smallworlds_pilot_node" {
  name        = "cc-pilot-node-01${local.env_slug}"
  image       = var.use_golden_image ? tostring(data.hcloud_image.golden[0].id) : "ubuntu-24.04"
  server_type = "cx43" # 8 shared vCPU (AMD), 16 GB RAM. Recommended x86 architecture for K3s and ML.
  location    = var.location

  ssh_keys = [local.ssh_key_id]
  firewall_ids = [hcloud_firewall.k8s_firewall.id]
  
  user_data = templatefile("${path.module}/cloud-init.yaml.tpl", {
    domain_name              = var.domain_name
    env_ext                  = var.env_ext
    git_url               = var.git_url
    git_username          = var.git_username
    git_password          = var.git_password

    admin_email              = var.admin_email

    server_ip                = data.hcloud_primary_ip.main_ip.ip_address
    hcloud_token        = var.hcloud_token
    golden_image        = var.use_golden_image
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
  name     = "smallworlds-data${local.env_slug}"
  size     = 200 # GB — covers Garage (100 GB) + Immich library (50 GB) + room to grow
  location = var.location # Volumes are location-bound and must match the server
  format   = "ext4"

  lifecycle {
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



