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
    direction = "in"
    protocol  = "tcp"
    port      = "22"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
  
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "80"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
  rule {
    direction = "in"
    protocol  = "tcp"
    port      = "6443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

# Provision the staging VM
resource "hcloud_server" "smallworlds_staging_node" {
  name        = "cc-staging-node-01"
  image       = "ubuntu-24.04"
  server_type = "cpx32"
  location    = "fsn1"
  firewall_ids = [hcloud_firewall.k8s_firewall_staging.id]
  ssh_keys    = [hcloud_ssh_key.staging_key.id]
  
  public_net {
    ipv4_enabled = true
    ipv6_enabled = true
  }

  user_data = templatefile("${path.module}/cloud-init.yaml.tpl", {
    domain_name      = var.domain_name
    git_url          = var.git_url
    github_pr_branch = var.github_pr_branch
    admin_email      = var.admin_email
    server_ip        = "0.0.0.0" # Passed down, but dynamic IP is used via local scripts instead
  })
}

output "server_ipv4" {
  value       = hcloud_server.smallworlds_staging_node.ipv4_address
  description = "The public IP address of the staging node"
}
