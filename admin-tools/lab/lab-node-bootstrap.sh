#!/usr/bin/env bash
# SmallWorlds LAB node bootstrap — the "hand path".
#
# Reproduces the parts of infrastructure/local/bootstrap-local-node.sh that the
# manifests actually depend on:
#   - /mnt/smallworlds-data layout (persistent-storage.yaml hard-codes it)
#   - node name smallworlds-local-node (static-local PV nodeAffinity)
#   - etcd relocated off the bulk disk onto the machine's own storage
#   - same k3s flags as the cloud-init template
#   - coredns-custom domain override
#   - self-signed ClusterIssuer published as "letsencrypt-prod"
#
# Deliberately omitted (this is a lab, not a deployment):
#   - the verified release-asset archive, PROFILE_ID/BOOTSTRAP_RUN_ID markers
#   - ArgoCD and the app-of-apps root application
#   - DDNS, operator secrets manifest
# Everything above k3s is applied afterwards with `kubectl apply -k`.
#
# Usage:  sudo bash smallworlds-lab-node.sh
#         sudo DATA_DIR=/data/smallworlds-data bash smallworlds-lab-node.sh
set -euo pipefail

GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'

DOMAIN="${DOMAIN:-smallworlds.network}"
ENV_EXT="${ENV_EXT:-}"
# Default to the machine's own (NVMe) storage rather than the big spinning
# disk: local-path PVCs — every CNPG database included — live under
# $DATA_DIR/k3s/storage, and Postgres on 5400rpm is a confound the backup
# drill does not need. Override to /data/smallworlds-data for a bulk test.
DATA_DIR="${DATA_DIR:-/var/lib/smallworlds-data}"
ETCD_DIR="${ETCD_DIR:-/var/lib/smallworlds-etcd}"
NODE_NAME="${NODE_NAME:-smallworlds-local-node}"
# Same pin as the current release inputs (docs/releases/bootstrap-inputs/v1.2.34.json).
K3S_VERSION="${K3S_VERSION:-v1.36.2+k3s1}"
LAB_DIR=/etc/smallworlds-lab

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}Run as root: sudo bash $0${NC}" >&2
    exit 1
fi

# ------------------------------------------------------------------
# Preflight
# ------------------------------------------------------------------
if command -v k3s >/dev/null 2>&1 || systemctl is-active --quiet k3s 2>/dev/null; then
    echo -e "${RED}k3s is already installed. Remove it first:${NC}" >&2
    echo "    sudo /usr/local/bin/k3s-uninstall.sh" >&2
    exit 1
fi
command -v curl >/dev/null 2>&1 || { echo -e "${RED}curl is required.${NC}" >&2; exit 1; }

if systemctl is-active --quiet firewalld 2>/dev/null; then
    echo -e "${YELLOW}firewalld is active — k3s pod/service traffic gets silently dropped.${NC}"
    echo -e "${YELLOW}Run 'systemctl disable --now firewalld' or add the k3s rules, then re-run.${NC}"
    exit 1
fi

NODE_IP=$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1)
[ -z "$NODE_IP" ] && NODE_IP=$(hostname -I | awk '{print $1}')
[ -n "$NODE_IP" ] || { echo -e "${RED}Could not determine the LAN IP.${NC}" >&2; exit 1; }

echo -e "${CYAN}Lab bootstrap: node ${NODE_NAME} (${NODE_IP}), data ${DATA_DIR}, etcd ${ETCD_DIR}${NC}"

# ------------------------------------------------------------------
# 1. Kernel limits (same values as the cloud-init template)
# ------------------------------------------------------------------
cat > /etc/sysctl.d/99-kubernetes-cri.conf <<'SYSCTL'
fs.inotify.max_user_instances=8192
fs.inotify.max_user_watches=524288
SYSCTL
sysctl --system >/dev/null

# ------------------------------------------------------------------
# 2. Data directories — the manifests hard-code /mnt/smallworlds-data
# ------------------------------------------------------------------
mkdir -p "$DATA_DIR/garage" "$DATA_DIR/immich-library" "$DATA_DIR/k3s"
if [ "$DATA_DIR" != "/mnt/smallworlds-data" ]; then
    ln -sfn "$DATA_DIR" /mnt/smallworlds-data
fi

mkdir -p /var/lib/rancher
ln -sfn "$DATA_DIR/k3s" /var/lib/rancher/k3s

# etcd fsyncs every write; when the disk cannot keep up it drops its leader
# lease and k3s restarts in a loop. Keep the datastore off the bulk disk.
mkdir -p "$ETCD_DIR" "$DATA_DIR/k3s/server"
ln -sfn "$ETCD_DIR" "$DATA_DIR/k3s/server/db"

