#!/usr/bin/env bash
# SmallWorlds local-server bootstrap.
#
# This is the LAN counterpart of infrastructure/cloud-init/k3s-node.yaml.tpl:
# it performs the same k3s + ArgoCD bootstrap, but on an existing Linux
# machine (e.g. a laptop or mini-PC in your LAN) instead of a cloud-init'd
# Hetzner VM. It is normally invoked by smallworlds-init.sh over SSH, but can
# also be run manually on the target machine as root.
#
# It needs infrastructure/bootstrap-pins.env beside it (or one directory up, as
# in a checkout): the K3s and Argo CD versions it installs, with checksums.
#
# Usage:
#   bootstrap-local-node.sh <config.env>     install and bootstrap the cluster
#   bootstrap-local-node.sh --uninstall      remove k3s (keeps the data dir)
#   bootstrap-local-node.sh --uninstall --purge-data   also delete all data
#
# config.env variables:
#   DOMAIN            root domain for the cluster (e.g. smallworlds.network)
#   ENV_EXT           subdomain-syntax env extension (".dev"); "" for prod
#   ROOT_APP_GIT_URL  overlay repo for the ArgoCD root app; "" = no root app
#   ROOT_APP_GIT_REVISION exact reviewed overlay commit for the ArgoCD root
#                     app. Required whenever ROOT_APP_GIT_URL is set; HEAD
#                     must never change a deployment after approval.
#   ACME_EMAIL        Let's Encrypt account email; "" = self-signed issuer.
#                     Certificates are validated via DNS01 (Hetzner DNS
#                     webhook), so this works from behind NAT with no port
#                     forwarding — but it does need the `hetzner` Secret
#                     (cert-manager namespace, key `token`) to exist, which
#                     comes from the same Hetzner Cloud API token as
#                     stalwart-dns-secrets/hetzner-dns-token, delivered via
#                     SECRETS_MANIFEST.
#   ACME_CHALLENGE    how Let's Encrypt is shown the domain is ours. "dns01"
#                     (default) writes a TXT record through the Hetzner DNS
#                     webhook and needs no inbound connection. "http01" needs
#                     no DNS API at all — Let's Encrypt fetches a file from
#                     port 80 — so it suits a machine on the internet whose DNS
#                     the operator keeps by hand (docs/adr/0056). It cannot
#                     work on a LAN-only cluster.
#   MANAGE_DNS        "true" = deploy a DDNS CronJob that keeps the domain's
#                     A records in Hetzner DNS pointed at this connection's
#                     public IP (for internet-exposed deployments; the token
#                     comes from the hetzner-dns-token secret in the ddns
#                     namespace, delivered via SECRETS_MANIFEST)
#   DATA_DIR          where all persistent data lives (default
#                     /var/lib/smallworlds-data); symlinked to
#                     /mnt/smallworlds-data, which the manifests expect
#   BACKUP_DIR        where every Recovery Point lives (default
#                     /var/lib/smallworlds-backup); symlinked to
#                     /mnt/smallworlds-backup. MUST be on a different block
#                     device than DATA_DIR — see docs/adr/0048. The bootstrap
#                     refuses to continue when they share one.
#   OBJECT_STORAGE    "garage" (default) or "external". External means object
#                     data and Recovery Points live at an S3 provider
#                     (docs/adr/0053): BACKUP_DIR is then not used at all, and
#                     the machine may have a single disk.
#   LIBRARY_DIR       where the Immich photo library lives, when it should not
#                     be inside DATA_DIR — typically a second, larger disk on a
#                     machine whose own disk is small and fast. Empty (default)
#                     keeps it at DATA_DIR/immich-library. Not used by Gallery,
#                     which keeps photos in S3 (docs/adr/0054).
#   HOST_FIREWALL     "true" = this machine sits directly on the internet (a
#                     rented VPS, no router in front), so the bootstrap closes
#                     every inbound port except SSH and the ones below. Never
#                     set it for a machine behind a router: the router already
#                     is that firewall. See docs/adr/0055.
#                     Installs /usr/local/sbin/smallworlds-firewall, whose
#                     `close` and `open` switch SSH and the Kubernetes API on
#                     the public interface once the node is on the tailnet.
#   FIREWALL_TCP_PORTS / FIREWALL_UDP_PORTS
#                     space-separated ports to leave open besides SSH, e.g.
#                     "80 443 6443" and "10000". The installer derives them
#                     from the selected applications.
#   ETCD_DIR          where the cluster datastore lives (default
#                     /var/lib/smallworlds-etcd, on the machine's own disk).
#                     Must be fast storage — see the relocation below.
#   NODE_NAME         stable k3s node name (default smallworlds-local-node)
#   PROFILE_ID        Launcher profile that owns this installation. Required
#                     for safe resume; never use a node already owned by a
#                     different profile.
#   BOOTSTRAP_RUN_ID  Launcher-generated identifier recorded in durable
#                     checkpoints (required).
#   SKIP_FIREWALL_CHECK "true" = do not stop when a firewall this script did
#                     not write would drop traffic from the pod network (set by
#                     SW_SKIP_FIREWALL_CHECK=1 on the installer)
#   SECRETS_MANIFEST  optional path to a pre-generated secrets manifest that
#                     is moved into the k3s auto-apply manifests dir
set -euo pipefail

GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}This script must run as root (it installs k3s and writes to /var/lib).${NC}" >&2
    echo "Re-run with: sudo $0 $*" >&2
    exit 1
fi

# ------------------------------------------------------------------
# Uninstall mode
# ------------------------------------------------------------------
if [ "${1:-}" = "--uninstall" ]; then
    echo -e "${CYAN}Uninstalling SmallWorlds k3s cluster from this machine...${NC}"
    if [ -x /usr/local/bin/k3s-uninstall.sh ]; then
        /usr/local/bin/k3s-uninstall.sh
    else
        echo -e "${YELLOW}k3s-uninstall.sh not found — k3s does not appear to be installed.${NC}"
    fi
    # k3s-uninstall.sh removes /var/lib/rancher/k3s; if it was our symlink the
    # data dir target survives — clean up the dangling link either way.
    [ -L /var/lib/rancher/k3s ] && rm -f /var/lib/rancher/k3s
    # The host firewall (HOST_FIREWALL) is part of the installation, so it goes
    # with it; a machine that never had one is unaffected.
    if [ -f /etc/systemd/system/smallworlds-firewall.service ]; then
        systemctl disable --now smallworlds-firewall.service >/dev/null 2>&1 || true
        nft delete table inet smallworlds >/dev/null 2>&1 || true
        rm -f /etc/systemd/system/smallworlds-firewall.service /etc/smallworlds/firewall.nft \
              /etc/smallworlds/firewall.conf /usr/local/sbin/smallworlds-firewall
        systemctl daemon-reload >/dev/null 2>&1 || true
        echo -e "${GREEN}Host firewall removed.${NC}"
    fi
    if [ "${2:-}" = "--purge-data" ]; then
        for d in /mnt/smallworlds-data /var/lib/smallworlds-data; do
            if [ -e "$d" ]; then
                echo -e "${RED}Deleting $d (all user data)...${NC}"
                # resolve the symlink target first so the real data goes too
                real=$(readlink -f "$d")
                rm -rf "$real" "$d"
            fi
        done
    else
        echo -e "${GREEN}Data directory kept (pass --purge-data to delete it).${NC}"
    fi
    echo -e "${GREEN}Uninstall complete.${NC}"
    exit 0
