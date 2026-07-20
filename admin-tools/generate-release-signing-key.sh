#!/usr/bin/env bash
# Generate release-signing material for the GitHub Actions repository secret.
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: admin-tools/generate-release-signing-key.sh --output-directory DIRECTORY

Creates a private Ed25519 PEM key, a base64-encoded copy suitable for the
SMALLWORLDS_RELEASE_ED25519_PRIVATE_KEY_B64 GitHub Actions secret, and the raw
base64 public key that must be reviewed and compiled into the Launcher catalog.
The output directory must not already contain release-signing files.
USAGE
}

die() {
    printf 'generate-release-signing-key: %s\n' "$*" >&2
    exit 1
}

output_directory=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output-directory) output_directory="${2:-}"; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[ -n "$output_directory" ] || die "--output-directory is required"
command -v openssl >/dev/null 2>&1 || die "openssl is required"
mkdir -p -m 700 "$output_directory"
private_key="$output_directory/smallworlds-release-ed25519.pem"
private_key_b64="$output_directory/smallworlds-release-ed25519-private.b64"
public_key_b64="$output_directory/smallworlds-release-ed25519-public.b64"
for path in "$private_key" "$private_key_b64" "$public_key_b64"; do
    [ ! -e "$path" ] || die "refusing to overwrite $path"
done

umask 077
openssl genpkey -algorithm ED25519 -out "$private_key"
base64 -w0 "$private_key" > "$private_key_b64"
printf '\n' >> "$private_key_b64"
openssl pkey -in "$private_key" -pubout -outform DER | tail -c 32 | base64 -w0 > "$public_key_b64"
printf '\n' >> "$public_key_b64"
chmod 600 "$private_key" "$private_key_b64" "$public_key_b64"

printf 'Private key: %s\n' "$private_key"
printf 'GitHub Actions secret value: %s\n' "$private_key_b64"
printf 'Launcher public key value: %s\n' "$public_key_b64"
