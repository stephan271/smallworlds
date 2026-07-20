#!/usr/bin/env bash
# Build and sign the bootstrap release files that are attached to a GitHub Release.
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage:
  admin-tools/publish-bootstrap-assets.sh \
    --release vX.Y.Z \
    --inputs-file docs/releases/bootstrap-inputs/vX.Y.Z.json \
    --signing-key /secure/path/release-ed25519.pem \
    --output-directory DIRECTORY

The inputs file is a reviewed, checked-in lock file. This command builds the
archive, writes its SHA-256, signs the ASCII SHA-256 text, and writes the public
GitHub Release manifest. It never creates a GitHub Release or reads credentials.
USAGE
}

die() {
    printf 'publish-bootstrap-assets: %s\n' "$*" >&2
    exit 1
}

release=""
inputs_file=""
signing_key=""
output_directory=""

while [ "$#" -gt 0 ]; do
    case "$1" in
        --release) release="${2:-}"; shift 2 ;;
        --inputs-file) inputs_file="${2:-}"; shift 2 ;;
        --signing-key) signing_key="${2:-}"; shift 2 ;;
        --output-directory) output_directory="${2:-}"; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[ -n "$release" ] || die "--release is required"
[ -f "$inputs_file" ] || die "--inputs-file must name an existing file"
[ -f "$signing_key" ] || die "--signing-key must name an existing private key"
[ -n "$output_directory" ] || die "--output-directory is required"
command -v jq >/dev/null 2>&1 || die "jq is required"
command -v openssl >/dev/null 2>&1 || die "openssl is required"

locked_release="$(jq -er '.release | strings' "$inputs_file")" || die "inputs file must contain a string release"
[ "$locked_release" = "$release" ] || die "inputs file release does not match --release"

k3s_version="$(jq -er '.k3s.version | strings' "$inputs_file")"
k3s_url="$(jq -er '.k3s.installerUrl | strings' "$inputs_file")"
k3s_sha256="$(jq -er '.k3s.installerSHA256 | strings' "$inputs_file")"
argocd_version="$(jq -er '.argocd.version | strings' "$inputs_file")"
argocd_url="$(jq -er '.argocd.manifestUrl | strings' "$inputs_file")"
argocd_sha256="$(jq -er '.argocd.manifestSHA256 | strings' "$inputs_file")"

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
builder="$repository_root/admin-tools/build-bootstrap-assets.sh"
[ -x "$builder" ] || die "bootstrap asset builder is not executable"
mkdir -p "$output_directory"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

archive="$($builder \
    --release "$release" \
    --k3s-version "$k3s_version" \
    --k3s-installer-url "$k3s_url" \
    --k3s-installer-sha256 "$k3s_sha256" \
    --argocd-version "$argocd_version" \
    --argocd-manifest-url "$argocd_url" \
    --argocd-manifest-sha256 "$argocd_sha256" \
    --output-directory "$output_directory")"
archive_name="$(basename "$archive")"
archive_sha256="$(sha256sum "$archive" | awk '{print $1}')"

printf '%s  %s\n' "$archive_sha256" "$archive_name" > "$output_directory/$archive_name.sha256"
printf '%s' "$archive_sha256" > "$temporary_directory/archive-sha256.txt"
openssl pkeyutl -sign -rawin -inkey "$signing_key" \
    -in "$temporary_directory/archive-sha256.txt" \
    -out "$temporary_directory/archive-sha256.sig"
base64 -w0 "$temporary_directory/archive-sha256.sig" > "$output_directory/$archive_name.sig"
printf '\n' >> "$output_directory/$archive_name.sig"

public_key="$(openssl pkey -in "$signing_key" -pubout -outform DER | tail -c 32 | base64 -w0)"
signature="$(tr -d '\n' < "$output_directory/$archive_name.sig")"
jq -n \
    --arg release "$release" \
    --arg id "bootstrap-linux-amd64" \
    --arg url "https://github.com/stephan271/smallworlds/releases/download/$release/$archive_name" \
    --arg sha256 "$archive_sha256" \
    --arg signature "$signature" \
    --arg public_key "$public_key" \
    '{format: "smallworlds-bootstrap-assets/v1", release: $release, assets: [{id: $id, url: $url, sha256: $sha256, signature: $signature, destination: "github.com"}], signingPublicKey: $public_key}' \
    > "$output_directory/bootstrap-assets.manifest.json"

printf '%s\n' "$archive"
