#!/usr/bin/env bash
# A local release-packaging smoke test. It intentionally uses no network.
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
builder="$repository_root/admin-tools/package-bootstrap-launcher.sh"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

"$builder" --version v9.8.7 --source-date-epoch 0 --output-directory "$temporary_directory/out" >/dev/null
"$builder" --version v9.8.7 --source-date-epoch 0 --output-directory "$temporary_directory/rebuilt" >/dev/null
cmp "$temporary_directory/out/SHA256SUMS" "$temporary_directory/rebuilt/SHA256SUMS"

for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
    archive="$temporary_directory/out/smallworlds-bootstrap-launcher_v9.8.7_$target.tar.gz"
    [ -f "$archive" ]
    tar -tzf "$archive" | grep -Fx './smallworlds-admin' >/dev/null
    tar -tzf "$archive" | grep -Fx './README.txt' >/dev/null
done
mkdir "$temporary_directory/linux-amd64"
tar -xzf "$temporary_directory/out/smallworlds-bootstrap-launcher_v9.8.7_linux_amd64.tar.gz" -C "$temporary_directory/linux-amd64"
[ "$("$temporary_directory/linux-amd64/smallworlds-admin" --version)" = v9.8.7 ]
windows_archive="$temporary_directory/out/smallworlds-bootstrap-launcher_v9.8.7_windows_amd64.zip"
[ -f "$windows_archive" ]
unzip -Z1 "$windows_archive" | grep -Fx 'smallworlds-admin.exe' >/dev/null
[ -s "$temporary_directory/out/SHA256SUMS" ]
grep -F 'smallworlds-bootstrap-launcher_v9.8.7.spdx.json' "$temporary_directory/out/SHA256SUMS" >/dev/null
grep -F 'THIRD-PARTY-NOTICES.txt' "$temporary_directory/out/SHA256SUMS" >/dev/null