fi

# ------------------------------------------------------------------
# Load configuration
# ------------------------------------------------------------------
CONFIG_FILE="${1:-}"
if [ -z "$CONFIG_FILE" ] || [ ! -f "$CONFIG_FILE" ]; then
    echo -e "${RED}Usage: $0 <config.env> | --uninstall [--purge-data]${NC}" >&2
    exit 1
fi
# shellcheck disable=SC1090
source "$CONFIG_FILE"

DOMAIN="${DOMAIN:?DOMAIN must be set in $CONFIG_FILE}"
ENV_EXT="${ENV_EXT:-}"
ROOT_APP_GIT_URL="${ROOT_APP_GIT_URL:-}"
ROOT_APP_GIT_REVISION="${ROOT_APP_GIT_REVISION:-}"
ACME_EMAIL="${ACME_EMAIL:-}"
ACME_CHALLENGE="${ACME_CHALLENGE:-dns01}"
MANAGE_DNS="${MANAGE_DNS:-}"
DATA_DIR="${DATA_DIR:-/var/lib/smallworlds-data}"
BACKUP_DIR="${BACKUP_DIR:-/var/lib/smallworlds-backup}"
OBJECT_STORAGE="${OBJECT_STORAGE:-garage}"
LIBRARY_DIR="${LIBRARY_DIR:-}"
HOST_FIREWALL="${HOST_FIREWALL:-}"
FIREWALL_TCP_PORTS="${FIREWALL_TCP_PORTS:-80 443 6443}"
FIREWALL_UDP_PORTS="${FIREWALL_UDP_PORTS:-}"
ETCD_DIR="${ETCD_DIR:-/var/lib/smallworlds-etcd}"
NODE_NAME="${NODE_NAME:-smallworlds-local-node}"
PROFILE_ID="${PROFILE_ID:?PROFILE_ID must be set in $CONFIG_FILE}"
BOOTSTRAP_RUN_ID="${BOOTSTRAP_RUN_ID:?BOOTSTRAP_RUN_ID must be set in $CONFIG_FILE}"
SECRETS_MANIFEST="${SECRETS_MANIFEST:-}"

# What gets installed is pinned, not "whatever upstream serves today": the K3s
# version, the K3s install script and the Argo CD manifest come from
# bootstrap-pins.env, which travels with this script, and each download is
# checked against its SHA-256 before it is used (docs/adr/0057).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PINS_FILE="${SMALLWORLDS_BOOTSTRAP_PINS:-}"
if [ -z "$PINS_FILE" ]; then
    # Next to the script when it was copied to a node, one level up in a checkout.
    for candidate in "$SCRIPT_DIR/bootstrap-pins.env" "$SCRIPT_DIR/../bootstrap-pins.env"; do
        if [ -r "$candidate" ]; then PINS_FILE="$candidate"; break; fi
    done
fi
if [ -z "$PINS_FILE" ] || [ ! -r "$PINS_FILE" ]; then
    echo -e "${RED}bootstrap-pins.env not found next to $0.${NC}" >&2
    echo "Copy infrastructure/bootstrap-pins.env from the SmallWorlds checkout alongside this script." >&2
    exit 1
fi
# shellcheck disable=SC1090
source "$PINS_FILE"
for pin in K3S_VERSION K3S_INSTALLER_URL K3S_INSTALLER_SHA256 ARGOCD_MANIFEST_URL ARGOCD_MANIFEST_SHA256; do
    if [ -z "${!pin:-}" ]; then
        echo -e "${RED}$PINS_FILE does not set $pin.${NC}" >&2
        exit 1
    fi
