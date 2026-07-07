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
  description = "The root domain name (e.g., smallworlds.network)"
  type        = string
  default     = "smallworlds.network"
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
  description = "Hetzner location for the server and data volume. The Primary IP must be created in the same location. e.g. nbg1 (Nuremberg), fsn1 (Falkenstein), hel1 (Helsinki)."
  type        = string
  default     = "nbg1"
}

variable "use_golden_image" {
  description = "Boot from the most recent snapshot labeled smallworlds-golden=true (built by admin-tools/build-golden-image.sh) instead of plain ubuntu-24.04. Skips apt upgrades, the k3s download and ~7GB of image pulls."
  type        = bool
  default     = false
}
