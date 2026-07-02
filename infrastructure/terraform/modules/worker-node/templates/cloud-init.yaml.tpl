#cloud-config
package_update: true
package_upgrade: true

packages:
  - curl
  - fail2ban

runcmd:
  - curl -sfL https://get.k3s.io | K3S_URL=${k3s_url} K3S_TOKEN=${k3s_token} sh -
