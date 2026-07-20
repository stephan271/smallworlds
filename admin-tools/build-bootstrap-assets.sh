#!/usr/bin/env bash
# Build a deterministic, release-pinned bootstrap asset archive.
#
# This is a release-engineering command. It intentionally accepts no implicit
# "latest" versions and never reads credentials or operator configuration.
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage:
  admin-tools/build-bootstrap-assets.sh \
    --release vX.Y.Z \
    --k3s-version X.Y.Z+k3sN --k3s-installer-url HTTPS_URL --k3s-installer-sha256 SHA256 \
    --argocd-version vX.Y.Z --argocd-manifest-url HTTPS_URL --argocd-manifest-sha256 SHA256 \
    --output-directory DIRECTORY

Downloads exactly the declared K3s installer and Argo CD manifest, verifies
their SHA-256 digests, and writes a deterministic Linux amd64 asset archive.
The command accepts only direct HTTPS URLs without credentials, query strings,
or fragments. It does not sign or upload the resulting archive.
USAGE
}

die() {
    printf 'build-bootstrap-assets: %s\n' "$*" >&2
    exit 1
}

require_value() {
    [ "$#" -eq 2 ] || die "internal argument error"
    [ -n "$2" ] || die "$1 must not be empty"
}

validate_sha256() {
    [[ "$1" =~ ^[0-9a-f]{64}$ ]] || die "expected a 64-character lowercase SHA-256 digest"
}

validate_version() {
    [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._+~-]*$ ]] || die "invalid version: $1"
}

validate_https_url() {
    local url="$1" authority
    [[ "$url" =~ ^https://[^/?#]+(/[^?#]*)?$ ]] || die "URL must be an absolute HTTPS URL: $url"
    [[ "$url" != *'?'* && "$url" != *'#'* ]] || die "URL must not include a query string or fragment: $url"
    authority="${url#https://}"
    authority="${authority%%/*}"
    [[ "$authority" != *'@'* ]] || die "URL must not include credentials: $url"
}

download_verified() {
    local url="$1" expected_digest="$2" destination="$3" actual_digest
    curl --fail --location --proto '=https' --tlsv1.2 --output "$destination" "$url"
    actual_digest="$(sha256sum "$destination" | awk '{print $1}')"
    [ "$actual_digest" = "$expected_digest" ] || die "checksum mismatch for $url (expected $expected_digest, got $actual_digest)"
}

release=""
k3s_version=""
k3s_installer_url=""
k3s_installer_sha256=""
argocd_version=""
argocd_manifest_url=""
argocd_manifest_sha256=""
output_directory=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --release) release="${2:-}"; shift 2 ;;
        --k3s-version) k3s_version="${2:-}"; shift 2 ;;
        --k3s-installer-url) k3s_installer_url="${2:-}"; shift 2 ;;
        --k3s-installer-sha256) k3s_installer_sha256="${2:-}"; shift 2 ;;
        --argocd-version) argocd_version="${2:-}"; shift 2 ;;
        --argocd-manifest-url) argocd_manifest_url="${2:-}"; shift 2 ;;
        --argocd-manifest-sha256) argocd_manifest_sha256="${2:-}"; shift 2 ;;
        --output-directory) output_directory="${2:-}"; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

for required in \
    "--release:$release" \
    "--k3s-version:$k3s_version" \
    "--k3s-installer-url:$k3s_installer_url" \
    "--k3s-installer-sha256:$k3s_installer_sha256" \
    "--argocd-version:$argocd_version" \
    "--argocd-manifest-url:$argocd_manifest_url" \
    "--argocd-manifest-sha256:$argocd_manifest_sha256" \
    "--output-directory:$output_directory"; do
    require_value "${required%%:*}" "${required#*:}"
done

validate_version "$release"
validate_version "$k3s_version"
validate_version "$argocd_version"
validate_https_url "$k3s_installer_url"
validate_https_url "$argocd_manifest_url"
validate_sha256 "$k3s_installer_sha256"
validate_sha256 "$argocd_manifest_sha256"

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
bootstrap_script="$repository_root/infrastructure/local/bootstrap-local-node.sh"
[ -f "$bootstrap_script" ] || die "missing local bootstrap script: $bootstrap_script"
mkdir -p "$output_directory"

work_directory="$(mktemp -d)"
trap 'rm -rf "$work_directory"' EXIT
stage="$work_directory/smallworlds-bootstrap"
mkdir -p "$stage/third-party"

download_verified "$k3s_installer_url" "$k3s_installer_sha256" "$stage/third-party/k3s-install.sh"
download_verified "$argocd_manifest_url" "$argocd_manifest_sha256" "$stage/third-party/argocd-install.yaml"
printf '%s\n' "$k3s_version" > "$stage/third-party/k3s-version"
printf '%s\n' "$argocd_version" > "$stage/third-party/argocd-version"
install -m 0755 "$bootstrap_script" "$stage/bootstrap-local-node.sh"
install -m 0755 /dev/stdin "$stage/run-local-node-bootstrap.sh" <<'RUNNER'
#!/usr/bin/env sh
set -eu
asset_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export SMALLWORLDS_BOOTSTRAP_ASSET_DIR="$asset_directory"
exec "$asset_directory/bootstrap-local-node.sh" "$@"
RUNNER
printf '%s\n' "$release" > "$stage/VERSION"
cat > "$stage/metadata.json" <<METADATA
{
  "format": "smallworlds-bootstrap-payload/v1",
  "release": "$release",
  "platform": "linux-amd64",
  "inputs": [
    {
      "id": "k3s-installer",
      "version": "$k3s_version",
      "url": "$k3s_installer_url",
      "sha256": "$k3s_installer_sha256"
    },
    {
      "id": "argocd-install-manifest",
      "version": "$argocd_version",
      "url": "$argocd_manifest_url",
      "sha256": "$argocd_manifest_sha256"
    }
  ]
}
METADATA

archive="$output_directory/smallworlds-bootstrap-${release}-linux-amd64.tar.gz"
tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$stage" -cf - . | gzip -n > "$archive"

printf '%s\n' "$archive"