done
if ! [[ "$K3S_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._+~-]*$ ]]; then
    echo -e "${RED}$PINS_FILE has an invalid K3S_VERSION.${NC}" >&2
    exit 1
fi

# fetch_verified <url> <sha256> <destination>: download, and keep the file only
# if it is byte for byte what was pinned.
fetch_verified() {
    local url="$1" expected="$2" destination="$3" actual
    if ! curl -fsSL --retry 3 --max-time 120 -o "$destination" "$url"; then
        echo -e "${RED}Could not download $url.${NC}" >&2
        exit 1
    fi
    actual="$(sha256sum "$destination" | cut -d' ' -f1)"
    if [ "$actual" != "$expected" ]; then
        rm -f "$destination"
        echo -e "${RED}Checksum mismatch for $url${NC}" >&2
        echo "  expected $expected" >&2
        echo "  got      $actual" >&2
        echo "The file is not the one that was reviewed, so nothing was installed. If upstream" >&2
        echo "moved it, update the pin in infrastructure/bootstrap-pins.env." >&2
        exit 1
    fi
}

MARKER_DIR=/etc/smallworlds
EXISTING_PROFILE_ID=""
if [ -f "$MARKER_DIR/profile-id" ]; then
    EXISTING_PROFILE_ID="$(cat "$MARKER_DIR/profile-id")"
fi
if [ -n "$EXISTING_PROFILE_ID" ] && [ "$EXISTING_PROFILE_ID" != "$PROFILE_ID" ]; then
    echo -e "${RED}This node belongs to a different SmallWorlds profile and will not be adopted.${NC}" >&2
    exit 1
fi

# ------------------------------------------------------------------
# Preflight checks
# ------------------------------------------------------------------
if { command -v k3s >/dev/null 2>&1 || systemctl is-active --quiet k3s 2>/dev/null; } && { [ "$EXISTING_PROFILE_ID" != "$PROFILE_ID" ] || [ ! -f "$MARKER_DIR/k3s-ready" ]; }; then
    echo -e "${RED}k3s is already installed on this machine.${NC}" >&2
    echo "A stale cluster must never be adopted silently. If this machine previously" >&2
    echo "ran SmallWorlds (or anything else on k3s), remove it first:" >&2
    echo "    sudo $0 --uninstall" >&2
    exit 1
fi

# A node is only claimed after the foreign-installation guard above. Markers
# are deliberately small, durable evidence used by the Launcher to resume at
# safe boundaries after an SSH or process interruption.
#
# Readable on purpose. The Launcher inspects this node over SSH as an ordinary
# user and never elevates merely to look — that is what lets it promise it
# changes nothing while inspecting. A root-only directory made exactly the
# evidence it needs invisible: its own node then read as somebody else's
# ("installation.data.foreign") and no interruption could ever be observed, so
# nothing was ever resumable. Nothing secret lives here — the run's Cluster
# Secrets stay under /var/lib/smallworlds-launcher with umask 077, and the
# issuer manifest only names a Secret rather than containing one.
mkdir -p "$MARKER_DIR"
chmod 755 "$MARKER_DIR"
printf '%s\n' "$PROFILE_ID" > "$MARKER_DIR/profile-id"
printf '%s\n' "$BOOTSTRAP_RUN_ID" > "$MARKER_DIR/bootstrap-run-id"
chmod 644 "$MARKER_DIR/profile-id" "$MARKER_DIR/bootstrap-run-id"
touch "$MARKER_DIR/bootstrap-started"
on_bootstrap_failure() {
    touch "$MARKER_DIR/bootstrap-interrupted"
}
# HUP belongs here: the bootstrap runs on the Launcher's SSH session, so a
# dropped connection is the most likely interruption of all, and without it the
# one signal that actually arrives left no evidence behind.
trap on_bootstrap_failure ERR INT TERM HUP

MEM_GB=$(awk '/MemTotal/ {printf "%d", $2/1024/1024}' /proc/meminfo)
if [ "$MEM_GB" -lt 16 ]; then
    echo -e "${YELLOW}Warning: only ${MEM_GB} GB RAM detected. The full app suite needs ~16 GB; 32 GB is recommended.${NC}"
fi

FREE_GB=$(df -BG --output=avail "$(dirname "$DATA_DIR")" 2>/dev/null | tail -1 | tr -dc '0-9' || echo 0)
if [ "${FREE_GB:-0}" -lt 100 ]; then
    echo -e "${YELLOW}Warning: only ${FREE_GB} GB free where $DATA_DIR will live. Garage + Immich + databases want 100 GB+.${NC}"
fi

if systemctl is-active --quiet firewalld 2>/dev/null; then
    echo -e "${YELLOW}firewalld is active. k3s and firewalld conflict (pod/service traffic gets dropped).${NC}"
    echo -e "${YELLOW}Recommended: 'sudo systemctl disable --now firewalld', or follow${NC}"
    echo -e "${YELLOW}https://docs.k3s.io/installation/requirements to add the k3s rules, then re-run.${NC}"
fi
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    echo -e "${YELLOW}ufw is active. Make sure 80/tcp, 443/tcp, 6443/tcp and 10000/udp are allowed,${NC}"
    echo -e "${YELLOW}and that pod/service CIDRs are permitted (see https://docs.k3s.io/installation/requirements).${NC}"
fi

# A firewall this script did not write, whose input chain drops by default and
# lets nothing in from the pod network, is fatal rather than a warning: pods
# reach the Kubernetes API on this machine's own address, so the node comes up
# Ready while every controller times out, Argo CD never starts, and the run ends
# 15 minutes later waiting for cert-manager. That is how it was first met, on a
# server whose /etc/nftables.conf admitted only its web and SSH ports.
#
# foreign_input_firewalls reads `nft list ruleset` on stdin and prints
# "<family> <table> <chain>" for each such chain.
foreign_input_firewalls() {
    awk '
        $1 == "table" && $NF == "{" { family = $2; table = $3; next }
        $1 == "chain" && $NF == "{" { chain = $2; body = ""; inside = 1; next }
        inside && $1 == "}" {
            if (body ~ /hook input/ && body ~ /policy drop/ && table != "smallworlds" \
                && body !~ /cni0|10\.42\.0\.0\/16/)
                print family, table, chain
            inside = 0; next
        }
        inside { body = body "\n" $0 }
    '
}
if [ "${SKIP_FIREWALL_CHECK:-}" != "true" ]; then
    BLOCKING_CHAINS=""
    if command -v nft >/dev/null 2>&1; then
        BLOCKING_CHAINS="$(nft list ruleset 2>/dev/null | foreign_input_firewalls || true)"
    elif command -v iptables >/dev/null 2>&1; then
        INPUT_RULES="$(iptables -S INPUT 2>/dev/null || true)"
        if printf '%s\n' "$INPUT_RULES" | grep -q '^-P INPUT DROP' \
           && ! printf '%s\n' "$INPUT_RULES" | grep -qE 'cni0|10\.42\.0\.0/16'; then
            BLOCKING_CHAINS="ip filter INPUT"
        fi
    fi
    if [ -n "$BLOCKING_CHAINS" ]; then
        echo -e "${RED}This machine's own firewall would block the cluster's internal traffic.${NC}" >&2
        echo "These input chains drop by default and admit nothing from the pod network:" >&2
        printf '%s\n' "$BLOCKING_CHAINS" | while read -r family table chain; do
            echo "    table $family $table, chain $chain" >&2
        done
        echo "Pods reach the Kubernetes API on this machine's own address, so nothing in the" >&2
        echo "cluster would start. Add this rule to each chain, and to the file that loads it at" >&2
        echo "boot (usually /etc/nftables.conf), then run the install again:" >&2
        echo >&2
        echo '    iifname { "cni0", "flannel.1" } accept' >&2
        echo >&2
        echo "To try it at once:  nft add rule <family> <table> <chain> 'iifname { \"cni0\", \"flannel.1\" } accept'" >&2
        echo "The same chains must also admit what should be reachable from outside:" >&2
        echo "    tcp ${FIREWALL_TCP_PORTS// /, }${FIREWALL_UDP_PORTS:+   udp ${FIREWALL_UDP_PORTS// /, }}" >&2
        echo "This run has installed nothing. (SW_SKIP_FIREWALL_CHECK=1 skips this check.)" >&2
        exit 1
    fi
fi

for tool in curl sha256sum; do
    command -v "$tool" >/dev/null 2>&1 || { echo -e "${RED}Missing required tool: $tool${NC}" >&2; exit 1; }
done

# Both downloads happen here, before the firewall, the disks or k3s are touched,
# so a pin that no longer matches stops the bootstrap with nothing installed.
FETCH_DIR="$(mktemp -d)"
trap 'rm -rf "$FETCH_DIR"' EXIT
K3S_INSTALLER="$FETCH_DIR/k3s-install.sh"
ARGOCD_MANIFEST="$FETCH_DIR/argocd-install.yaml"
echo -e "${CYAN}Fetching the pinned K3s installer and Argo CD manifest...${NC}"
fetch_verified "$K3S_INSTALLER_URL" "$K3S_INSTALLER_SHA256" "$K3S_INSTALLER"
fetch_verified "$ARGOCD_MANIFEST_URL" "$ARGOCD_MANIFEST_SHA256" "$ARGOCD_MANIFEST"

# Primary LAN IP: the address this machine uses to reach the outside world.
NODE_IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1)
[ -z "$NODE_IP" ] && NODE_IP=$(hostname -I | awk '{print $1}')
if [ -z "$NODE_IP" ]; then
    echo -e "${RED}Could not determine this machine's LAN IP address.${NC}" >&2
    exit 1
fi
echo -e "${CYAN}Bootstrapping SmallWorlds on this machine (node IP: ${NODE_IP}, data dir: ${DATA_DIR})...${NC}"

# ------------------------------------------------------------------
# 0. Host firewall — only for a machine directly on the internet
#
# The Hetzner target gets a cloud firewall from Terraform; a VPS rented
# anywhere else has nothing in front of it, and k3s then publishes the kubelet,
# every NodePort and whatever a hostNetwork pod binds to the whole internet.
# This is the same allow-list, enforced on the machine itself (docs/adr/0055).
#
# The rules sit in a prerouting chain that runs before destination NAT, and
# look only at packets arriving on the public interface. That is what makes it
# behave like a firewall in front of the machine: a port is judged by the
# number the outside world dialled, before kube-proxy or the CNI rewrites it,
# so NodePorts are closed too — an input-hook filter would never see them.
# Traffic between pods, and anything the machine itself started, is untouched.
# ------------------------------------------------------------------
if [ "$HOST_FIREWALL" = "true" ]; then
    PUBLIC_IF=$(ip -o route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="dev") print $(i+1)}' | head -1)
    if ! command -v nft >/dev/null 2>&1; then
        echo -e "${RED}HOST_FIREWALL is set but nft is not installed.${NC}" >&2
        echo "Install the nftables package (apt install nftables / dnf install nftables) and re-run." >&2
        echo "Continuing without a firewall would leave the Kubernetes ports open to the internet." >&2
        exit 1
    fi
    if [ -z "$PUBLIC_IF" ]; then
        echo -e "${RED}Could not determine the public network interface for the firewall.${NC}" >&2
        exit 1
    fi

    # Every port sshd might be answering on. Getting this wrong locks the
    # operator out of a machine they can only reach over SSH, so it is taken
    # from three independent places and 22 is kept regardless.
    SSH_PORTS="22"
    SSH_PORTS="$SSH_PORTS $(sshd -T 2>/dev/null | awk '$1=="port"{print $2}' | tr '\n' ' ')"
    SSH_PORTS="$SSH_PORTS $(printf '%s' "${SSH_CONNECTION:-}" | awk '{print $4}')"
    SSH_PORTS="$SSH_PORTS $(ss -H -tlnp 2>/dev/null | awk '/"sshd"/{n=split($4,a,":"); print a[n]}' | tr '\n' ' ')"
    SSH_PORTS=$(printf '%s\n' $SSH_PORTS | grep -E '^[0-9]{1,5}$' | sort -un | tr '\n' ' ')

    # The administrative ports — SSH and the Kubernetes API — are a switch of
    # their own, because they are the only ones that can be closed once the
    # node is on the tailnet (`smallworlds-firewall close`). 6443 is therefore
    # taken out of the application list and tracked with SSH instead.
    APP_TCP_PORTS=$(printf '%s\n' $FIREWALL_TCP_PORTS | grep -vx 6443 | tr '\n' ' ')

    # A re-run of the installer must not reopen what the operator closed on
    # purpose: the installer may well be arriving over the tailnet by then.
    ADMIN_PORTS=open
    if [ -f /etc/smallworlds/firewall.conf ]; then
        ADMIN_PORTS=$(sed -n 's/^ADMIN_PORTS=//p' /etc/smallworlds/firewall.conf | tr -d '"' | head -1)
        [ "$ADMIN_PORTS" = "closed" ] || ADMIN_PORTS=open
    fi

    mkdir -p /etc/smallworlds
    cat > /etc/smallworlds/firewall.conf <<CONF
