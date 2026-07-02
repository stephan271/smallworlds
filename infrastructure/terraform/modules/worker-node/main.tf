variable "hcloud_token" {
  type        = string
  description = "Hetzner Cloud API Token"
}

variable "cluster_name" {
  type        = string
  description = "Name of the K3s cluster"
}

variable "server_type" {
  type        = string
  description = "Hetzner server type (e.g. cpx21)"
  default     = "cpx21"
}

variable "location" {
  type        = string
  description = "Datacenter location"
  default     = "nbg1"
}

variable "ssh_keys" {
  type        = list(string)
  description = "List of SSH key IDs or names"
}

variable "k3s_url" {
  type        = string
  description = "URL of the K3s control plane"
}

variable "k3s_token" {
  type        = string
  description = "K3s node join token"
  sensitive   = true
}

resource "hcloud_server" "worker" {
  name        = "${var.cluster_name}-worker"
  image       = "ubuntu-24.04"
  server_type = var.server_type
  location    = var.location
  ssh_keys    = var.ssh_keys

  user_data = templatefile("${path.module}/templates/cloud-init.yaml.tpl", {
    k3s_url   = var.k3s_url
    k3s_token = var.k3s_token
  })
}
