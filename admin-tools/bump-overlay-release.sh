#!/usr/bin/env bash
# Move a SmallWorlds GitOps overlay from one base release to the next.
#
#   bump-overlay-release.sh <overlay-directory> <from-tag> <to-tag>
#
# The overlay is the only thing that decides which base release a cluster
# actually runs, and it names that release in more places than is comfortable to
# edit by hand. This does the mechanical part and the release-specific part, and
# keeps them apart:
#
#   1. The pinned base release. Every kustomization.yaml — the root one and one
#      per application — names the tag once per remote reference, and
#      overlay-config.yaml carries it again as the release the cluster reports
#      about itself. Missing that last one leaves a cluster running one release
#      while announcing the previous one, which nothing detects because nothing
#      depends on it.
#
#   2. Applications that joined the always-installed set. Those need an
#      Application patch in the root file and a file of their own; an overlay
#      established before they existed has neither, and a bare tag bump would
#      quietly leave them on the project's own hostnames.
#
# What it writes is byte-for-byte what the console's own overlay renderer
# produces for the same inputs, so a bumped overlay and a freshly established
# one are the same file rather than merely equivalent ones.
#
# Nothing is committed and nothing is pushed. Run it, read the diff, decide.
set -euo pipefail

OVERLAY="${1:?usage: bump-overlay-release.sh <overlay-directory> <from-tag> <to-tag>}"
FROM="${2:?missing from-tag}"
TO="${3:?missing to-tag}"

for tag in "$FROM" "$TO"; do
    if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo "not a release tag: $tag" >&2
        exit 1
    fi
done
if [ ! -f "$OVERLAY/kustomization.yaml" ]; then
    echo "no overlay kustomization in $OVERLAY" >&2
    exit 1
fi

# True when the target release is at least the given one. Used to decide which
# of the migrations below apply, so bumping several releases at once still
# picks up everything on the way.
version_at_least() {
    [ "$(printf '%s\n%s\n' "$1" "$TO" | sort -V | head -1)" = "$1" ]
}

# --- 1. the pinned base release ------------------------------------------
touched=0
while IFS= read -r file; do
    if grep -q "$FROM" "$file"; then
        sed -i "s|/$FROM/|/$TO/|g; s|?ref=$FROM|?ref=$TO|g; s|smallworldsRelease: $FROM|smallworldsRelease: $TO|g" "$file"
        echo "  pinned $TO in ${file#"$OVERLAY"/}"
        touched=$((touched + 1))
    fi
done < <(find "$OVERLAY" \( -name kustomization.yaml -o -name overlay-config.yaml \) -not -path "*/.git/*" | sort)

if [ "$touched" -eq 0 ]; then
    echo "nothing referenced $FROM — is the overlay already on $TO?" >&2
    exit 1
fi

# --- 2. headscale, part of the always-installed set since v1.2.30 ---------
# The domain is read from an existing application rather than passed in: the
# overlay already knows which hostnames it uses, and asking again would only be
# an opportunity for the answer to disagree with the file.
if version_at_least v1.2.30 && [ ! -f "$OVERLAY/headscale/kustomization.yaml" ]; then
    DOMAIN="$(sed -n 's|^ *value: dashboard\.\(.*\)$|\1|p' "$OVERLAY/dashboard/kustomization.yaml" | head -1)"
    if [ -z "$DOMAIN" ]; then
        echo "could not read the cluster's domain from dashboard/kustomization.yaml" >&2
        exit 1
    fi
    echo "  domain: $DOMAIN"
    mkdir -p "$OVERLAY/headscale"
    cat > "$OVERLAY/headscale/kustomization.yaml" <<HEADSCALE
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/headscale?ref=$TO
patches:
  - target:
      kind: Ingress
      name: headscale
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: vpn.$DOMAIN
      - op: replace
        path: /spec/tls/0/hosts/0
        value: vpn.$DOMAIN
  - target:
      kind: Deployment
      name: headscale
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/0/value
        value: https://vpn.$DOMAIN
HEADSCALE
    echo "  created headscale/kustomization.yaml"
fi

# The root file lists Application patches alphabetically. Inserted with python
# rather than sed: placing an indented multi-line block at a computed position
# is exactly where a stream editor starts guessing.
if version_at_least v1.2.30 && ! grep -q "name: headscale" "$OVERLAY/kustomization.yaml"; then
    python3 - "$OVERLAY/kustomization.yaml" <<'PYTHON'
import re
import sys

path = sys.argv[1]
text = open(path).read()
block = """  - target:
      group: argoproj.io
      kind: Application
      name: headscale
    patch: |-
      - op: replace
        path: /spec/source/repoURL
        value: {repository}
      - op: replace
        path: /spec/source/path
        value: headscale
"""
repository = re.search(r"value: (https://\S+?)\n", text)
if not repository:
    sys.exit("could not read the overlay's own repository URL from the root kustomization")
block = block.format(repository=repository.group(1))

entries = list(re.finditer(r"  - target:\n      group: argoproj\.io\n      kind: Application\n      name: (\S+)\n", text))
successor = next((entry for entry in entries if entry.group(1) > "headscale"), None)
if successor:
    text = text[: successor.start()] + block + text[successor.start() :]
else:
    text = text.rstrip("\n") + "\n" + block
open(path, "w").write(text)
print("  added the headscale Application patch to kustomization.yaml")
PYTHON
fi

echo
echo "Done. Nothing was committed. Review with:  git -C $OVERLAY diff"