# Written by bootstrap-local-node.sh (HOST_FIREWALL). Re-running the installer
# rewrites everything here except ADMIN_PORTS, which belongs to
# \`smallworlds-firewall open|close\`.
PUBLIC_IF="$PUBLIC_IF"
SSH_PORTS="$SSH_PORTS"
API_PORT="6443"
TCP_PORTS="$APP_TCP_PORTS"
UDP_PORTS="$FIREWALL_UDP_PORTS"
ADMIN_PORTS="$ADMIN_PORTS"
CONF

    cat > /usr/local/sbin/smallworlds-firewall <<'TOOL'
#!/bin/sh
# SmallWorlds host firewall — installed by bootstrap-local-node.sh.
#
#   smallworlds-firewall status   what is open
#   smallworlds-firewall apply    load the rules (run at boot)
#   smallworlds-firewall close    stop answering SSH and the Kubernetes API on
#                                 the public interface; both stay reachable
#                                 over the tailnet
#   smallworlds-firewall open     answer them on the public interface again
#
# Locked out? The tailnet path is unaffected by these rules. Failing that, use
# your provider's web console and run `smallworlds-firewall open` there.
set -eu

CONF=/etc/smallworlds/firewall.conf
RULES=/etc/smallworlds/firewall.nft
TAILNET_IF="${SMALLWORLDS_TAILNET_IF:-tailscale0}"
[ -r "$CONF" ] || { echo "smallworlds-firewall: $CONF is missing" >&2; exit 1; }
. "$CONF"

port_set() {
    # Space-separated words -> "a, b, c": numbers only, de-duplicated.
    printf '%s\n' $1 | grep -E '^[0-9]{1,5}$' | sort -un | paste -sd, - | sed 's/,/, /g'
}

render() {
    tcp="$TCP_PORTS"
    [ "$ADMIN_PORTS" = "closed" ] || tcp="$tcp $SSH_PORTS $API_PORT"
    tcp_set=$(port_set "$tcp")
    udp_set=$(port_set "$UDP_PORTS")
    {
        echo "# Generated by smallworlds-firewall from $CONF — do not edit."
        echo "table inet smallworlds"
        echo "delete table inet smallworlds"
        echo "table inet smallworlds {"
        echo "    chain perimeter {"
        echo "        type filter hook prerouting priority -150; policy accept;"
        echo "        iifname != \"$PUBLIC_IF\" accept"
        echo "        ct state established,related accept"
        echo "        meta l4proto { icmp, ipv6-icmp } accept"
        echo "        udp dport 68 accept"
        [ -n "$tcp_set" ] && echo "        tcp dport { $tcp_set } accept"
        [ -n "$udp_set" ] && echo "        udp dport { $udp_set } accept"
        echo "        counter drop"
        echo "    }"
        echo "}"
    } > "$RULES.new"
    # Checked before it replaces anything: a file nft rejects must never
    # become the one loaded at boot.
    if ! nft -c -f "$RULES.new"; then
        rm -f "$RULES.new"
        echo "smallworlds-firewall: nft rejected the generated rules; nothing was changed." >&2
        exit 1
    fi
    mv "$RULES.new" "$RULES"
}

