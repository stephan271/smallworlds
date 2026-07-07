#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# SmallWorlds Golden Image Builder
#
# Builds a Hetzner snapshot containing:
#   - Ubuntu 24.04 with all updates + curl/jq
#   - k3s (pinned) installed but not enabled, install script kept locally
#   - all container images from infrastructure/golden-image/images.txt
#     preloaded into the containerd store
#
# Fresh installs and staging runs then skip apt upgrades, the k3s download
# and ~7GB of image pulls (~10-15 minutes per boot).
#
# Usage:
#   export HCLOUD_TOKEN=...
#   ./admin-tools/build-golden-image.sh [--refresh-list]
#
#   --refresh-list  regenerate images.txt from the live cluster
#                   (requires KUBECONFIG pointing at it) before building
#
# Rebuilt automatically every Monday 05:00 UTC (after the Renovate weekly
# automerge window) by .github/workflows/golden-image.yml, which also prunes
# old snapshots. A stale image is harmless (missing images are simply
# pulled), it just wins back less time.
# ============================================================================

K3S_VERSION="v1.36.2+k3s1"
BUILD_SERVER_TYPE="cx23"   # 40GB disk: snapshot stays usable on any >=40GB server
LOCATION="nbg1"
SNAPSHOT_LABEL="smallworlds-golden=true"

GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
IMAGE_LIST="$REPO_ROOT/infrastructure/golden-image/images.txt"

if [ -z "${HCLOUD_TOKEN:-}" ]; then
    echo -e "${RED}Error: HCLOUD_TOKEN is not set.${NC}"
    exit 1
fi

if [ "${1:-}" = "--refresh-list" ]; then
    echo -e "${CYAN}Regenerating image list from the live cluster...${NC}"
    kubectl get nodes -o jsonpath='{range .items[*].status.images[*]}{.names}{"\n"}{end}' \
        | grep -oE '"[^"@]+:[^"@]+"' | tr -d '"' | grep -v '^sha256' | sort -u > "$IMAGE_LIST"
    echo -e "  $(wc -l < "$IMAGE_LIST") images."
fi

[ -s "$IMAGE_LIST" ] || { echo -e "${RED}Image list $IMAGE_LIST is missing or empty.${NC}"; exit 1; }

SERVER_NAME="golden-image-builder-$$"
TEMP_KEY=$(mktemp -u)
ssh-keygen -t ed25519 -f "$TEMP_KEY" -N "" -q
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -i $TEMP_KEY"

cleanup() {
    echo -e "${YELLOW}Cleaning up builder server and key...${NC}"
    hcloud server delete "$SERVER_NAME" >/dev/null 2>&1 || true
    hcloud ssh-key delete "golden-builder-key-$$" >/dev/null 2>&1 || true
    rm -f "$TEMP_KEY" "$TEMP_KEY.pub"
}
trap cleanup EXIT

echo -e "${CYAN}[1/4] Creating builder server ($BUILD_SERVER_TYPE in $LOCATION)...${NC}"
hcloud ssh-key create --name "golden-builder-key-$$" --public-key-from-file "$TEMP_KEY.pub" >/dev/null
hcloud server create --name "$SERVER_NAME" --type "$BUILD_SERVER_TYPE" --location "$LOCATION" \
    --image ubuntu-24.04 --ssh-key "golden-builder-key-$$" >/dev/null
SERVER_IP=$(hcloud server ip "$SERVER_NAME")
echo -e "  Builder at $SERVER_IP"

echo -e "${CYAN}Waiting for SSH...${NC}"
until timeout 3 bash -c "</dev/tcp/$SERVER_IP/22" 2>/dev/null; do sleep 3; done
sleep 5

# Write the provision script to a file and scp it — NEVER pipe scripts over
# ssh stdin: any command that reads stdin swallows the rest of the script and
# bash exits 0 without running the tail (this silently skipped the cleanup
# once, leaking the bake's cluster state into the snapshot).
PROVISION_SCRIPT=$(mktemp)
cat > "$PROVISION_SCRIPT" <<EOF
#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get upgrade -yq
apt-get install -yq curl jq

cat > /etc/sysctl.d/99-kubernetes-cri.conf <<'SYSCTL'
fs.inotify.max_user_instances=8192
fs.inotify.max_user_watches=524288
SYSCTL

