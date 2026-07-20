#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
publisher="$repository_root/admin-tools/publish-bootstrap-assets.sh"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

fixture_directory="$temporary_directory/fixtures"
mock_bin="$temporary_directory/bin"
output_directory="$temporary_directory/output"
mkdir -p "$fixture_directory" "$mock_bin" "$output_directory"
printf '%s\n' 'reviewed k3s installer fixture' > "$fixture_directory/k3s-install.sh"
printf '%s\n' 'apiVersion: v1' 'kind: ConfigMap' > "$fixture_directory/argocd-install.yaml"

cat > "$mock_bin/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
output=""
url=""
while [ "$#" -gt 0 ]; do
    case "$1" in
        --output) output="$2"; shift 2 ;;
        --fail|--location|--tlsv1.2) shift ;;
        --proto) shift 2 ;;
        *) url="$1"; shift ;;
    esac
done
case "$url" in
    *k3s*) cp "$FIXTURE_DIRECTORY/k3s-install.sh" "$output" ;;
    *argocd*) cp "$FIXTURE_DIRECTORY/argocd-install.yaml" "$output" ;;
    *) exit 9 ;;
esac
CURL
chmod +x "$mock_bin/curl"

k3s_sha="$(sha256sum "$fixture_directory/k3s-install.sh" | awk '{print $1}')"
argocd_sha="$(sha256sum "$fixture_directory/argocd-install.yaml" | awk '{print $1}')"
inputs_file="$temporary_directory/v1.2.24.json"
jq -n \
    --arg k3s_sha "$k3s_sha" \
    --arg argocd_sha "$argocd_sha" \
    '{release: "v1.2.24", k3s: {version: "v1.31.5+k3s1", installerUrl: "https://releases.example.test/k3s-install.sh", installerSHA256: $k3s_sha}, argocd: {version: "v2.14.5", manifestUrl: "https://releases.example.test/argocd-install.yaml", manifestSHA256: $argocd_sha}}' \
    > "$inputs_file"

private_key="$temporary_directory/release-ed25519.pem"
public_key="$temporary_directory/release-ed25519-public.pem"
openssl genpkey -algorithm ED25519 -out "$private_key" >/dev/null 2>&1
openssl pkey -in "$private_key" -pubout -out "$public_key" >/dev/null 2>&1
PATH="$mock_bin:$PATH" FIXTURE_DIRECTORY="$fixture_directory" "$publisher" \
    --release v1.2.24 \
    --inputs-file "$inputs_file" \
    --signing-key "$private_key" \
    --output-directory "$output_directory" >/dev/null

archive="$output_directory/smallworlds-bootstrap-v1.2.24-linux-amd64.tar.gz"
checksum_file="$archive.sha256"
signature_file="$archive.sig"
manifest="$output_directory/bootstrap-assets.manifest.json"
[ -f "$archive" ] && [ -f "$checksum_file" ] && [ -f "$signature_file" ] && [ -f "$manifest" ]
digest="$(sha256sum "$archive" | awk '{print $1}')"
grep -F "$digest  $(basename "$archive")" "$checksum_file" >/dev/null
printf '%s' "$digest" > "$temporary_directory/digest.txt"
base64 -d "$signature_file" > "$temporary_directory/signature.bin"
openssl pkeyutl -verify -rawin -pubin -inkey "$public_key" -in "$temporary_directory/digest.txt" -sigfile "$temporary_directory/signature.bin" >/dev/null
[ "$(jq -r '.assets[0].sha256' "$manifest")" = "$digest" ]
[ "$(jq -r '.assets[0].url' "$manifest")" = "https://github.com/stephan271/smallworlds/releases/download/v1.2.24/$(basename "$archive")" ]
[ "$(jq -r '.signingPublicKey' "$manifest" | base64 -d | wc -c)" -eq 32 ]

if PATH="$mock_bin:$PATH" FIXTURE_DIRECTORY="$fixture_directory" "$publisher" \
    --release v9.9.9 \
    --inputs-file "$inputs_file" \
    --signing-key "$private_key" \
    --output-directory "$temporary_directory/rejected" >/dev/null 2>&1; then
    echo 'expected mismatched release rejection' >&2
    exit 1
fi

echo 'publish-bootstrap-assets: tests passed'