load() {
    render
    nft -f "$RULES"
}

set_admin() {
    sed -i "s/^ADMIN_PORTS=.*/ADMIN_PORTS=\"$1\"/" "$CONF"
    ADMIN_PORTS="$1"
}

# Is whoever is running this logged in over the tailnet? Headscale and
# Tailscale hand out addresses from 100.64.0.0/10 and fd7a:115c:a1e0::/48.
over_tailnet() {
    origin="$(printf '%s' "${SSH_CONNECTION:-}" | awk '{print $1}')"
    [ -n "$origin" ] || origin="$(who -m 2>/dev/null | sed -n 's/.*(\(.*\)).*/\1/p')"
    case "$origin" in
        fd7a:115c:a1e0:*) return 0 ;;
        100.*)
            second=$(printf '%s' "$origin" | cut -d. -f2)
            [ "$second" -ge 64 ] && [ "$second" -le 127 ] && return 0 ;;
    esac
    return 1
}

case "${1:-status}" in
    apply)
        load
        ;;
    close)
        if [ "${2:-}" != "--force" ]; then
            if ! ip link show "$TAILNET_IF" >/dev/null 2>&1; then
                echo "Refusing: this machine is not on the tailnet ($TAILNET_IF does not exist)." >&2
                echo "Closing SSH now would leave no way in. Join it first (admin-tools/setup-vpn.sh)." >&2
                exit 1
            fi
            if ! over_tailnet; then
                echo "Refusing: you are not logged in over the tailnet." >&2
                echo "Log in through the tailnet address and run this again from there — that" >&2
                echo "session is the proof that the way in still works once the public one is gone." >&2
                echo "(--force skips this check; the provider's web console is then your way back.)" >&2
                exit 1
            fi
        fi
        set_admin closed
        load
        echo "Closed on $PUBLIC_IF: SSH ($SSH_PORTS) and the Kubernetes API ($API_PORT). Both remain reachable over the tailnet."
        echo "To undo: smallworlds-firewall open"
        ;;
    open)
        set_admin open
        load
        echo "Open on $PUBLIC_IF again: SSH ($SSH_PORTS) and the Kubernetes API ($API_PORT)."
        ;;
    status)
        echo "public interface:      $PUBLIC_IF"
        echo "administrative ports:  $ADMIN_PORTS  (ssh $SSH_PORTS, kubernetes api $API_PORT)"
        echo "application ports:     tcp ${TCP_PORTS:-none}, udp ${UDP_PORTS:-none}"
        if nft list table inet smallworlds >/dev/null 2>&1; then
            echo "rules:                 loaded"
        else
            echo "rules:                 NOT loaded — run: smallworlds-firewall apply"
        fi
        ;;
    *)
        echo "usage: smallworlds-firewall status|apply|close [--force]|open" >&2
        exit 2
        ;;
esac
TOOL
    chmod 755 /usr/local/sbin/smallworlds-firewall

    cat > /etc/systemd/system/smallworlds-firewall.service <<'UNIT'
[Unit]
Description=SmallWorlds host firewall (inbound allow-list)
Wants=network-pre.target
Before=network-pre.target k3s.service
DefaultDependencies=no
After=systemd-modules-load.service local-fs.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/smallworlds-firewall apply
ExecStop=/usr/bin/env nft delete table inet smallworlds

[Install]
WantedBy=multi-user.target
UNIT
    systemctl daemon-reload
    systemctl enable smallworlds-firewall.service >/dev/null 2>&1
    /usr/local/sbin/smallworlds-firewall apply
    echo -e "${GREEN}Host firewall active:${NC}"
    /usr/local/sbin/smallworlds-firewall status | sed 's/^/    /'
fi

# ------------------------------------------------------------------
# 1. Kernel limits (same values as the cloud-init template)
# ------------------------------------------------------------------
cat > /etc/sysctl.d/99-kubernetes-cri.conf <<'SYSCTL'
fs.inotify.max_user_instances=8192
fs.inotify.max_user_watches=524288
SYSCTL
sysctl --system >/dev/null

# ------------------------------------------------------------------
# 2. Data directories — manifests hard-code /mnt/smallworlds-data
#    (see infrastructure/kubernetes/apps/persistent-storage.yaml), so the
#    configurable DATA_DIR is exposed there via a symlink.
# ------------------------------------------------------------------
mkdir -p "$DATA_DIR/k3s"
LIBRARY_LINK="$DATA_DIR/immich-library"
if [ -n "$LIBRARY_DIR" ] && [ "$LIBRARY_DIR" != "$LIBRARY_LINK" ]; then
    # The photo library is the one thing that outgrows a small fast disk, so it
    # may live on another one. The manifests keep their fixed path; this link
    # is what points it elsewhere. An existing library is never moved for the
    # operator: a directory that already holds photos stops the install.
    mkdir -p "$LIBRARY_DIR"
    if [ -d "$LIBRARY_LINK" ] && [ ! -L "$LIBRARY_LINK" ]; then
        if [ -n "$(ls -A "$LIBRARY_LINK" 2>/dev/null)" ]; then
            echo -e "${RED}$LIBRARY_LINK already holds a photo library.${NC}" >&2
            echo "Move its contents to $LIBRARY_DIR yourself and remove the empty" >&2
            echo "directory, or leave LIBRARY_DIR unset to keep the library where it is." >&2
            exit 1
        fi
        rmdir "$LIBRARY_LINK"
    fi
    ln -sfn "$LIBRARY_DIR" "$LIBRARY_LINK"
    echo -e "${CYAN}Photo library: $LIBRARY_DIR (linked from $LIBRARY_LINK).${NC}"
else
    mkdir -p "$LIBRARY_LINK"
fi
if [ "$DATA_DIR" != "/mnt/smallworlds-data" ]; then
    ln -sfn "$DATA_DIR" /mnt/smallworlds-data
fi

if [ "$OBJECT_STORAGE" = "external" ]; then
    # Object data and every Recovery Point live at an S3 provider
    # (docs/adr/0053): there is no Garage to give a directory to, and nothing
    # is kept on this machine that a second disk would have to protect. The
    # two-disk rule below exists to keep backups off the operational disk; a
    # provider is further from it than a second disk here could be.
    echo -e "${CYAN}Object storage is external — no Garage directories, no backup disk.${NC}"
