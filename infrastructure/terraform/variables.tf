variable "git_url" {
  description = "The URL of the Git repository"
  type        = string
  default     = "https://github.com/stephan271/smallworlds.git"
}

variable "git_username" {
  description = "The Git username for authentication"
  type        = string
}


variable "domain_name" {
  description = "The root domain name for your cluster (e.g. smallworlds.network)"
  type        = string
}

variable "env_ext" {
  description = "The environment extension for subdomains, in subdomain syntax (e.g. \".dev\" for identity.dev.<domain>; empty for production)"
  type        = string
  default     = ""
}

variable "admin_email" {
  description = "Email address for the admin account in Nextcloud and Immich (set before terraform apply)"
  type        = string
  default     = "admin@smallworlds.network"
}


variable "git_password" {
  description = "The Git Project Access Token (Reporter role)"
  type        = string
  sensitive   = true
}

variable "hcloud_token" {
  description = "The Hetzner DNS API Token (for automating email server DNS configuration)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "keycloak_admin_password" {
  description = "Initial admin password for Keycloak (stable across reinstalls if provided in tfvars)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "nextcloud_admin_password" {
  description = "Initial admin password for Nextcloud (stable across reinstalls if provided in tfvars)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "immich_admin_password" {
  description = "Initial admin password for Immich (stable across reinstalls if provided in tfvars)"
  type        = string
  default     = ""
  sensitive   = true
}



variable "location" {
  description = <<-DESC
    Hetzner location for the server and both volumes. Leave empty (the default)
    to follow the Primary IP, which is datacenter-bound and therefore already
    decides this: moving the node elsewhere would mean a different public
    address, and with it every A record, the PTR and the tailnet.

    The scan across var.location_preference happens where the choice is still
    free — smallworlds-init.sh's ensure_primary_ip() picks a location that can
    actually serve var.server_type before it creates the Primary IP, and passes
    the answer back in here. Set this by hand only to pin a location
    deliberately; the apply then fails early, with a message, if that location
    has no var.server_type to sell.
  DESC
  type        = string
  default     = ""
}

variable "location_preference" {
  description = <<-DESC
    Locations to consider, most preferred first, when nothing pins the choice
    yet. All three are in the EU, so member data stays inside it, and each can
    hold the node, both volumes and the Primary IP together. Hetzner sells a
    server type out in one location while the others still have it, so a single
    hard-coded location is the difference between an install that proceeds and
    one that stops at "resource unavailable".
  DESC
  type        = list(string)
  default     = ["nbg1", "fsn1", "hel1"]
}

variable "server_type" {
  description = <<-DESC
    8 shared vCPU / 16 GB. 16 GB is the minimum: 8 GB nodes saturate once the
    full app suite deploys, and the probe timeouts cascade into CNPG failovers
    and OOM crashloops. Exposed as a variable because availability differs per
    location, so choosing a type and choosing a location are one decision.
  DESC
  type        = string
  default     = "cx43"
}

variable "apps" {
  description = <<-DESC
    The optional applications this community selected, by the names
    admin-tools/prepare-community-repo.sh uses. Only their DNS records are
    created: an A record for photos.<domain> with no Immich behind it sends
    members to a certificate error and makes the zone read as though the
    application were installed.

    Defaults to every optional application, so a plain `terraform apply` that
    does not pass this keeps the zone it had.
  DESC
  type        = list(string)
  default = [
    "nextcloud", "immich", "forgejo", "excalidraw", "jitsi",
    "collabora", "plane", "stalwart", "bulwark", "pod-gateway",
  ]
}

variable "use_golden_image" {
  description = "Boot from the most recent snapshot labeled smallworlds-golden=true (built by admin-tools/build-golden-image.sh) instead of plain ubuntu-24.04. Skips apt upgrades, the k3s download and ~7GB of image pulls."
  type        = bool
  default     = false
}

variable "ssh_key_id" {
  description = "Hetzner id of the shared 'SmallWorlds Admin Key', uploaded/looked-up by smallworlds-init.sh's ensure_ssh_key() before terraform apply (see main.tf locals for why this isn't a Terraform-managed resource)."
  type        = number
}

variable "public_admin_ports" {
  description = <<-DESC
    Keep SSH (22) and the Kubernetes API (6443) reachable from the public
    internet. Both are administrative; neither is needed publicly once the node
    has joined the tailnet, because tailnet traffic arrives inside the WireGuard
    tunnel and never passes this firewall.

    Defaults to true because a fresh install has no tailnet yet and
    admin-tools/setup-vpn.sh needs SSH to create one. Set to false only after
    verifying `ssh root@admin.<domain>` and kubectl from an enrolled device —
    closing these blind locks you out of the server.
  DESC
  type        = bool
  default     = true
}
