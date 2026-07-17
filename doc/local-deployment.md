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

A local deployment runs in one of two modes, chosen in the wizard: **LAN-only**
(the default) or **internet-exposed** (public DNS + Let's Encrypt, see the
"Internet exposure" section below).

| Concern | Hetzner | Local (LAN-only) | Local (internet-exposed) |
|---|---|---|---|
| Provisioning | Terraform (VM, firewall, volume, DNS) | `bootstrap-local-node.sh` over SSH | same |
| Public DNS | Hetzner DNS zone + A records (Terraform) | none — you provide LAN name resolution | Hetzner DNS zone + A records, maintained by an in-cluster DDNS CronJob |
| TLS | Let's Encrypt (HTTP-01) | self-signed `ClusterIssuer` (same as staging) | Let's Encrypt (HTTP-01) |
| Persistent data | Hetzner network volume at `/mnt/smallworlds-data` | local directory (default `/var/lib/smallworlds-data`), symlinked to `/mnt/smallworlds-data` | same |
| Kubeconfig label | `production` / `dev` | `local` / `local-<ext>` (`~/.smallworlds/kubeconfigs/local.yaml`) | same |
| Mail (Stalwart) | fully functional (public IP, PTR, port 25) | deploys, but external delivery does not work behind NAT; no DNS automation | mail DNS records automated, but delivery from home connections is unreliable (see below) |
| Golden image | optional fast-boot snapshot | n/a | n/a |

The manifests are untouched by the target choice: `persistent-storage.yaml`
hard-codes `hostPath: /mnt/smallworlds-data/...`, which the bootstrap
satisfies via a symlink to the chosen data directory. The self-signed issuer
is published under the name `letsencrypt-prod`, so the `cluster-issuer`
annotations on all Ingresses work unchanged (the same trick the staging
pipeline uses).

## LAN name resolution (LAN-only mode)

In LAN-only mode nothing manages DNS. Two layers are involved:

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

## TLS (LAN-only mode)

Certificates are self-signed; browsers warn on first visit — expected.
`FULL_OIDC=1` e2e runs are impossible against self-signed certs (same
limitation as ephemeral staging). Choose internet exposure (below) to get
real Let's Encrypt certificates.

Converting an already-installed LAN-only cluster to Let's Encrypt later is
possible but needs three manual steps (cert-manager's webhook forbids
changing an issuer's type in place — "may not specify more than one issuer
type"):

1. Overwrite `/var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml`
   on the server with the ACME variant (see the cloud-init template), then
   `kubectl delete clusterissuer letsencrypt-prod` and re-create it from
   that file.
2. Deploy the DDNS pieces: the `ddns` namespace + `hetzner-dns-token`
   secret, and the `ddns.yaml` manifest the bootstrap generates when
   `MANAGE_DNS=true`.
3. Delete every certificate's TLS secret (`kubectl get certificate -A`
   lists them) **after** the issuer swap — the issuer keeps its name, so
   cert-manager will not re-issue existing self-signed certificates on its
   own.
4. `kubectl -n stalwart rollout restart deploy/stalwart-mail` **after** the
   new certificates are issued: Stalwart caches its OIDC discovery against
   Keycloak, and a cache poisoned by the self-signed era makes it 401 every
   bearer token (webmail login fails) until restarted.

## Internet exposure

Answering **yes** to the wizard's "Expose the apps on the internet?" question
turns the LAN deployment into a publicly reachable one. Prerequisites:

- **A real public IPv4, not CGNAT.** Check: the WAN IP in your router's status
  page must equal what `curl -4 ifconfig.me` reports. Behind CGNAT inbound
  connections are impossible and this mode cannot work (a VPS relay would be
  needed instead).
- **A registered domain** with its nameservers pointed at Hetzner DNS
  (`helium.ns.hetzner.de`, `oxygen.ns.hetzner.com`, `hydrogen.ns.hetzner.com`)
  — same as a Hetzner deployment.
- **A Hetzner API token**, used exclusively for DNS record management (no VM
  is created; the DNS zone is free).
- **Router port forwards** to the server: `80/tcp`, `443/tcp` (HTTP/HTTPS) and
  `10000/udp` (Jitsi media). Nothing else — keep 6443 and SSH LAN-only.

What the installer then does differently:

1. Ensures the DNS zone exists in Hetzner DNS (same code path as the Hetzner
   target).
2. Passes `ACME_EMAIL` to the bootstrap, so the `letsencrypt-prod`
   ClusterIssuer is a real ACME HTTP-01 issuer instead of self-signed.
3. Deploys a **DDNS CronJob** (namespace `ddns`, written by the bootstrap as a
   k3s auto-apply manifest, token via the `hetzner-dns-token` secret): every
   5 minutes it compares the zone's A records (`@`, `identity`, `dashboard`,
   ..., matching the Terraform record set) against the connection's current
   public IP and upserts them at TTL 300. This both creates the records on
   first run and keeps them correct when your home IP changes. Check it with
   `kubectl -n ddns get jobs` / `kubectl -n ddns logs -l job-name=<name>`.

Expected timeline after install: DNS records appear on the CronJob's first
run (≤5 min), Let's Encrypt certificates a few minutes after that; expect
browser certificate warnings until issuance completes.

Caveats:

- **Hairpin NAT**: some routers cannot reach their own public IP from inside
  the LAN. If apps work from mobile data but not from home Wi-Fi, add the
  hosts line from `~/.smallworlds/hosts-local.txt` to your router DNS /
  Pi-hole / device `/etc/hosts` (LAN clients then talk to the laptop
  directly).
- **Mail**: Stalwart's DNS automation now runs (MX/SPF/DKIM/DMARC), but real
  mail delivery from a residential connection remains unreliable — ISPs block
  outbound port 25, the home IP has no PTR record and sits on blocklists.
  Use an outbound SMTP relay, or keep mail on a cloud deployment.
- **Security**: the same internet-facing surface as a Hetzner deployment
  (Traefik + the apps behind Keycloak), so keep the pinned overlay up to date.
  Only forward the three ports listed above; the k8s API and SSH must stay
  LAN-only.

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
