#!/usr/bin/env bash
# SmallWorlds local-server bootstrap.
#
# This is the LAN counterpart of infrastructure/cloud-init/k3s-node.yaml.tpl:
# it performs the same k3s + ArgoCD bootstrap, but on an existing Linux
# machine (e.g. a laptop or mini-PC in your LAN) instead of a cloud-init'd
# Hetzner VM. It is normally invoked by smallworlds-init.sh over SSH, but can
# also be run manually on the target machine as root.
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
#   ETCD_DIR          where the cluster datastore lives (default
#                     /var/lib/smallworlds-etcd, on the machine's own disk).
#                     Must be fast storage — see the relocation below.
#   NODE_NAME         stable k3s node name (default smallworlds-local-node)
#   PROFILE_ID        Launcher profile that owns this installation. Required
#                     for safe resume; never use a node already owned by a
#                     different profile.
#   BOOTSTRAP_RUN_ID  Launcher-generated identifier recorded in durable
#                     checkpoints (required).
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
MANAGE_DNS="${MANAGE_DNS:-}"
DATA_DIR="${DATA_DIR:-/var/lib/smallworlds-data}"
BACKUP_DIR="${BACKUP_DIR:-/var/lib/smallworlds-backup}"
ETCD_DIR="${ETCD_DIR:-/var/lib/smallworlds-etcd}"
NODE_NAME="${NODE_NAME:-smallworlds-local-node}"
PROFILE_ID="${PROFILE_ID:?PROFILE_ID must be set in $CONFIG_FILE}"
BOOTSTRAP_RUN_ID="${BOOTSTRAP_RUN_ID:?BOOTSTRAP_RUN_ID must be set in $CONFIG_FILE}"
SECRETS_MANIFEST="${SECRETS_MANIFEST:-}"

# This script is an execution payload, not an ambient-upstream installer. The
# Launcher obtains this directory from a signed release archive before opening
# SSH, so all executable and manifest inputs are already verified.
BOOTSTRAP_ASSET_DIR="${SMALLWORLDS_BOOTSTRAP_ASSET_DIR:-}"
if [ -z "$BOOTSTRAP_ASSET_DIR" ]; then
    echo -e "${RED}This bootstrap must be run from a verified SmallWorlds release archive.${NC}" >&2
    exit 1
fi
K3S_INSTALLER="$BOOTSTRAP_ASSET_DIR/third-party/k3s-install.sh"
K3S_VERSION_FILE="$BOOTSTRAP_ASSET_DIR/third-party/k3s-version"
ARGOCD_MANIFEST="$BOOTSTRAP_ASSET_DIR/third-party/argocd-install.yaml"
for asset in "$K3S_INSTALLER" "$K3S_VERSION_FILE" "$ARGOCD_MANIFEST"; do
    if [ ! -r "$asset" ]; then
        echo -e "${RED}Verified bootstrap archive is incomplete: $asset${NC}" >&2
        exit 1
    fi
done
K3S_VERSION="$(tr -d '\r\n' < "$K3S_VERSION_FILE")"
if ! [[ "$K3S_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9._+~-]*$ ]]; then
    echo -e "${RED}Verified bootstrap archive has an invalid k3s version.${NC}" >&2
    exit 1
fi

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

for tool in curl; do
    command -v "$tool" >/dev/null 2>&1 || { echo -e "${RED}Missing required tool: $tool${NC}" >&2; exit 1; }
done

# Primary LAN IP: the address this machine uses to reach the outside world.
NODE_IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1)
[ -z "$NODE_IP" ] && NODE_IP=$(hostname -I | awk '{print $1}')
if [ -z "$NODE_IP" ]; then
    echo -e "${RED}Could not determine this machine's LAN IP address.${NC}" >&2
    exit 1
fi
echo -e "${CYAN}Bootstrapping SmallWorlds on this machine (node IP: ${NODE_IP}, data dir: ${DATA_DIR})...${NC}"

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
mkdir -p "$DATA_DIR/garage" "$DATA_DIR/immich-library" "$DATA_DIR/k3s"
if [ "$DATA_DIR" != "/mnt/smallworlds-data" ]; then
    ln -sfn "$DATA_DIR" /mnt/smallworlds-data
fi

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
if [ -n "$ACME_EMAIL" ]; then
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
    for sub in identity dashboard files photos git mail webmail monitoring whiteboard meet office plan deploy pod; do
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
