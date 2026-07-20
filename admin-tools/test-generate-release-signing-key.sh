#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
generator="$repository_root/admin-tools/generate-release-signing-key.sh"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

output_directory="$temporary_directory/signing"
"$generator" --output-directory "$output_directory" >/dev/null
private_key="$output_directory/smallworlds-release-ed25519.pem"
private_key_b64="$output_directory/smallworlds-release-ed25519-private.b64"
public_key_b64="$output_directory/smallworlds-release-ed25519-public.b64"
[ -f "$private_key" ] && [ -f "$private_key_b64" ] && [ -f "$public_key_b64" ]
[ "$(stat -c '%a' "$private_key")" = '600' ]
[ "$(base64 -d "$private_key_b64" | cmp -s - "$private_key"; printf '%s' "$?")" = '0' ]
[ "$(base64 -d "$public_key_b64" | wc -c)" -eq 32 ]
openssl pkey -in "$private_key" -noout >/dev/null 2>&1

if "$generator" --output-directory "$output_directory" >/dev/null 2>&1; then
    echo 'expected existing signing material rejection' >&2
    exit 1
fi

echo 'generate-release-signing-key: tests passed'
