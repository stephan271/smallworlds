#!/usr/bin/env bash
# Checks infrastructure/bootstrap-pins.env and the bootstrap paths that read it
# (docs/adr/0057).
#
#   admin-tools/test-bootstrap-pins.sh            offline: format, immutable
#                                                 URLs, every path wired up, and
#                                                 the node's checksum check
#   admin-tools/test-bootstrap-pins.sh --online   also download both pinned files
#                                                 and compare their digests
#
# The offline half touches no network. Run --online before tagging a release,
# and whenever a pin is changed.
set -uo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
pins="$repository_root/infrastructure/bootstrap-pins.env"
local_bootstrap="$repository_root/infrastructure/local/bootstrap-local-node.sh"
cloud_init="$repository_root/infrastructure/cloud-init/k3s-node.yaml.tpl"
online=0
[[ "${1:-}" == "--online" ]] && online=1

failures=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; failures=$((failures + 1)); }
check() { if "${@:2}"; then pass "$1"; else fail "$1"; fi; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# --------------------------------------------------------------------------
echo "bootstrap-pins.env — format"
# --------------------------------------------------------------------------
[[ -r "$pins" ]] || { fail "the pins file exists"; echo; echo "1 check(s) failed."; exit 1; }

# Terraform parses the file line by line, so anything but comments, blank lines
# and bare KEY=value would be read differently there than by the shell.
stray="$(grep -vE '^(#.*|[[:space:]]*|[A-Z0-9_]+=[^[:space:]"'"'"'$`\\]+)$' "$pins" || true)"
check "only comments and bare KEY=value lines" test -z "$stray"

# shellcheck disable=SC1090
source "$pins"
for key in K3S_VERSION K3S_INSTALLER_URL K3S_INSTALLER_SHA256 \
           ARGOCD_VERSION ARGOCD_MANIFEST_URL ARGOCD_MANIFEST_SHA256; do
    check "$key is set" test -n "${!key:-}"
done
is_sha256() { [[ "$1" =~ ^[0-9a-f]{64}$ ]]; }
check "the K3s installer digest is a SHA-256" is_sha256 "${K3S_INSTALLER_SHA256:-}"
check "the Argo CD manifest digest is a SHA-256" is_sha256 "${ARGOCD_MANIFEST_SHA256:-}"
matches() { [[ "$1" =~ $2 ]]; }
check "the K3s version looks like vX.Y.Z+k3sN" matches "${K3S_VERSION:-}" '^v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+$'

# --------------------------------------------------------------------------
echo "bootstrap-pins.env — the URLs cannot change under the digest"
# --------------------------------------------------------------------------
# https://get.k3s.io and a branch name both serve today's file: the digest then
# stops matching the day upstream edits it, and every install fails.
immutable() { [[ "$1" == https://* && ! "$1" =~ get\.k3s\.io|/(master|main|stable|latest|HEAD)/ ]]; }
check "the K3s installer URL names no branch" immutable "${K3S_INSTALLER_URL:-}"
check "the Argo CD manifest URL names no branch" immutable "${ARGOCD_MANIFEST_URL:-}"
check "the K3s installer is pinned by commit" matches "${K3S_INSTALLER_URL:-}" '/[0-9a-f]{40}/install\.sh$'
check "the Argo CD URL is the pinned version's" matches "${ARGOCD_MANIFEST_URL:-}" "/${ARGOCD_VERSION:-none}/manifests/install\.yaml\$"

# --------------------------------------------------------------------------
echo "every bootstrap path installs the pins"
# --------------------------------------------------------------------------
absent() { ! grep -qE "$1" "$2"; }
present() { grep -qF -- "$1" "$2"; }
for path in "$local_bootstrap" "$cloud_init" "$repository_root/admin-tools/build-golden-image.sh"; do
    name="${path#"$repository_root"/}"
    check "$name fetches nothing unpinned" absent 'get\.k3s\.io|argo-cd/(stable|master)/' "$path"
done
check "the local bootstrap checks both downloads" \
    test "$(grep -c '^fetch_verified "\$' "$local_bootstrap")" -eq 2
for value in k3s_version k3s_installer_url k3s_installer_sha256 argocd_manifest_url argocd_manifest_sha256; do
    check "cloud-init uses \${$value}" present "\${$value}" "$cloud_init"
    for root in terraform terraform-staging; do
        check "$root passes $value" present "$value " "$repository_root/infrastructure/$root/main.tf"
    done
done
check "the installer ships the pins with the script" \
    present '"$BOOTSTRAP_SCRIPT" "$BOOTSTRAP_PINS"' "$repository_root/smallworlds-init.sh"

# --------------------------------------------------------------------------
echo "the node's checksum check"
# --------------------------------------------------------------------------
# The function is lifted out of the bootstrap script and run against local
# files, so this exercises the code a node runs without needing root or k3s.
sed -n '/^fetch_verified() {$/,/^}$/p' "$local_bootstrap" > "$work/fetch.sh"
check "fetch_verified could be extracted" test -s "$work/fetch.sh"
printf 'reviewed contents\n' > "$work/source"
good="$(sha256sum "$work/source" | cut -d' ' -f1)"
run_fetch() {
    ( RED= NC=; source "$work/fetch.sh"; fetch_verified "file://$work/source" "$1" "$2" ) >/dev/null 2>"$work/stderr"
}
if run_fetch "$good" "$work/kept" && cmp -s "$work/source" "$work/kept"; then
    pass "a file with the pinned digest is kept"
else
    fail "a file with the pinned digest is kept"
fi
if run_fetch "$(printf '0%.0s' {1..64})" "$work/refused"; then
    fail "a file with another digest stops the bootstrap"
else
    pass "a file with another digest stops the bootstrap"
fi
check "and is not left behind" test ! -e "$work/refused"
check "and the message names the pins file" grep -q 'bootstrap-pins.env' "$work/stderr"

# --------------------------------------------------------------------------
if [[ "$online" -eq 1 ]]; then
    echo "upstream still serves what was pinned"
    digest_of() { curl -fsSL --retry 3 --max-time 120 "$1" | sha256sum | cut -d' ' -f1; }
    check "the K3s installer matches its digest" \
        test "$(digest_of "$K3S_INSTALLER_URL")" = "$K3S_INSTALLER_SHA256"
    check "the Argo CD manifest matches its digest" \
        test "$(digest_of "$ARGOCD_MANIFEST_URL")" = "$ARGOCD_MANIFEST_SHA256"
    # The installer downloads K3s itself, from the release this version names.
    check "K3s $K3S_VERSION is a published release" curl -fsSL --max-time 60 -o /dev/null \
        "https://github.com/k3s-io/k3s/releases/download/${K3S_VERSION//+/%2B}/sha256sum-amd64.txt"
else
    echo "upstream digests not checked (pass --online)"
fi

echo
if [[ "$failures" -eq 0 ]]; then
    echo "All checks passed."
else
    echo "$failures check(s) failed."
    exit 1
fi
