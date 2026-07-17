# Local (LAN) Deployment

SmallWorlds can run on an existing Linux machine in your LAN — a laptop,
mini-PC or home server — instead of a Hetzner Cloud VM. The installer
(`smallworlds-init.sh`) asks for the deployment target as its first question;
choosing `local` swaps the Terraform/cloud-init provisioning for
`infrastructure/local/bootstrap-local-node.sh`, which performs the exact same
k3s + ArgoCD bootstrap directly on the target machine over SSH.

Everything above the provisioning layer is identical: the same ArgoCD root
app, the same sync waves, the same community overlay repo. A change that works
on Hetzner works locally and vice versa.

## Requirements

- A systemd-based Linux distribution (Ubuntu, Debian, Fedora, openSUSE, ...).
  On SELinux-enforcing systems (Fedora/RHEL) the k3s installer pulls in
  `k3s-selinux` automatically.
- 16 GB RAM minimum for the full app suite; 32 GB recommended.
- 100 GB+ free disk space for the data directory (Garage S3, Immich library,
  databases).
- SSH access from the machine running the installer, with root login or a
  sudo-capable user (`ssh -t` is used, so an interactive sudo password prompt
  works). Use the literal target `localhost` to install on the machine you are
  running the installer on.
- No k3s already installed — the bootstrap refuses to adopt an existing
  cluster. Remove one first with
  `sudo infrastructure/local/bootstrap-local-node.sh --uninstall`.
- **firewalld must be disabled** (or configured per the
  [k3s requirements](https://docs.k3s.io/installation/requirements)) — k3s pod
  and service traffic is silently dropped otherwise. The bootstrap warns but
  does not change your firewall. With ufw, allow 80/tcp, 443/tcp, 6443/tcp and
  10000/udp (Jitsi).

## What differs from a Hetzner deployment

| Concern | Hetzner | Local |
|---|---|---|
| Provisioning | Terraform (VM, firewall, volume, DNS) | `bootstrap-local-node.sh` over SSH |
| Public DNS | Hetzner DNS zone + A records | none — you provide LAN name resolution |
| TLS | Let's Encrypt (HTTP-01) | self-signed `ClusterIssuer` (same as staging) |
| Persistent data | Hetzner network volume at `/mnt/smallworlds-data` | local directory (default `/var/lib/smallworlds-data`), symlinked to `/mnt/smallworlds-data` |
| Kubeconfig label | `production` / `dev` | `local` / `local-<ext>` (`~/.smallworlds/kubeconfigs/local.yaml`) |
| Mail (Stalwart) | fully functional (public IP, PTR, port 25) | deploys, but external delivery does not work behind NAT; no Hetzner DNS automation |
| Golden image | optional fast-boot snapshot | n/a |

The manifests are untouched by the target choice: `persistent-storage.yaml`
hard-codes `hostPath: /mnt/smallworlds-data/...`, which the bootstrap
satisfies via a symlink to the chosen data directory. The self-signed issuer
is published under the name `letsencrypt-prod`, so the `cluster-issuer`
annotations on all Ingresses work unchanged (the same trick the staging
pipeline uses).

## LAN name resolution

Nothing manages DNS for a local deployment. Two layers are involved:

1. **In-cluster**: handled automatically. The bootstrap writes the
   `coredns-custom` ConfigMap so pods resolve the app hostnames to the node's
   LAN IP (required for OIDC token exchanges against Keycloak).
2. **Your devices**: the installer prints — and saves to
   `~/.smallworlds/hosts-local.txt` — a ready-made hosts line mapping every
   app hostname to the server's LAN IP. Add it to your router's local DNS, a
   Pi-hole, or the `/etc/hosts` of each device.

Because resolution is purely local, the domain does not need to be registered
with any registrar — but using a domain you own avoids clashes with public DNS
if a device leaves the LAN.

Note: the laptop's LAN IP is baked into the kubeconfig, the CoreDNS override
and your hosts entries — give the machine a static IP / DHCP reservation in
your router.

## TLS

Certificates are self-signed; browsers warn on first visit — expected.
`FULL_OIDC=1` e2e runs are impossible against self-signed certs (same
limitation as ephemeral staging).

If your machine *is* publicly reachable on ports 80/443 (port forwarding +
public DNS pointing at your connection), you can switch to Let's Encrypt by
setting `ACME_EMAIL` in the bootstrap config and re-applying the
`letsencrypt-prod` ClusterIssuer — see the header of
`infrastructure/local/bootstrap-local-node.sh`.

## Lifecycle

```bash
# Install / reinstall (from the machine running the installer)
./smallworlds-init.sh                # choose target "local"

# Uninstall k3s but keep all user data (on the server)
sudo bash infrastructure/local/bootstrap-local-node.sh --uninstall

# Full wipe including user data
sudo bash infrastructure/local/bootstrap-local-node.sh --uninstall --purge-data
```

A "rebuild preserving data" is therefore: `--uninstall`, then re-run the
installer — cluster state (`<data-dir>/k3s`) and app data survive in the data
directory. The Hetzner-specific admin tools (`destroy-cluster.sh`,
`prepare-fresh-rebuild.sh`, `build-golden-image.sh`, `test-pr-locally.sh`) do
not apply to local deployments.

## Smoke tests

The shallow e2e suite works against a local deployment once the machine
running the tests has the hosts entries in place:

```bash
cd e2e-tests
DOMAIN=<your-domain> npx playwright test   # shallow (OIDC redirect) checks
```
