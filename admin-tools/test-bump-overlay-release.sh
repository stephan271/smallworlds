#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
bumper="$repository_root/admin-tools/bump-overlay-release.sh"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

# A minimal overlay in the shape the console establishes: the root file with
# alphabetical Application patches, one file per application, and the config map
# that carries the release a second time.
overlay="$temporary_directory/overlay"
mkdir -p "$overlay/dashboard" "$overlay/immich"
cat > "$overlay/kustomization.yaml" <<'ROOT'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - overlay-config.yaml
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=v1.2.29
  - https://raw.githubusercontent.com/stephan271/smallworlds/v1.2.29/infrastructure/kubernetes/apps/immich.yaml
patches:
  - target:
      group: argoproj.io
      kind: Application
      name: dashboard
    patch: |-
      - op: replace
        path: /spec/source/repoURL
        value: https://github.com/octocat/overlay.git
      - op: replace
        path: /spec/source/path
        value: dashboard
  - target:
      group: argoproj.io
      kind: Application
      name: immich
    patch: |-
      - op: replace
        path: /spec/source/repoURL
        value: https://github.com/octocat/overlay.git
      - op: replace
        path: /spec/source/path
        value: immich
ROOT
cat > "$overlay/overlay-config.yaml" <<'CONFIG'
apiVersion: v1
kind: ConfigMap
metadata:
  name: smallworlds-overlay
  namespace: default
data:
  baseDomain: home.example
  deploymentMode: local-lan
  smallworldsRelease: v1.2.29
CONFIG
cat > "$overlay/dashboard/kustomization.yaml" <<'DASHBOARD'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/dashboard?ref=v1.2.29
patches:
  - target:
      kind: Ingress
      name: dashboard
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: dashboard.home.example
DASHBOARD
cat > "$overlay/immich/kustomization.yaml" <<'IMMICH'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/immich?ref=v1.2.29
IMMICH

"$bumper" "$overlay" v1.2.29 v1.2.30 >/dev/null

# Every reference moved, in all three shapes the tag appears in.
[ "$(grep -rc 'v1\.2\.29' "$overlay" | grep -vc ':0$')" = '0' ]
grep -q '?ref=v1.2.30' "$overlay/kustomization.yaml"
grep -q '/v1.2.30/infrastructure/kubernetes/apps/immich.yaml' "$overlay/kustomization.yaml"
grep -q 'smallworldsRelease: v1.2.30' "$overlay/overlay-config.yaml"
grep -q '?ref=v1.2.30' "$overlay/dashboard/kustomization.yaml"

# Headscale arrives with the release, on the overlay's own domain — read from
# the overlay rather than supplied, so it cannot disagree with the other hosts.
grep -q 'value: vpn.home.example' "$overlay/headscale/kustomization.yaml"
grep -q 'value: https://vpn.home.example' "$overlay/headscale/kustomization.yaml"
grep -q '?ref=v1.2.30' "$overlay/headscale/kustomization.yaml"
grep -q 'value: https://github.com/octocat/overlay.git' "$overlay/kustomization.yaml"

# Alphabetical placement: headscale between dashboard and immich, or Argo CD
# would apply the patches in an order the console never produces.
order="$(grep -oP '(?<=      name: )\S+' "$overlay/kustomization.yaml" | tr '\n' ' ')"
[ "$order" = 'dashboard headscale immich ' ] || {
    echo "Application patches are out of order: $order" >&2
    exit 1
}

# Idempotent: bumping again neither duplicates headscale nor rewrites its file.
before="$(cat "$overlay/kustomization.yaml")"
"$bumper" "$overlay" v1.2.30 v1.2.31 >/dev/null
[ "$(grep -c 'name: headscale' "$overlay/kustomization.yaml")" = '1' ]
[ "$(grep -c '?ref=v1.2.31' "$overlay/headscale/kustomization.yaml")" = '1' ]
[ "$before" != "$(cat "$overlay/kustomization.yaml")" ]

# An overlay that never mentions the from-tag is a mistake worth stopping for:
# silently succeeding would report a bump that did not happen.
if "$bumper" "$overlay" v1.2.20 v1.2.32 >/dev/null 2>&1; then
    echo 'expected refusal when nothing references the from-tag' >&2
    exit 1
fi
if "$bumper" "$overlay" v1.2.31 not-a-tag >/dev/null 2>&1; then
    echo 'expected refusal of a malformed target tag' >&2
    exit 1
fi
if "$bumper" "$temporary_directory/absent" v1.2.31 v1.2.32 >/dev/null 2>&1; then
    echo 'expected refusal when the directory holds no overlay' >&2
    exit 1
fi

# Bumping between releases older than headscale leaves it out entirely.
legacy="$temporary_directory/legacy"
cp -a "$temporary_directory/overlay" "$legacy"
rm -rf "$legacy/headscale"
python3 - "$legacy/kustomization.yaml" <<'PYTHON'
import re, sys
path = sys.argv[1]
text = open(path).read()
text = re.sub(r"  - target:\n      group: argoproj\.io\n      kind: Application\n      name: headscale\n(?:.*\n)*?        value: headscale\n", "", text)
text = text.replace("v1.2.31", "v1.2.27")
open(path, "w").write(text)
PYTHON
"$bumper" "$legacy" v1.2.27 v1.2.28 >/dev/null
[ ! -e "$legacy/headscale" ]
if grep -q 'name: headscale' "$legacy/kustomization.yaml"; then
    echo 'headscale was added to an overlay bumped to a release that predates it' >&2
    exit 1
fi

echo 'bump-overlay-release: tests passed'
