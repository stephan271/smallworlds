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
