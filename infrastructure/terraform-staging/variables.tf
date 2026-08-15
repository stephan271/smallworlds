variable "git_url" {
  description = "The URL of the Git repository"
  type        = string
  default     = "https://github.com/stephan271/smallworlds.git"
}

variable "git_username" {
  description = "The Git username for authentication"
  type        = string
  default     = ""
}

variable "domain_name" {
  description = "The root domain name (e.g., smallworlds.network)"
  type        = string
  default     = "smallworlds.network"
}

variable "env_ext" {
  description = "The environment extension for subdomains, in subdomain syntax (e.g. \".dev\")"
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
  default     = ""
}

variable "hcloud_token" {
  description = "The Hetzner DNS API Token (for automating email server DNS configuration)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "github_pr_branch" {
  description = "The target revision for ArgoCD to check out (used in ephemeral testing)"
  type        = string
  default     = "main"
}

variable "ssh_public_key_path" {
  description = "Path to the public SSH key to inject into the staging VM"
  type        = string
}

variable "use_golden_image" {
  description = "Boot from the most recent snapshot labeled smallworlds-golden=true instead of plain ubuntu-24.04"
  type        = bool
  default     = false
}

variable "location" {
  description = <<-DESC
    Hetzner location for the ephemeral staging VM. Override per run, e.g.
    TF_VAR_location=hel1 (Helsinki). Nothing here is location-bound — staging
    uses an ephemeral IP and no volumes — but var.server_type must be offered
    in whichever location is chosen, or the apply fails before the VM is made.
  DESC
  type        = string
  default     = "nbg1"
}

variable "server_type" {
  description = <<-DESC
    16 GB minimum: 8 GB nodes saturate when the full app suite deploys, and the
    probe timeouts cascade into CNPG failovers and OOM crashloops. Exposed
    alongside var.location because type availability differs per location.
  DESC
  type        = string
  default     = "cx43"
}