ETCD_SOURCE_DEVICE=$(df --output=source "$ETCD_DIR" 2>/dev/null | tail -1)
ETCD_DISK=$(lsblk -no pkname "$ETCD_SOURCE_DEVICE" 2>/dev/null | head -1)
[ -n "$ETCD_DISK" ] || ETCD_DISK=$(basename "${ETCD_SOURCE_DEVICE:-none}")
if [ "$(cat "/sys/block/$ETCD_DISK/queue/rotational" 2>/dev/null || echo 0)" = "1" ]; then
    echo -e "${RED}Refusing: $ETCD_DIR is on $ETCD_DISK, a rotating disk.${NC}" >&2
    echo -e "${RED}Point ETCD_DIR at the NVMe or the control plane will restart in a loop.${NC}" >&2
    exit 1
fi

DATA_SOURCE_DEVICE=$(df --output=source "$DATA_DIR" 2>/dev/null | tail -1)
DATA_DISK=$(lsblk -no pkname "$DATA_SOURCE_DEVICE" 2>/dev/null | head -1)
[ -n "$DATA_DISK" ] || DATA_DISK=$(basename "${DATA_SOURCE_DEVICE:-none}")
if [ "$(cat "/sys/block/$DATA_DISK/queue/rotational" 2>/dev/null || echo 0)" = "1" ]; then
    echo -e "${YELLOW}Note: $DATA_DIR is on $DATA_DISK, a rotating disk. Bulk data is fine there,${NC}"
    echo -e "${YELLOW}but every local-path PVC (all CNPG databases) lives there too and will be slow.${NC}"
fi

# ------------------------------------------------------------------
# 3. Bootstrap manifests (auto-applied by k3s on startup)
# ------------------------------------------------------------------
mkdir -p "$DATA_DIR/k3s/server/manifests" "$LAB_DIR"

# Pods must resolve the app domains to this node, not to whatever public DNS
# says — which for smallworlds.network is the production cluster.
cat > "$DATA_DIR/k3s/server/manifests/coredns-custom.yaml" <<COREDNS
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

# Not auto-applied: a ClusterIssuer cannot apply before cert-manager's CRDs
# exist, and k3s would retry forever, writing events into etcd every few
# seconds. Applied by hand once cert-manager is in.
cat > "$LAB_DIR/letsencrypt-prod.yaml" <<'ISSUER'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  selfSigned: {}
ISSUER

# ------------------------------------------------------------------
# 4. Install k3s — same flags as the cloud-init template
# ------------------------------------------------------------------
echo -e "${CYAN}Installing k3s ${K3S_VERSION}...${NC}"
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="$K3S_VERSION" sh -s - \
    server --cluster-init \
    --node-ip="$NODE_IP" --node-name="$NODE_NAME" \
    --disable traefik \
    --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
echo -e "${CYAN}Waiting for the node to become Ready...${NC}"
DEADLINE=$(( $(date +%s) + 300 ))
until kubectl get nodes 2>/dev/null | grep -v NotReady | grep -q Ready; do
    if [ "$(date +%s)" -ge "$DEADLINE" ]; then
        echo -e "${RED}Node did not become Ready within 5 minutes.${NC}" >&2
        echo "Check: journalctl -u k3s -n 50" >&2
        exit 1
    fi
    sleep 5
done

# ------------------------------------------------------------------
# 5. Export a kubeconfig the operator laptop can scp
# ------------------------------------------------------------------
EXPORT_KUBECONFIG=/tmp/smallworlds-lab-kubeconfig.yaml
cp /etc/rancher/k3s/k3s.yaml "$EXPORT_KUBECONFIG"
sed -i "s/127.0.0.1/$NODE_IP/g" "$EXPORT_KUBECONFIG"
chmod 600 "$EXPORT_KUBECONFIG"
[ -n "${SUDO_USER:-}" ] && chown "$SUDO_USER" "$EXPORT_KUBECONFIG"

kubectl get nodes -o wide
echo
echo -e "${GREEN}Lab node ready. Node IP: ${NODE_IP}${NC}"
echo -e "${GREEN}Kubeconfig: ${EXPORT_KUBECONFIG}${NC}"
echo -e "${CYAN}Storage contract:${NC}"
ls -ld /mnt/smallworlds-data "$DATA_DIR"/{garage,immich-library,k3s} /var/lib/rancher/k3s "$DATA_DIR/k3s/server/db"
