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

locals {
  # The shared admin SSH key is uploaded to Hetzner once by
  # smallworlds-init.sh's ensure_ssh_key() (idempotent, same pattern as
  # ensure_dns_zone) and its id passed in as var.ssh_key_id. Terraform does
  # not own/create this resource: dev and prod share no Terraform state, so
  # whichever one is deployed first would otherwise "claim" the key via a
  # create/lookup split — breaking the moment they're deployed in the other
  # order. Resolving it outside Terraform makes it order-independent.
  ssh_key_id = var.ssh_key_id
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

  # SSH and the Kubernetes API. Both are administrative and neither is needed
  # from the public internet once the node is on the tailnet: tailnet traffic
  # arrives inside the WireGuard tunnel and is never seen by this firewall, so
  # closing these ports does not affect `ssh root@admin.<domain>` or kubectl
  # against the tailnet address. See admin-tools/setup-vpn.sh.
  #
  # Left OPEN by default, because closing them before the tailnet works locks
  # the operator out of their own server, and a fresh install has no tailnet at
  # all — setup-vpn.sh needs SSH to join the node to it. Flip
  # `public_admin_ports = false` as a deliberate, separate apply once
  # `ssh root@admin.<domain>` has been verified from an enrolled device.
  dynamic "rule" {
    for_each = var.public_admin_ports ? ["22", "6443"] : []
    content {
      direction = "in"
      protocol  = "tcp"
      port      = rule.value
      source_ips = [
        "0.0.0.0/0",
        "::/0"
      ]
    }
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



# The Primary IP. Created by smallworlds-init.sh's ensure_primary_ip() before
# terraform runs — the same idempotent-precondition pattern as the DNS zone and
# the admin SSH key, and for the same reason: it must outlive any one Terraform
# state, and destroy-cluster.sh must not be able to take the community's public
# address away with the node.
#
# Primary IPs are datacenter-bound, so this resource is what pins the location
# once it exists. ensure_primary_ip() therefore does the location scan itself,
# at the one moment the choice is still free, and hands the answer to
# var.location.
data "hcloud_primary_ip" "main_ip" {
  name = "smallworlds-ip${local.env_slug}"
}

# Where var.server_type can actually be ordered right now. Hetzner sells a type
# out in one location while the others still have it, and an apply that only
# discovers this at the server resource has already created the firewall and
# both volumes by then, in a location that cannot run the node.
data "hcloud_server_type" "node" {
  name = var.server_type
}

locals {
  server_type_available = {
    for entry in data.hcloud_server_type.node.locations :
    entry.name => entry.available
  }

  # var.location_preference filtered to what is orderable, preference order
  # preserved. Surfaced as an output and quoted in the failure message below,
  # because "pick another location" is only useful advice if it says which.
  available_locations = [
    for name in var.location_preference : name
    if lookup(local.server_type_available, name, false)
  ]

  # An explicit location wins. Otherwise the Primary IP decides, because it
  # already has. The scan is the fallback for a bare `terraform apply` in a
  # project whose Primary IP the provider could not report a location for.
  chosen_location = coalesce(
    var.location != "" ? var.location : null,
    data.hcloud_primary_ip.main_ip.location,
    try(local.available_locations[0], null),
  )

  # Absence of data is not evidence of unavailability: if the provider reports
  # no locations at all for this type, let Hetzner answer for itself rather
  # than blocking an install on a lookup that came back empty.
  server_type_orderable = lookup(
    local.server_type_available, local.chosen_location,
    length(local.server_type_available) == 0,
  )
}

# The DNS zone is created by the smallworlds-init.sh script to avoid Terraform state conflicts
data "hcloud_zone" "existing_zone" {
  name = var.domain_name
}

locals {
  zone_id = data.hcloud_zone.existing_zone.id
}

locals {
  # Deployed by infrastructure/kubernetes/kustomization.yaml for every
  # community, so their records are unconditional: identity (Keycloak),
  # dashboard, monitoring (kube-prometheus-stack), deploy (argocd-ingress) and
  # vpn (headscale).
  core_subdomains = ["identity", "dashboard", "monitoring", "deploy", "vpn"]

  # The subdomain each optional application answers on. Mirrors the map in
  # admin-tools/generate_domain_patches.py, which writes the Ingress hosts
  # these records have to resolve to.
  app_subdomains = {
    "nextcloud"   = "files"
    "immich"      = "photos"
    "forgejo"     = "git"
    "excalidraw"  = "whiteboard"
    "jitsi"       = "meet"
    "collabora"   = "office"
    "euro-office" = "office"
    "plane"       = "plan"
    "stalwart"    = "mail"
    "bulwark"     = "webmail"
    "pod-gateway" = "pod"
  }

  selected_subdomains = [
    for app in var.apps : local.app_subdomains[app]
    if contains(keys(local.app_subdomains), app)
  ]

  # The apex belongs to production only: identity.dev.<domain> and friends are
  # a second cluster inside the same zone, and it must not take the root record
  # away from the first.
  record_names = concat(
    var.env_ext == "" ? ["@"] : [],
    local.core_subdomains,
    local.selected_subdomains,
  )

  # rDNS has to name something that resolves forward. Mail is opt-in
  # (docs/adr/0049), so a community that did not select Stalwart has no
  # mail.<domain> record and must not have its PTR pointed at one.
  rdns_hostname = contains(var.apps, "stalwart") ? "mail${var.env_ext}.${var.domain_name}" : (
    var.env_ext == "" ? var.domain_name : "dashboard${var.env_ext}.${var.domain_name}"
  )
}

# A records for the root domain, the always-deployed services, and the
# applications this community actually selected — nothing else. A record for an
# application that was never installed points members at a certificate error,
# and makes the zone read as though it were there.
resource "hcloud_zone_rrset" "app_records" {
  for_each = toset(local.record_names)

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
  dns_ptr       = local.rdns_hostname
}

# Most recent golden image built by admin-tools/build-golden-image.sh
data "hcloud_image" "golden" {
  count             = var.use_golden_image ? 1 : 0
  with_selector     = "smallworlds-golden=true"
  with_architecture = "x86"
  most_recent       = true
}

# Provision the actual VM
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

resource "hcloud_server" "smallworlds_pilot_node" {
  name        = "cc-pilot-node-01${local.env_slug}"
  image       = var.use_golden_image ? tostring(data.hcloud_image.golden[0].id) : "ubuntu-24.04"
  server_type = var.server_type
  location    = local.chosen_location

  ssh_keys     = [local.ssh_key_id]
  firewall_ids = [hcloud_firewall.k8s_firewall.id]

  user_data = templatefile("${path.module}/../cloud-init/k3s-node.yaml.tpl", {
    k3s_version            = local.bootstrap_pins["K3S_VERSION"]
    k3s_installer_url      = local.bootstrap_pins["K3S_INSTALLER_URL"]
    k3s_installer_sha256   = local.bootstrap_pins["K3S_INSTALLER_SHA256"]
    argocd_manifest_url    = local.bootstrap_pins["ARGOCD_MANIFEST_URL"]
    argocd_manifest_sha256 = local.bootstrap_pins["ARGOCD_MANIFEST_SHA256"]
    golden_image           = var.use_golden_image
    node_name              = "cc-pilot-node-01"
    server_ip              = data.hcloud_primary_ip.main_ip.ip_address
    domain_name            = var.domain_name
    env_ext                = var.env_ext
    acme_email             = var.admin_email
    hcloud_token           = var.hcloud_token
    root_app_git_url       = var.git_url
    persistent_volume      = true
    # Stable /dev/disk/by-id paths — see the automount comment on the attachments.
    data_volume_device   = hcloud_volume.smallworlds_data.linux_device
    backup_volume_device = hcloud_volume.smallworlds_backup.linux_device
  })

  public_net {
    ipv4         = data.hcloud_primary_ip.main_ip.id
    ipv6_enabled = true
  }


  lifecycle {
    ignore_changes  = [user_data]
    prevent_destroy = false # Protects the node from accidental terraform destroy in the future

    # Checked before anything is created, so a sold-out server type costs an
    # error message rather than a half-built project. Hetzner's own answer for
    # this is "resource_unavailable" at the last step, which says neither what
    # is unavailable nor where it would be.
    precondition {
      condition = local.server_type_orderable
      error_message = join(" ", [
        "Hetzner has no ${var.server_type} to sell in ${local.chosen_location} right now.",
        length(local.available_locations) > 0
        ? "It is available in: ${join(", ", local.available_locations)}."
        : "It is available in none of ${join(", ", var.location_preference)}.",
        "The Primary IP smallworlds-ip${local.env_slug} is datacenter-bound and pins the location,",
        "so moving the node means giving up that address: delete the Primary IP in the Hetzner",
        "Console and re-run the installer, which creates a new one where the hardware is.",
        "Or keep the location and choose another size with TF_VAR_server_type.",
      ])
    }
  }
}

# ------------------------------------------------------------------------------
# Persistent Network Volume — survives VM deletion and re-creation.
# All critical stateful data is stored here: Garage S3 and Immich photo library.
# Hetzner re-attaches this volume automatically when the VM is re-created.
# ------------------------------------------------------------------------------
resource "hcloud_volume" "smallworlds_data" {
  name = "smallworlds-data${local.env_slug}"
  size = 200 # GB — covers Garage (100 GB) + Immich library (50 GB) + room to grow
  # Volumes are location-bound and must match the server.
  location = local.chosen_location
  format   = "ext4"

  lifecycle {
    prevent_destroy = true # CRITICAL: never destroy this volume, it holds all user data
  }
}

# ------------------------------------------------------------------------------
# Backup volume — physically separate storage for every Recovery Point.
# See docs/adr/0048: hop one of every backup chain must not share a failure
# domain with the data it protects, and nothing on the cluster enforces a size,
# so a runaway consumer on the data volume must not be able to starve backups.
# Holds the garage-backup instance (metadata AND data).
# ------------------------------------------------------------------------------
resource "hcloud_volume" "smallworlds_backup" {
  name = "smallworlds-backup${local.env_slug}"
  size = 100 # GB — barman + Velero + pv-backup + pods + the Nextcloud copy
  # Volumes are location-bound and must match the server.
  location = local.chosen_location
  format   = "ext4"

  lifecycle {
    prevent_destroy = true # destroy-cluster.sh rewrites this when tearing down
  }
}

# automount is deliberately false on both volumes. Hetzner's automount writes its
# own fstab entry at /mnt/HC_Volume_<id>, and with two volumes attached there is
# no way for cloud-init to tell which is which afterwards — the old
# "first unmounted disk" heuristic would mount them in arbitrary order and could
# put backups on the operational volume. cloud-init mounts both by the exact
# device path Terraform passes it instead.
resource "hcloud_volume_attachment" "smallworlds_data" {
  volume_id = hcloud_volume.smallworlds_data.id
  server_id = hcloud_server.smallworlds_pilot_node.id
  automount = false

  depends_on = [hcloud_server.smallworlds_pilot_node]
}

resource "hcloud_volume_attachment" "smallworlds_backup" {
  volume_id = hcloud_volume.smallworlds_backup.id
  server_id = hcloud_server.smallworlds_pilot_node.id
  automount = false

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

output "location" {
  value       = local.chosen_location
  description = "The Hetzner location the node and both volumes were placed in."
}

output "server_type_available_locations" {
  value       = local.available_locations
  description = "Which of var.location_preference can currently sell var.server_type, most preferred first."
}

output "dns_records" {
  value       = sort(local.record_names)
  description = "The names an A record was created for, in the community's zone."
}

output "backup_volume_linux_device" {
  value       = hcloud_volume.smallworlds_backup.linux_device
  description = "The Linux device name for the backup volume holding garage-backup (docs/adr/0048)."
}


