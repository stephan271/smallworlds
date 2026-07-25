#!/usr/bin/env bash
# Build self-contained Bootstrap Launcher release archives from the embedded web
# client. This is a release-engineering command, never an Operator prerequisite.
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage:
  admin-tools/package-bootstrap-launcher.sh \
    --version vX.Y.Z --output-directory DIRECTORY [--source-date-epoch UNIX_SECONDS]

Builds Linux amd64/arm64, macOS amd64/arm64, and Windows amd64 Bootstrap
Launcher archives. The Svelte client must already have been built into
operator-console/internal/webui/dist by the release workflow. Each output set
contains SHA256SUMS, an SPDX SBOM, and third-party notices. No global tools are
required by somebody running an extracted launcher archive.
USAGE
}

die() {
    printf 'package-bootstrap-launcher: %s\n' "$*" >&2
    exit 1
}

version=""
output_directory=""
source_date_epoch="${SOURCE_DATE_EPOCH:-}"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) version="${2:-}"; shift 2 ;;
        --output-directory) output_directory="${2:-}"; shift 2 ;;
        --source-date-epoch) source_date_epoch="${2:-}"; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) die "unknown argument: $1" ;;
    esac
done

[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]] || die "--version must be a release tag such as v1.2.3"
[ -n "$output_directory" ] || die "--output-directory is required"

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
console_root="$repository_root/operator-console"
web_assets="$console_root/internal/webui/dist"
[ -f "$web_assets/index.html" ] || die "embedded Svelte client is missing; run npm ci && npm run build in operator-console/web first"
[ -f "$web_assets/console.html" ] || die "embedded Svelte console client is incomplete; run npm run build in operator-console/web first"
command -v go >/dev/null 2>&1 || die "Go is required only to create a release archive"
command -v tar >/dev/null 2>&1 || die "tar is required only to create a release archive"
command -v gzip >/dev/null 2>&1 || die "gzip is required only to create a release archive"
command -v zip >/dev/null 2>&1 || die "zip is required only to create the Windows release archive"

if [ -z "$source_date_epoch" ]; then
    source_date_epoch="$(git -C "$repository_root" log -1 --format=%ct 2>/dev/null || true)"
fi
[[ "$source_date_epoch" =~ ^[0-9]+$ ]] || die "--source-date-epoch (or SOURCE_DATE_EPOCH) must be a Unix timestamp"

mkdir -p "$output_directory"
output_directory="$(cd "$output_directory" && pwd)"
work_directory="$(mktemp -d)"
trap 'rm -rf "$work_directory"' EXIT

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

notices="$output_directory/THIRD-PARTY-NOTICES.txt"
sbom="$output_directory/smallworlds-bootstrap-launcher_${version}.spdx.json"

{
    printf '%s\n\n' 'SmallWorlds Bootstrap Launcher third-party notices'
    printf '%s\n\n' "Release: $version"
    printf '%s\n' 'This release contains the Go modules listed below. License texts are reproduced when the module declares one at its module root.'
} > "$notices"

printf '{\n  "spdxVersion": "SPDX-2.3",\n  "dataLicense": "CC0-1.0",\n  "SPDXID": "SPDXRef-DOCUMENT",\n  "name": "smallworlds-bootstrap-launcher-%s",\n  "documentNamespace": "https://smallworlds.example/spdx/bootstrap-launcher/%s",\n  "creationInfo": {"creators": ["Tool: package-bootstrap-launcher.sh"], "created": "1970-01-01T00:00:00Z"},\n  "packages": [' "$version" "$version" > "$sbom"

first_package=true
while IFS=$'\t' read -r module_path module_version module_directory; do
    [ -n "$module_path" ] || continue
    if [ "$first_package" = true ]; then
        first_package=false
    else
        printf ',' >> "$sbom"
    fi
    printf '\n    {"SPDXID":"SPDXRef-Package-%s","name":"%s","versionInfo":"%s","downloadLocation":"NOASSERTION","filesAnalyzed":false,"licenseConcluded":"NOASSERTION","licenseDeclared":"NOASSERTION","copyrightText":"NOASSERTION"}' "$(printf '%s' "$module_path" | tr '/.' '--')" "$module_path" "${module_version:-unknown}" >> "$sbom"

    printf '\n%s %s\n' "$module_path" "${module_version:-unknown}" >> "$notices"
    license_file="$(find "$module_directory" -maxdepth 1 -type f \( -iname 'license*' -o -iname 'copying*' -o -iname 'notice*' \) -print -quit 2>/dev/null || true)"
    if [ -n "$license_file" ]; then
        printf '%s\n' '----- license text -----' >> "$notices"
        cat "$license_file" >> "$notices"
        printf '\n%s\n' '----- end license text -----' >> "$notices"
    else
        printf '%s\n' 'License text was not present at the module root in the build cache; consult the module source.' >> "$notices"
    fi
done < <(cd "$console_root" && go list -m -f '{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}' all)
printf '\n  ]\n}\n' >> "$sbom"

build_target() {
    local goos="$1" goarch="$2" extension="" archive stage binary archive_name
    if [ "$goos" = windows ]; then
        extension=.exe
    fi
    archive_name="smallworlds-bootstrap-launcher_${version}_${goos}_${goarch}"
    stage="$work_directory/$archive_name"
    binary="smallworlds-admin$extension"
    mkdir -p "$stage"

    (
        cd "$console_root"
        CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
            -trimpath -buildvcs=false \
            -ldflags "-s -w -X github.com/stephan271/smallworlds/operator-console/internal/buildinfo.Version=$version" \
            -o "$stage/$binary" ./cmd/smallworlds-admin
    )
    printf '%s\n\n' 'SmallWorlds Bootstrap Launcher' > "$stage/README.txt"
    printf 'Version: %s\n\n' "$version" >> "$stage/README.txt"
    printf '%s\n\n' 'Run the included executable. It opens a secure local browser session and needs no Git, GitHub CLI, OpenTofu, Kubernetes tooling, or JavaScript runtime on this computer.' >> "$stage/README.txt"
    printf '%s\n' 'For integrity verification, download SHA256SUMS and SHA256SUMS.sig from the same signed release before extracting this archive.' >> "$stage/README.txt"
    touch -d "@$source_date_epoch" "$stage/$binary" "$stage/README.txt"

    if [ "$goos" = windows ]; then
        archive="$output_directory/$archive_name.zip"
        (cd "$stage" && zip -X -q "$archive" "$binary" README.txt)
    else
        archive="$output_directory/$archive_name.tar.gz"
        tar --sort=name --mtime="@$source_date_epoch" --owner=0 --group=0 --numeric-owner \
            -C "$stage" -cf - . | gzip -n > "$archive"
    fi
}

build_target linux amd64
build_target linux arm64
build_target darwin amd64
build_target darwin arm64
build_target windows amd64

checksums="$output_directory/SHA256SUMS"
: > "$checksums"
while IFS= read -r artifact; do
    name="$(basename "$artifact")"
    printf '%s  %s\n' "$(sha256 "$artifact")" "$name" >> "$checksums"
done < <(find "$output_directory" -maxdepth 1 -type f \( -name '*.tar.gz' -o -name '*.zip' -o -name '*.spdx.json' -o -name 'THIRD-PARTY-NOTICES.txt' \) -print | sort)

printf '%s\n' "$output_directory"