# Keep the install script for node-specific re-runs at first boot
# (INSTALL_K3S_SKIP_DOWNLOAD=true regenerates the systemd unit without network)
curl -sfL https://get.k3s.io -o /usr/local/lib/k3s-install.sh
chmod +x /usr/local/lib/k3s-install.sh
# Disable every addon: this k3s only exists to warm the containerd image
# store, nothing it deploys may leak into the snapshot
INSTALL_K3S_VERSION="$K3S_VERSION" INSTALL_K3S_SKIP_ENABLE=true \\
  /usr/local/lib/k3s-install.sh server \\
  --disable traefik --disable servicelb --disable local-storage --disable metrics-server

systemctl start k3s
until k3s kubectl get node >/dev/null 2>&1; do sleep 3; done

FAILED=""
while IFS= read -r IMG; do
    [ -z "\$IMG" ] && continue
    echo "  pulling \$IMG"
    k3s ctr images pull "\$IMG" >/dev/null 2>&1 </dev/null || FAILED="\$FAILED \$IMG"
done < /tmp/images.txt
if [ -n "\$FAILED" ]; then
    echo "WARNING: failed to pull:\$FAILED (will be pulled at runtime instead)"
fi

# Strip ALL cluster identity but keep the containerd image store: the
# snapshot must boot as a brand-new cluster with a warm image cache
systemctl stop k3s || true
/usr/local/bin/k3s-killall.sh >/dev/null 2>&1 || true
rm -rf /var/lib/rancher/k3s/server
rm -rf /etc/rancher/k3s /etc/rancher/node

# Make the machine snapshot-clean
cloud-init clean --logs
truncate -s 0 /etc/machine-id
rm -f /tmp/images.txt /tmp/provision.sh
EOF

echo -e "${CYAN}[2/4] Provisioning: updates, k3s $K3S_VERSION, image preload...${NC}"
scp $SSH_OPTS "$IMAGE_LIST" root@"$SERVER_IP":/tmp/images.txt >/dev/null
scp $SSH_OPTS "$PROVISION_SCRIPT" root@"$SERVER_IP":/tmp/provision.sh >/dev/null
rm -f "$PROVISION_SCRIPT"
ssh $SSH_OPTS root@"$SERVER_IP" "bash /tmp/provision.sh"

echo -e "${CYAN}[3/4] Verifying the snapshot carries no cluster state...${NC}"
ssh $SSH_OPTS root@"$SERVER_IP" \
    "test ! -e /var/lib/rancher/k3s/server && test ! -e /etc/rancher/k3s && test ! -e /etc/rancher/node && test -d /var/lib/rancher/k3s/agent" \
    || { echo -e "${RED}Cleanup verification FAILED — refusing to snapshot a dirty builder.${NC}"; exit 1; }
echo -e "  Clean: no datastore, no node identity, image store present."

echo -e "${CYAN}[4/4] Powering off and creating snapshot...${NC}"
hcloud server poweroff "$SERVER_NAME" >/dev/null
SNAPSHOT_DESC="smallworlds-golden k3s=$K3S_VERSION $(date -u +%Y-%m-%d)"
# Note: create-image has no -o json in all hcloud CLI versions; query the ID afterwards
hcloud server create-image "$SERVER_NAME" --type snapshot \
    --description "$SNAPSHOT_DESC" --label "$SNAPSHOT_LABEL"
SNAPSHOT_ID=$(hcloud image list --selector "$SNAPSHOT_LABEL" -o json | jq -r 'sort_by(.created) | last | .id')

echo -e "${GREEN}=======================================================${NC}"
echo -e "${GREEN}Golden image created: $SNAPSHOT_ID ($SNAPSHOT_DESC)${NC}"
echo -e "Terraform picks it up automatically via use_golden_image=true"
echo -e "(most recent snapshot labeled $SNAPSHOT_LABEL wins)."
echo -e "${YELLOW}Consider deleting older golden snapshots to save storage:${NC}"
echo -e "  hcloud image list --selector $SNAPSHOT_LABEL"
echo -e "  hcloud image delete <old-id>"
echo -e "${GREEN}=======================================================${NC}"