else
    mkdir -p "$DATA_DIR/garage"

    # Every Recovery Point lives here, and the whole point is that it survives the
    # loss of DATA_DIR (docs/adr/0048). Both the garage-backup data and metadata
    # directories go on it: the LMDB index is what resolves a bucket key to
    # content-addressed blocks, so leaving meta behind on the operational disk
    # would make the surviving blocks unreadable.
    mkdir -p "$BACKUP_DIR/garage-backup/data" "$BACKUP_DIR/garage-backup/meta"
    if [ "$BACKUP_DIR" != "/mnt/smallworlds-backup" ]; then
        ln -sfn "$BACKUP_DIR" /mnt/smallworlds-backup
    fi

    # A co-located backup directory looks identical to a separated one right up
    # until the disk it shares dies. Refuse rather than pretend.
    DATA_DEVICE=$(df --output=source "$DATA_DIR" 2>/dev/null | tail -1)
    BACKUP_DEVICE=$(df --output=source "$BACKUP_DIR" 2>/dev/null | tail -1)
    if [ "$DATA_DEVICE" = "$BACKUP_DEVICE" ]; then
        echo -e "${RED}DATA_DIR and BACKUP_DIR are both on $DATA_DEVICE.${NC}" >&2
        echo "Backups must survive the loss of the operational disk, so they may not" >&2
        echo "share one. Point BACKUP_DIR at a second disk and re-run:" >&2
        echo "    BACKUP_DIR=/mnt/<second-disk>/smallworlds-backup" >&2
        exit 1
    fi
fi

# Relocate k3s state onto the data dir so a k3s reinstall/upgrade never
# separates cluster state from user data.
mkdir -p /var/lib/rancher
if [ -d /var/lib/rancher/k3s ] && [ ! -L /var/lib/rancher/k3s ]; then
    cp -a /var/lib/rancher/k3s/. "$DATA_DIR/k3s/"
    rm -rf /var/lib/rancher/k3s
fi
ln -sfn "$DATA_DIR/k3s" /var/lib/rancher/k3s

# etcd is the one thing here that must not live on a slow disk. It fsyncs every
# write, and when the disk cannot keep up it fails to renew its leader lease;
# k3s then exits and systemd restarts it. On a real installation that meant a
# control plane dying every four minutes, pods stuck in Unknown, and Argo CD
# waiting on hook jobs that no longer existed — with nothing in the cluster
# itself looking wrong. The bulk data stays on the data disk where it belongs;
# only the datastore moves to the machine's own storage.
mkdir -p "$ETCD_DIR"
ETCD_LINK="$DATA_DIR/k3s/server/db"
mkdir -p "$DATA_DIR/k3s/server"
if [ -d "$ETCD_LINK" ] && [ ! -L "$ETCD_LINK" ]; then
    if systemctl is-active --quiet k3s 2>/dev/null; then
        echo -e "${YELLOW}The cluster datastore is still on $DATA_DIR and k3s is running; it is left alone.${NC}" >&2
        echo "Moving it means stopping the cluster. To do it by hand:" >&2
        echo "    systemctl stop k3s" >&2
        echo "    mv $ETCD_LINK/* $ETCD_DIR/ && rmdir $ETCD_LINK && ln -s $ETCD_DIR $ETCD_LINK" >&2
        echo "    systemctl start k3s" >&2
    else
        cp -a "$ETCD_LINK/." "$ETCD_DIR/"
        rm -rf "$ETCD_LINK"
        ln -sfn "$ETCD_DIR" "$ETCD_LINK"
    fi
else
    ln -sfn "$ETCD_DIR" "$ETCD_LINK"
fi

# Reported, not enforced: virtualized disks routinely claim to be rotational
# when they are not, and refusing on that would turn a false reading into a
# failed installation. A true reading, though, predicts exactly the failure
# above.
ETCD_SOURCE_DEVICE=$(df --output=source "$ETCD_DIR" 2>/dev/null | tail -1)
ETCD_DISK=$(lsblk -no pkname "$ETCD_SOURCE_DEVICE" 2>/dev/null | head -1)
[ -n "$ETCD_DISK" ] || ETCD_DISK=$(basename "${ETCD_SOURCE_DEVICE:-none}")
if [ "$(cat "/sys/block/$ETCD_DISK/queue/rotational" 2>/dev/null || echo 0)" = "1" ]; then
    echo -e "${YELLOW}Warning: $ETCD_DIR sits on $ETCD_DISK, which reports itself as a rotating disk.${NC}"
    echo -e "${YELLOW}etcd needs low write latency; on a spinning disk the control plane loses its"
    echo -e "leader lease and k3s restarts in a loop. Point ETCD_DIR at an SSD or NVMe.${NC}"
fi
touch "$MARKER_DIR/data-ready"

# ------------------------------------------------------------------
# 3. Bootstrap manifests (auto-applied by k3s on startup)
# ------------------------------------------------------------------
mkdir -p /var/lib/rancher/k3s/server/manifests

# The ClusterIssuer is deliberately NOT written into server/manifests. k3s
# auto-applies that directory and retries forever on failure, and a ClusterIssuer
# cannot apply until cert-manager's CRDs exist — which happens much later, once
# Argo CD has synced. The result was a permanent apply-fail-retry loop writing
# events into etcd every few seconds. It is applied once, after the CRD is there.
if [ -n "$ACME_EMAIL" ] && [ "$ACME_CHALLENGE" = "http01" ]; then
    # Same issuer name, different proof: cert-manager answers the challenge
    # through a short-lived Ingress of its own. Traefik redirects port 80 to
    # 443 for every host, which Let's Encrypt follows without judging the
    # certificate it finds there — the one it is about to replace.
    cat > "$MARKER_DIR/letsencrypt-prod.yaml" <<ISSUER
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: "${ACME_EMAIL}"
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - http01:
        ingress:
          ingressClassName: traefik
ISSUER
elif [ -n "$ACME_EMAIL" ]; then
    cat > "$MARKER_DIR/letsencrypt-prod.yaml" <<ISSUER
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: "${ACME_EMAIL}"
    privateKeySecretRef:
      name: letsencrypt-prod
    solvers:
    - dns01:
        webhook:
          groupName: acme.hetzner.com
          solverName: hetzner
          config:
            tokenSecretKeyRef:
              name: hetzner
              key: token
ISSUER
else
    # Self-signed issuer published under the production name so the
    # cluster-issuer annotations on the Ingresses work unchanged
    cat > "$MARKER_DIR/letsencrypt-prod.yaml" <<'ISSUER'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  selfSigned: {}
ISSUER
fi

