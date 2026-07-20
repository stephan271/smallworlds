#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
builder="$repository_root/admin-tools/build-bootstrap-assets.sh"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

fixture_directory="$temporary_directory/fixtures"
mock_bin="$temporary_directory/bin"
output_one="$temporary_directory/one"
output_two="$temporary_directory/two"
mkdir -p "$fixture_directory" "$mock_bin" "$output_one" "$output_two"
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
arguments=(
    --release v1.2.24
    --k3s-version v1.31.5+k3s1
    --k3s-installer-url https://get.k3s.io
    --k3s-installer-sha256 "$k3s_sha"
    --argocd-version v2.14.5
    --argocd-manifest-url https://releases.example.test/argocd-install.yaml
    --argocd-manifest-sha256 "$argocd_sha"
)

PATH="$mock_bin:$PATH" FIXTURE_DIRECTORY="$fixture_directory" "$builder" "${arguments[@]}" --output-directory "$output_one" >/dev/null
PATH="$mock_bin:$PATH" FIXTURE_DIRECTORY="$fixture_directory" "$builder" "${arguments[@]}" --output-directory "$output_two" >/dev/null

archive_one="$output_one/smallworlds-bootstrap-v1.2.24-linux-amd64.tar.gz"
archive_two="$output_two/smallworlds-bootstrap-v1.2.24-linux-amd64.tar.gz"
[ -f "$archive_one" ]
[ "$(sha256sum "$archive_one" | awk '{print $1}')" = "$(sha256sum "$archive_two" | awk '{print $1}')" ]

expected_entries=$'./\n./VERSION\n./bootstrap-local-node.sh\n./metadata.json\n./run-local-node-bootstrap.sh\n./third-party/\n./third-party/argocd-install.yaml\n./third-party/k3s-install.sh'
[ "$(tar -tzf "$archive_one")" = "$expected_entries" ]
[ "$(tar -xOzf "$archive_one" ./VERSION)" = 'v1.2.24' ]
tar -xOzf "$archive_one" ./metadata.json | grep -F '"release": "v1.2.24"' >/dev/null
tar -xOzf "$archive_one" ./metadata.json | grep -F '"version": "v1.31.5+k3s1"' >/dev/null

if PATH="$mock_bin:$PATH" FIXTURE_DIRECTORY="$fixture_directory" "$builder" \
    --release v1.2.24 \
    --k3s-version v1.31.5+k3s1 \
    --k3s-installer-url 'https://releases.example.test/k3s-install.sh?token=no' \
    --k3s-installer-sha256 "$k3s_sha" \
    --argocd-version v2.14.5 \
    --argocd-manifest-url https://releases.example.test/argocd-install.yaml \
    --argocd-manifest-sha256 "$argocd_sha" \
    --output-directory "$temporary_directory/rejected" >/dev/null 2>&1; then
    echo 'expected credential/query URL rejection' >&2
    exit 1
fi

echo 'build-bootstrap-assets: tests passed'
