#!/usr/bin/env bash
# Installs the Ring 0 Steward's GitHub Actions runner on Ring 0's server
# (design-per-app-updates.md §5.3, docs/adr/0064). Run as root on that server.
#
# The runner runs as the unprivileged user `steward`, outside k3s, and is
# registered to the PRIVATE Ring 0 overlay repository only: a self-hosted runner
# on a public repository could be handed work by anyone's pull request. Its only
# cluster credential is the ServiceAccount ring0-steward
# (tenants/canary-follower/steward-rbac.yaml): no Secrets, no exec, deletes only
# pods and Jobs. It cannot read /etc/rancher/k3s/k3s.yaml.
#
#   Take the three values from GitHub: my-canary-installation → Settings →
#   Actions → Runners → New self-hosted runner → Linux x64. The page shows the
#   download URL, its SHA-256 and a registration token valid for one hour.
#
#   RUNNER_URL=https://github.com/actions/runner/releases/download/v…/actions-runner-linux-x64-….tar.gz \
#   RUNNER_SHA256=… RUNNER_TOKEN=… \
#     ./install-steward-runner.sh
#
#   ./install-steward-runner.sh --kubeconfig-only   # after rotating the token Secret
set -euo pipefail

REPO_URL="${REPO_URL:-https://github.com/stephan271/my-canary-installation}"
RUNNER_NAME="${RUNNER_NAME:-ring0-steward}"
RUNNER_LABELS="${RUNNER_LABELS:-ring0-steward}"
USER_NAME=steward
HOME_DIR=/home/$USER_NAME
ROOT_KUBECONFIG=/etc/rancher/k3s/k3s.yaml
NAMESPACE=canary-follower
TOKEN_SECRET=ring0-steward-token

[[ $(id -u) -eq 0 ]] || { echo "Run as root on Ring 0's server." >&2; exit 1; }
[[ -r $ROOT_KUBECONFIG ]] || { echo "No $ROOT_KUBECONFIG: is this the k3s server?" >&2; exit 1; }

write_kubeconfig() {
    echo ">>> kubeconfig for ServiceAccount ring0-steward"
    local token ca
    for _ in $(seq 1 30); do
        token=$(KUBECONFIG=$ROOT_KUBECONFIG kubectl -n $NAMESPACE get secret $TOKEN_SECRET \
            -o jsonpath='{.data.token}' 2>/dev/null | base64 -d || true)
        [[ -n $token ]] && break
        sleep 2
    done
    [[ -n $token ]] || { echo "Secret $NAMESPACE/$TOKEN_SECRET has no token. Is the canary-follower" \
        "Application synced with steward-rbac.yaml?" >&2; exit 1; }
    ca=$(KUBECONFIG=$ROOT_KUBECONFIG kubectl -n $NAMESPACE get secret $TOKEN_SECRET \
        -o jsonpath='{.data.ca\.crt}')
    install -d -m 700 -o $USER_NAME -g $USER_NAME "$HOME_DIR/.kube"
    umask 077
    cat > "$HOME_DIR/.kube/config" <<EOF
apiVersion: v1
kind: Config
clusters:
  - name: ring0
    cluster:
      server: https://127.0.0.1:6443
      certificate-authority-data: $ca
users:
  - name: ring0-steward
    user:
      token: $token
contexts:
  - name: ring0
    context: {cluster: ring0, user: ring0-steward}
current-context: ring0
EOF
    chown $USER_NAME:$USER_NAME "$HOME_DIR/.kube/config"
    chmod 600 "$HOME_DIR/.kube/config"
    # Prove the limits hold before anything depends on them.
    local can
    # k3s's kubectl falls back to root's kubeconfig: always name the file.
    local as_steward=(sudo -u $USER_NAME env KUBECONFIG="$HOME_DIR/.kube/config" kubectl)
    can=$("${as_steward[@]}" auth can-i get secrets -A 2>/dev/null || true)
    [[ $can == no ]] || { echo "ring0-steward can read Secrets ($can): stop." >&2; exit 1; }
    can=$("${as_steward[@]}" auth can-i create pods/exec -A 2>/dev/null || true)
    [[ $can == no ]] || { echo "ring0-steward can exec into pods ($can): stop." >&2; exit 1; }
    "${as_steward[@]}" get applications -n argocd >/dev/null
    echo "    reads Applications, cannot read Secrets or exec: as intended"
}

if [[ ${1:-} == --kubeconfig-only ]]; then
    write_kubeconfig
    exit 0
fi

: "${RUNNER_URL:?RUNNER_URL from the page New self-hosted runner on GitHub}"
: "${RUNNER_SHA256:?RUNNER_SHA256 from the same page}"
: "${RUNNER_TOKEN:?RUNNER_TOKEN (registration token) from the same page}"

echo ">>> packages the Steward uses: git, gh, jq, python3, unzip (the action fetches bun)"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git gh jq python3 unzip curl ca-certificates >/dev/null

echo ">>> user $USER_NAME (no sudo, no docker group)"
id $USER_NAME >/dev/null 2>&1 || useradd --create-home --shell /bin/bash $USER_NAME
if id -nG $USER_NAME | grep -qwE 'sudo|wheel|docker|adm'; then
    echo "$USER_NAME is in a privileged group: stop." >&2; exit 1
fi

write_kubeconfig

echo ">>> runner"
RUNNER_DIR=$HOME_DIR/actions-runner
install -d -o $USER_NAME -g $USER_NAME "$RUNNER_DIR"
ARCHIVE=$(mktemp)
curl -fsSL -o "$ARCHIVE" "$RUNNER_URL"
echo "$RUNNER_SHA256  $ARCHIVE" | sha256sum -c - >/dev/null || {
    echo "The runner download does not match RUNNER_SHA256." >&2; rm -f "$ARCHIVE"; exit 1; }
sudo -u $USER_NAME tar -xzf "$ARCHIVE" -C "$RUNNER_DIR"
rm -f "$ARCHIVE"
"$RUNNER_DIR/bin/installdependencies.sh" >/dev/null

cd "$RUNNER_DIR"
sudo -u $USER_NAME ./config.sh --unattended --replace --url "$REPO_URL" --token "$RUNNER_TOKEN" \
    --name "$RUNNER_NAME" --labels "$RUNNER_LABELS" --work _work
# Every job sees the ServiceAccount's kubeconfig, never root's.
echo "KUBECONFIG=$HOME_DIR/.kube/config" | sudo -u $USER_NAME tee "$RUNNER_DIR/.env" >/dev/null
./svc.sh install $USER_NAME
SERVICE=$(cat .service)

echo ">>> limits, so a run cannot starve Ring 0 (8 CPUs, 24 GB)"
install -d "/etc/systemd/system/$SERVICE.d"
cat > "/etc/systemd/system/$SERVICE.d/limits.conf" <<EOF
[Service]
CPUQuota=200%
MemoryMax=4G
TasksMax=512
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=$HOME_DIR
PrivateTmp=true
EOF
systemctl daemon-reload
./svc.sh start
systemctl --no-pager --lines=0 status "$SERVICE"

cat <<EOF

Done. The runner '$RUNNER_NAME' (labels: $RUNNER_LABELS) is registered to
$REPO_URL and runs as $USER_NAME under $SERVICE.
GitHub shows it under Settings → Actions → Runners as Idle.
EOF