# In-cluster DNS override: pods must resolve the app domains to this node,
# not to whatever public DNS says (usually nothing, for a LAN install).
cat > /var/lib/rancher/k3s/server/manifests/coredns-custom.yaml <<COREDNS
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-custom
  namespace: kube-system
data:
  smallworlds.server: |
    ${DOMAIN}:53 {
      hosts {
        $NODE_IP identity${ENV_EXT}.${DOMAIN} files${ENV_EXT}.${DOMAIN} photos${ENV_EXT}.${DOMAIN} git${ENV_EXT}.${DOMAIN} mail${ENV_EXT}.${DOMAIN} meet${ENV_EXT}.${DOMAIN} webmail${ENV_EXT}.${DOMAIN} whiteboard${ENV_EXT}.${DOMAIN} office${ENV_EXT}.${DOMAIN} dashboard${ENV_EXT}.${DOMAIN} monitoring${ENV_EXT}.${DOMAIN} pod${ENV_EXT}.${DOMAIN}
        fallthrough
      }
      forward . /etc/resolv.conf
    }
COREDNS

# Dynamic DNS for internet-exposed deployments: a CronJob keeps the zone's
# A records pointed at this connection's public IP (home IPs change). The
# rrset upsert pattern (list -> delete stale -> POST) mirrors the Stalwart
# init job, the only other Hetzner DNS client in this repo.
if [ "$MANAGE_DNS" = "true" ]; then
    RECORD_NAMES=""
    if [ -z "$ENV_EXT" ]; then RECORD_NAMES="@"; fi
    for sub in identity dashboard files photos git mail webmail monitoring whiteboard meet office plan deploy pod vpn; do
        RECORD_NAMES="${RECORD_NAMES:+$RECORD_NAMES }${sub}${ENV_EXT}"
    done

    cat > /var/lib/rancher/k3s/server/manifests/ddns.yaml <<DDNSHEAD
apiVersion: v1
kind: ConfigMap
metadata:
  name: ddns-script
  namespace: ddns
data:
  ddns.sh: |
DDNSHEAD
    cat >> /var/lib/rancher/k3s/server/manifests/ddns.yaml <<'DDNSSCRIPT'
    #!/bin/sh
    # Upserts an A record for every name in $RECORD_NAMES to the current
    # public IPv4. Env: HCLOUD_TOKEN, DOMAIN, RECORD_NAMES.
    set -eu
    API="https://api.hetzner.cloud/v1"
    AUTH="Authorization: Bearer $HCLOUD_TOKEN"

    IP=$(curl -4 -sf --max-time 10 https://api.ipify.org || curl -4 -sf --max-time 10 https://ifconfig.me)
    case "$IP" in
      *[!0-9.]*|"") echo "Could not determine public IPv4 (got: '$IP')" >&2; exit 1;;
    esac

    ZONE_ID=$(curl -sf -H "$AUTH" "$API/zones" | jq -r --arg d "$DOMAIN" '.zones[] | select(.name==$d) | .id')
    if [ -z "$ZONE_ID" ] || [ "$ZONE_ID" = "null" ]; then
      echo "Zone $DOMAIN not found in Hetzner DNS" >&2; exit 1
    fi

    # All existing A records, as "name=value" lines (paginated)
    EXISTING=$(
      page=1
      while [ -n "$page" ] && [ "$page" != "null" ]; do
        resp=$(curl -sf -H "$AUTH" "$API/zones/$ZONE_ID/rrsets?per_page=50&page=$page")
        echo "$resp" | jq -r '.rrsets[] | select(.type=="A") | "\(.name)=\(.records[0].value)"'
        page=$(echo "$resp" | jq -r '.meta.pagination.next_page')
      done
    )

    for name in $RECORD_NAMES; do
      current=$(printf '%s\n' "$EXISTING" | sed -n "s/^$name=//p" | head -1)
      if [ "$current" = "$IP" ]; then
        continue
      fi
      if [ -n "$current" ]; then
        echo "Updating $name: $current -> $IP"
        encoded=$(printf '%s' "$name" | sed 's/@/%40/')
        curl -sf -X DELETE -H "$AUTH" "$API/zones/$ZONE_ID/rrsets/$encoded/A" >/dev/null || true
      else
        echo "Creating $name -> $IP"
      fi
      curl -sf -X POST -H "$AUTH" -H "Content-Type: application/json" \
        -d "{\"name\":\"$name\",\"type\":\"A\",\"ttl\":300,\"records\":[{\"value\":\"$IP\"}]}" \
        "$API/zones/$ZONE_ID/rrsets" >/dev/null
    done
    echo "DDNS check complete (public IP: $IP)"
DDNSSCRIPT
    cat >> /var/lib/rancher/k3s/server/manifests/ddns.yaml <<DDNSTAIL
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: ddns
  namespace: ddns
spec:
  schedule: "*/5 * * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 1
  failedJobsHistoryLimit: 2
  jobTemplate:
    spec:
      backoffLimit: 2
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: ddns
              image: docker.io/alpine/k8s:1.36.2
              command: ["/bin/sh", "/scripts/ddns.sh"]
              env:
                - name: DOMAIN
                  value: "${DOMAIN}"
                - name: RECORD_NAMES
                  value: "${RECORD_NAMES}"
                - name: HCLOUD_TOKEN
                  valueFrom:
                    secretKeyRef:
                      name: hetzner-dns-token
                      key: HCLOUD_TOKEN
              resources:
                requests:
                  cpu: 10m
                  memory: 32Mi
                limits:
                  memory: 64Mi
              volumeMounts:
                - name: script
                  mountPath: /scripts
          volumes:
            - name: script
              configMap:
                name: ddns-script
DDNSTAIL
fi

# Operator-provided secrets (generated by smallworlds-init.sh)
if [ -n "$SECRETS_MANIFEST" ] && [ -f "$SECRETS_MANIFEST" ]; then
    mv "$SECRETS_MANIFEST" /var/lib/rancher/k3s/server/manifests/smallworlds-secrets.yaml
    chmod 600 /var/lib/rancher/k3s/server/manifests/smallworlds-secrets.yaml
fi

# ------------------------------------------------------------------
# 4. Install k3s (same flags as the cloud-init template; auto-applies
#    the manifests written above). On SELinux-enforcing systems the
#    installer pulls in k3s-selinux automatically.
# ------------------------------------------------------------------
echo -e "${CYAN}Installing k3s...${NC}"
if [ ! -f "$MARKER_DIR/k3s-ready" ]; then
    INSTALL_K3S_VERSION="$K3S_VERSION" sh "$K3S_INSTALLER" server --cluster-init --node-ip="$NODE_IP" --node-name="$NODE_NAME" --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100
