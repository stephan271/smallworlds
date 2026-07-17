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
#   ACME_EMAIL        Let's Encrypt account email; "" = self-signed issuer.
#                     Only set this if ports 80/443 of this machine are
#                     reachable from the public internet — HTTP-01 fails
#                     behind NAT and the cluster ends up with no certs at all.
#   DATA_DIR          where all persistent data lives (default
#                     /var/lib/smallworlds-data); symlinked to
#                     /mnt/smallworlds-data, which the manifests expect
#   NODE_NAME         stable k3s node name (default smallworlds-local-node)
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
ACME_EMAIL="${ACME_EMAIL:-}"
DATA_DIR="${DATA_DIR:-/var/lib/smallworlds-data}"
NODE_NAME="${NODE_NAME:-smallworlds-local-node}"
SECRETS_MANIFEST="${SECRETS_MANIFEST:-}"

# ------------------------------------------------------------------
# Preflight checks
# ------------------------------------------------------------------
if command -v k3s >/dev/null 2>&1 || systemctl is-active --quiet k3s 2>/dev/null; then
    echo -e "${RED}k3s is already installed on this machine.${NC}" >&2
    echo "A stale cluster must never be adopted silently. If this machine previously" >&2
    echo "ran SmallWorlds (or anything else on k3s), remove it first:" >&2
    echo "    sudo $0 --uninstall" >&2
    exit 1
fi

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

# Relocate k3s state onto the data dir so a k3s reinstall/upgrade never
# separates cluster state from user data.
mkdir -p /var/lib/rancher
if [ -d /var/lib/rancher/k3s ] && [ ! -L /var/lib/rancher/k3s ]; then
    cp -a /var/lib/rancher/k3s/. "$DATA_DIR/k3s/"
    rm -rf /var/lib/rancher/k3s
fi
ln -sfn "$DATA_DIR/k3s" /var/lib/rancher/k3s

# ------------------------------------------------------------------
# 3. Bootstrap manifests (auto-applied by k3s on startup)
# ------------------------------------------------------------------
mkdir -p /var/lib/rancher/k3s/server/manifests

if [ -n "$ACME_EMAIL" ]; then
    cat > /var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml <<ISSUER
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
          class: traefik
ISSUER
else
    # Self-signed issuer published under the production name so the
    # cluster-issuer annotations on the Ingresses work unchanged
    cat > /var/lib/rancher/k3s/server/manifests/letsencrypt-prod.yaml <<'ISSUER'
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
        $NODE_IP identity${ENV_EXT}.${DOMAIN} files${ENV_EXT}.${DOMAIN} photos${ENV_EXT}.${DOMAIN} git${ENV_EXT}.${DOMAIN} mail${ENV_EXT}.${DOMAIN} meet${ENV_EXT}.${DOMAIN} webmail${ENV_EXT}.${DOMAIN} whiteboard${ENV_EXT}.${DOMAIN} office${ENV_EXT}.${DOMAIN} dashboard${ENV_EXT}.${DOMAIN} monitoring${ENV_EXT}.${DOMAIN}
        fallthrough
      }
      forward . /etc/resolv.conf
    }
COREDNS

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
curl -sfL https://get.k3s.io | sh -s - server --cluster-init --node-ip="$NODE_IP" --node-name="$NODE_NAME" --disable traefik --kubelet-arg=registry-qps=50 --kubelet-arg=registry-burst=100

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
echo -e "${CYAN}Waiting for the node to become Ready...${NC}"
until kubectl get nodes 2>/dev/null | grep -v NotReady | grep -q Ready; do sleep 5; done

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
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml --server-side --force-conflicts
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

# ------------------------------------------------------------------
# 6. ArgoCD root app (app-of-apps pointing at the community overlay repo)
# ------------------------------------------------------------------
if [ -n "$ROOT_APP_GIT_URL" ]; then
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
    targetRevision: HEAD
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
fi

# The config file may sit in /tmp next to the secrets — remove both traces.
rm -f "$CONFIG_FILE"

echo -e "${GREEN}Local node bootstrap complete. Node IP: ${NODE_IP}${NC}"
echo -e "${GREEN}Kubeconfig exported to ${EXPORT_KUBECONFIG} (retrieved and deleted by the installer).${NC}"
