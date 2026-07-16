# Shared helpers for cluster-facing admin scripts. Source, don't execute.
#
# Environment selection (production vs dev cluster):
#   1. ENV_EXT environment variable if set (e.g. ENV_EXT=".dev"), matching the
#      terraform `env_ext` variable — empty string means production.
#   2. Otherwise `env_ext` parsed from infrastructure/terraform/terraform.tfvars.
#   3. Otherwise "" (production).

LIB_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LIB_TF_DIR="$LIB_REPO_ROOT/infrastructure/terraform"

SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"

detect_env_ext() {
  if [ -n "${ENV_EXT+x}" ]; then
    echo "$ENV_EXT"
    return
  fi
  sed -n 's/^[[:space:]]*env_ext[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' \
    "$LIB_TF_DIR/terraform.tfvars" 2>/dev/null | head -1
}

# "production" for env_ext="", otherwise the extension without its leading
# dot (".dev" -> "dev")
cluster_label() {
  local ext="${1#.}"
  if [ -z "$ext" ]; then echo "production"; else echo "$ext"; fi
}

# Canonical local kubeconfig path for a cluster label ("production", "dev",
# "staging" — see cluster_label). All tooling reads and writes kubeconfigs
# here, outside any git checkout. Creates the directory on demand.
kubeconfig_path() {
  local dir="$HOME/.smallworlds/kubeconfigs"
  mkdir -p "$dir"
  echo "$dir/$1.yaml"
}

detect_domain() {
  local domain
  domain=$(sed -n 's/^[[:space:]]*domain_name[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' \
    "$LIB_TF_DIR/terraform.tfvars" 2>/dev/null | head -1)
  echo "${domain:-smallworlds.network}"
}

detect_server_ip() {
  local env_ext="$1" ip
  ip=$(terraform -chdir="$LIB_TF_DIR" output -raw server_ipv4 2>/dev/null || true)
  if [ -z "$ip" ]; then
    ip="identity${env_ext}.$(detect_domain)"
  fi
  echo "$ip"
}

# Fetch a fresh kubeconfig from the server (the CA changes on every rebuild,
# so locally cached kubeconfigs go stale). Writes to $2, rewriting the
# API endpoint from 127.0.0.1 to the server address.
fetch_kubeconfig() {
  local server="$1" dest="$2"
  ssh $SSH_OPTS "root@$server" "cat /etc/rancher/k3s/k3s.yaml" 2>/dev/null \
    | sed "s|https://127.0.0.1:6443|https://$server:6443|" > "$dest"
  [ -s "$dest" ]
}