fi

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
echo -e "${CYAN}Waiting for the node to become Ready...${NC}"
until kubectl get nodes 2>/dev/null | grep -v NotReady | grep -q Ready; do sleep 5; done
touch "$MARKER_DIR/k3s-ready"

# Export a kubeconfig for retrieval by the installer (world-unreadable, but
# owned by the invoking sudo user so a non-root scp can pick it up).
EXPORT_KUBECONFIG=/tmp/smallworlds-kubeconfig.yaml
cp /etc/rancher/k3s/k3s.yaml "$EXPORT_KUBECONFIG"
sed -i "s/127.0.0.1/$NODE_IP/g" "$EXPORT_KUBECONFIG"
chmod 600 "$EXPORT_KUBECONFIG"
if [ -n "${SUDO_USER:-}" ]; then chown "$SUDO_USER" "$EXPORT_KUBECONFIG"; fi

# ------------------------------------------------------------------
# 5. Install ArgoCD (identical to the cloud-init template)
# ------------------------------------------------------------------
echo -e "${CYAN}Installing ArgoCD...${NC}"
kubectl create namespace argocd 2>/dev/null || true
kubectl apply -n argocd -f "$ARGOCD_MANIFEST" --server-side --force-conflicts
cat > /tmp/argocd-cm-patch.yaml <<'EOF'
data:
  kustomize.buildOptions: "--enable-helm"
  server.insecure: "true"
  resource.customizations.health.argoproj.io_Application: |
    hs = {}
    hs.status = "Progressing"
    hs.message = ""
    if obj.status ~= nil then
      if obj.status.health ~= nil then
        hs.status = obj.status.health.status
        if obj.status.health.message ~= nil then
          hs.message = obj.status.health.message
        end
      end
    end
    return hs
EOF
kubectl patch cm/argocd-cm -n argocd --type=merge --patch-file /tmp/argocd-cm-patch.yaml
# server.insecure is only honored in argocd-cmd-params-cm (NOT argocd-cm);
# without it argocd-server 307-redirects Traefik's plain-HTTP upstream
# traffic back to https forever and the deploy.<domain> UI never loads
kubectl patch cm/argocd-cmd-params-cm -n argocd --type=merge -p '{"data":{"server.insecure":"true"}}'
kubectl -n argocd rollout restart deployment argocd-server
touch "$MARKER_DIR/argocd-ready"

# ------------------------------------------------------------------
# 6. ArgoCD root app (app-of-apps pointing at the community overlay repo)
# ------------------------------------------------------------------
if [ -n "$ROOT_APP_GIT_URL" ]; then
    if ! [[ "$ROOT_APP_GIT_REVISION" =~ ^[0-9a-f]{40,64}$ ]]; then
        echo -e "${RED}ROOT_APP_GIT_REVISION must be the reviewed overlay commit.${NC}" >&2
        exit 1
    fi
    cat > /tmp/argocd-root-app.yaml <<ROOTAPP
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: smallworlds-root
  namespace: argocd
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    repoURL: '${ROOT_APP_GIT_URL}'
    targetRevision: '${ROOT_APP_GIT_REVISION}'
    path: .
  destination:
    server: 'https://kubernetes.default.svc'
    namespace: argocd
  syncPolicy:
    # Generous retries: without them ArgoCD gives up after 5 attempts and
    # never retries the same revision — one transient wave failure during
    # bootstrap then stalls the whole install until a manual sync
    retry:
      limit: 20
      backoff:
        duration: 15s
        factor: 2
        maxDuration: 5m
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
      - SkipDryRunOnMissingResource=true
ROOTAPP
    kubectl apply -f /tmp/argocd-root-app.yaml
    touch "$MARKER_DIR/overlay-applied"

    # ------------------------------------------------------------------
    # 7. Certificate issuer (needs cert-manager's CRDs, which Argo CD brings)
    # ------------------------------------------------------------------
    # Waiting for the one precondition beats retrying blindly: every Ingress
    # annotates cluster-issuer letsencrypt-prod, so without this nothing gets a
    # certificate. A timeout fails the run rather than passing silently — the
    # step is idempotent, so a resumed run simply waits again.
    if [ ! -f "$MARKER_DIR/issuer-ready" ]; then
        echo -e "${YELLOW}Waiting for cert-manager's CRDs before creating the certificate issuer...${NC}"
        # kubectl wait fails immediately on a resource that does not exist yet,
        # and this CRD only appears once Argo CD has synced cert-manager. So poll
        # for it to appear, then wait for it to be established.
        ISSUER_DEADLINE=$(( $(date +%s) + ${ISSUER_CRD_TIMEOUT_SECONDS:-900} ))
        until kubectl get crd clusterissuers.cert-manager.io >/dev/null 2>&1; do
            if [ "$(date +%s)" -ge "$ISSUER_DEADLINE" ]; then
                echo -e "${RED}cert-manager did not install clusterissuers.cert-manager.io in time.${NC}" >&2
                echo -e "${RED}No certificate can be issued until it does; re-run to wait again.${NC}" >&2
                exit 1
            fi
            sleep 5
        done
        kubectl wait --for=condition=Established --timeout=2m crd/clusterissuers.cert-manager.io
        # An Established CRD says nothing about cert-manager being able to
        # *admit* one. Its validating webhook is a separate Deployment installed
        # by the same Argo CD Application, and until that has endpoints every
        # create is refused with "no endpoints available for service
        # cert-manager-webhook". The CRD appearing and the webhook serving are
        # tens of seconds apart, so on a first install a single apply lands in
        # that gap almost every time — and then nothing ever issues a
        # certificate, because every Ingress annotates this issuer by name.
        # Retry until the deadline instead of assuming.
        until kubectl apply -f "$MARKER_DIR/letsencrypt-prod.yaml" >/dev/null 2>&1; do
            if [ "$(date +%s)" -ge "$ISSUER_DEADLINE" ]; then
                echo -e "${RED}The certificate issuer could not be created before the deadline.${NC}" >&2
                # Run it once more unsilenced so the real reason is in the log.
                kubectl apply -f "$MARKER_DIR/letsencrypt-prod.yaml" >&2 || true
                exit 1
            fi
            sleep 5
        done
        touch "$MARKER_DIR/issuer-ready"
    fi
fi

# The config file may sit in /tmp next to the secrets — remove both traces.
rm -f "$CONFIG_FILE"
rm -f "$MARKER_DIR/bootstrap-interrupted"
touch "$MARKER_DIR/bootstrap-complete"
trap - ERR INT TERM HUP

echo -e "${GREEN}Local node bootstrap complete. Node IP: ${NODE_IP}${NC}"
echo -e "${GREEN}Kubeconfig exported to ${EXPORT_KUBECONFIG} (retrieved and deleted by the installer).${NC}"
