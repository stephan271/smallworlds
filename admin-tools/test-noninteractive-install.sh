#!/usr/bin/env bash
# Asserts the prompting contract that admin-tools/lib/ask.sh establishes, which
# is what lets the browser setup wizard drive the two setup scripts:
#
#   1. SMALLWORLDS_NONINTERACTIVE=1 never reads stdin and never blocks.
#   2. A missing required value exits 78 naming the variable, rather than
#      installing a cluster configured with a blank domain.
#   3. Secrets are never echoed, in either direction.
#   4. Interactive mode is UNCHANGED — same prompts, same pre-filled defaults,
#      same order, and no progress markers leaking into a human's terminal.
#
# Nothing here touches a real cloud account: the two scripts run against stub
# binaries in a throwaway HOME, and stop at the first call that would.
set -uo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT

failures=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; failures=$((failures + 1)); }

check_contains() {
    local haystack="$1" needle="$2" label="$3"
    if [[ "$haystack" == *"$needle"* ]]; then pass "$label"; else
        fail "$label (expected to find: $needle)"
    fi
}

check_absent() {
    local haystack="$1" needle="$2" label="$3"
    if [[ "$haystack" != *"$needle"* ]]; then pass "$label"; else
        fail "$label (unexpectedly found: $needle)"
    fi
}

# --------------------------------------------------------------------------
# A throwaway environment: stub binaries for everything that would reach a
# cloud account, and a HOME with an SSH key so no real one is read.
# --------------------------------------------------------------------------
stub_bin="$temporary_directory/bin"
stub_home="$temporary_directory/home"
mkdir -p "$stub_bin" "$stub_home/.ssh" "$stub_home/.config/smallworlds"
for stub in curl terraform ssh scp kubectl jq; do
    printf '#!/bin/sh\nexit 0\n' > "$stub_bin/$stub"
    chmod +x "$stub_bin/$stub"
done
printf 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITESTKEYTESTKEYTESTKEYTESTKEY test\n' \
    > "$stub_home/.ssh/id_ed25519.pub"

# smallworlds-init.sh sed-edits a tracked manifest and writes its cache file
# beside itself, so it runs from a copy rather than the working tree.
init_root="$temporary_directory/init-root"
mkdir -p "$init_root/admin-tools" "$init_root/infrastructure/kubernetes/tenants/keycloak"
cp "$repository_root/smallworlds-init.sh" "$init_root/"
cp -r "$repository_root/admin-tools/lib" "$init_root/admin-tools/"
cp "$repository_root/infrastructure/kubernetes/tenants/keycloak/realm-config-job.yaml" \
   "$init_root/infrastructure/kubernetes/tenants/keycloak/"

write_init_cache() {
    cat > "$init_root/.smallworlds-cache.env" <<CACHE
DEPLOY_TARGET="hetzner"
DOMAIN="${1-example.org}"
ENV_EXT=""
ADMIN_EMAIL="admin@example.org"
ONBOARDING_MODE="invitation"
HCLOUD_TOKEN="hetzner-token-should-never-be-printed"
GITOPS_REPO_URL="https://github.com/example/my-community-config.git"
GITOPS_REPO_USER="example"
GITOPS_REPO_TOKEN="git-token-should-never-be-printed"
CACHE
}

run_init() {
    ( cd "$init_root" \
      && env PATH="$stub_bin:$PATH" HOME="$stub_home" "$@" \
         timeout 60 bash smallworlds-init.sh </dev/null 2>&1 )
}

# --------------------------------------------------------------------------
echo "smallworlds-init.sh — non-interactive"
# --------------------------------------------------------------------------
write_init_cache
output="$(run_init SMALLWORLDS_NONINTERACTIVE=1)"

for expected in \
    "DEPLOY_TARGET: hetzner" \
    "DOMAIN: example.org" \
    "ADMIN_EMAIL: admin@example.org" \
    "ONBOARDING_MODE: invitation" \
    "ENV_EXT: (empty)" \
    "GITOPS_REPO_URL: https://github.com/example/my-community-config.git" \
    "GITOPS_REPO_USER: example"
do
    check_contains "$output" "$expected" "took $expected"
done
check_contains "$output" "GITOPS_REPO_TOKEN: [supplied]" "git token masked"
check_contains "$output" "HCLOUD_TOKEN: [supplied]"      "hetzner token masked"
check_absent "$output" "should-never-be-printed" "no secret value reached stdout"
check_absent "$output" "(blank=keep, type - to clear)" "no prompt was rendered"
check_contains "$output" ">>> step:dns-zone" "reached provisioning, so every question was answered"
check_contains "$output" ">>> ok:dns-zone"   "emitted a completion marker"

# --------------------------------------------------------------------------
echo "smallworlds-init.sh — a missing required value"
# --------------------------------------------------------------------------
write_init_cache ""
output="$(run_init SMALLWORLDS_NONINTERACTIVE=1)"
status=$?
check_contains "$output" "!!! missing:DOMAIN" "named the missing variable"
check_absent "$output" ">>> step:dns-zone" "stopped before provisioning anything"
if [[ "$status" -eq 78 ]]; then
    pass "exited 78 (EX_CONFIG)"
else
    fail "exited 78 (EX_CONFIG) — got $status"
fi

# --------------------------------------------------------------------------
echo "smallworlds-init.sh — external object storage"
# --------------------------------------------------------------------------
# No overlay record exists in this HOME yet, so the installer has to ask for
# everything itself — and has to notice when the provider is not described.
write_init_cache
echo 'OBJECT_STORAGE="external"' >> "$init_root/.smallworlds-cache.env"
output="$(run_init SMALLWORLDS_NONINTERACTIVE=1)"
status=$?
check_contains "$output" "!!! missing:S3_ENDPOINT" "external storage without an endpoint is refused by name"
check_absent "$output" ">>> step:dns-zone" "stopped before provisioning anything"
if [[ "$status" -eq 78 ]]; then pass "exited 78 (EX_CONFIG)"; else fail "exited 78 (EX_CONFIG) — got $status"; fi

write_init_cache
cat >> "$init_root/.smallworlds-cache.env" <<CACHE
OBJECT_STORAGE="external"
S3_ENDPOINT="https://s3.provider.example"
S3_REGION="eu-central-1"
S3_BUCKET_PREFIX="sw-example-org"
S3_ACCESS_KEY_ID="AKIDEXAMPLE"
S3_SECRET_ACCESS_KEY="s3-secret-should-never-be-printed"
CACHE
output="$(run_init SMALLWORLDS_NONINTERACTIVE=1)"
check_contains "$output" "OBJECT_STORAGE: external" "took the object storage choice"
check_contains "$output" "S3_ENDPOINT: https://s3.provider.example" "took the endpoint"
check_contains "$output" "S3_SECRET_ACCESS_KEY: [supplied]" "S3 secret masked"
check_absent "$output" "should-never-be-printed" "no secret value reached stdout"
check_absent "$output" "S3_BACKUP_ACCESS_KEY_ID" "no backup key asked for when backups share the provider"
check_contains "$output" ">>> step:dns-zone" "reached provisioning, so every question was answered"

# --------------------------------------------------------------------------
echo "smallworlds-init.sh — DNS kept by hand (no Hetzner account)"
# --------------------------------------------------------------------------
# A rented server whose operator keeps one wildcard record themselves
# (docs/adr/0056). The ssh stub answers the "what is your public address"
# question; identity.example.org does not resolve to it, here or anywhere.
write_rented_cache() {
    write_init_cache
    cat >> "$init_root/.smallworlds-cache.env" <<CACHE
DEPLOY_TARGET="local"
LOCAL_SSH_TARGET="root@203.0.113.9"
LOCAL_TLS="letsencrypt"
LOCAL_PUBLIC="yes"
LOCAL_FIREWALL="yes"
LOCAL_DNS="$1"
HCLOUD_TOKEN=""
CACHE
}
printf '#!/bin/sh\necho 203.0.113.9\n' > "$stub_bin/ssh"

write_rented_cache hetzner
output="$(run_init SMALLWORLDS_NONINTERACTIVE=1)"
check_contains "$output" "!!! missing:HCLOUD_TOKEN" "managed DNS still requires the Hetzner token"

write_rented_cache manual
output="$(run_init SMALLWORLDS_NONINTERACTIVE=1)"
status=$?
check_contains "$output" "LOCAL_DNS: manual" "took DNS by hand"
check_absent "$output" "!!! missing:HCLOUD_TOKEN" "DNS by hand asks for no Hetzner token"
check_contains "$output" ">>> fail:dns-zone" "a record that does not point at the server stops the install"
check_contains "$output" "A record named * (a wildcard) for example.org with the value 203.0.113.9" \
    "and says exactly which record to create"
check_absent "$output" ">>> step:bootstrap" "before the machine is touched"
if [[ "$status" -eq 1 ]]; then pass "exited 1, not 78: nothing was missing"; else
    fail "exited 1, not 78 — got $status"
fi

output="$(run_init SMALLWORLDS_NONINTERACTIVE=1 SW_SKIP_DNS_CHECK=1)"
check_contains "$output" ">>> ok:dns-zone" "with the record in place the install goes on"
check_absent "$output" "Ensuring the Hetzner DNS zone exists" "and never touches Hetzner DNS"
printf '#!/bin/sh\nexit 0\n' > "$stub_bin/ssh"

# --------------------------------------------------------------------------
echo "smallworlds-init.sh — interactive is unchanged"
# --------------------------------------------------------------------------
# read -p only renders its prompt on a terminal, so this needs a real pty:
# a pipe would pass for all the wrong reasons.
cat > "$temporary_directory/pty-drive.py" <<'PYTHON'
import os, pty, select, sys, time

argv = sys.argv[2:]
working_directory = sys.argv[1]
process_id, descriptor = pty.fork()
if process_id == 0:
    os.chdir(working_directory)
    os.execvp(argv[0], argv)

captured = bytearray()
deadline = time.time() + 40
while time.time() < deadline:
    readable, _, _ = select.select([descriptor], [], [], 0.4)
    if readable:
        try:
            chunk = os.read(descriptor, 4096)
        except OSError:
            break
        if not chunk:
            break
        captured += chunk
    else:
        # Nothing for a moment: assume a prompt is waiting, and accept its
        # default by pressing return.
        try:
            os.write(descriptor, b"\n")
        except OSError:
            break
try:
    os.close(descriptor)
except OSError:
    pass
os.waitpid(process_id, 0)
sys.stdout.write(captured.decode("utf-8", "replace"))
PYTHON

write_init_cache
output="$(PATH="$stub_bin:$PATH" HOME="$stub_home" timeout 60 \
    python3 "$temporary_directory/pty-drive.py" "$init_root" bash smallworlds-init.sh 2>&1)"

for prompt in \
    "1. Select deployment target (hetzner or local) [hetzner] (blank=keep, type - to clear):" \
    "2. Enter your target domain (e.g. smallworlds.network) [example.org] (blank=keep, type - to clear):" \
    "3. Enter the admin email address [admin@example.org] (blank=keep, type - to clear):" \
    "4. Select onboarding mode (invitation or self-registration) [invitation] (blank=keep, type - to clear):" \
    "5. Enter environment extension (e.g. .dev, or leave empty for prod):" \
    "7. Enter your Git username [example] (blank=keep, type - to clear):" \
    "8. Paste your Git Access Token [saved]:" \
    "9. Paste your Hetzner Cloud API Token [saved]:"
do
    check_contains "$output" "$prompt" "prompt unchanged: ${prompt%% *} ${prompt#* }"
done
check_absent "$output" ">>> step:"  "no progress markers in a human's terminal"
check_absent "$output" "DOMAIN: "   "no non-interactive report lines"
check_absent "$output" "should-never-be-printed" "no secret value echoed back"

# --------------------------------------------------------------------------
echo "prepare-community-repo.sh — non-interactive, end to end"
# --------------------------------------------------------------------------
overlay="$temporary_directory/overlay"
mkdir -p "$overlay"
cat > "$stub_home/.config/smallworlds/community-setup.conf" <<CONF
STORED_REPO_PATH="$overlay"
STORED_REMOTE_URL="https://github.com/example/my-community-config.git"
STORED_VERSION="v1.2.43"
STORED_TARGET_DOMAIN="example.org"
STORED_ENV_EXT=""
STORED_CREATE_REPO="n"
STORED_PUSH_CHOICE="n"
STORED_APP_forgejo=n
STORED_APP_immich=n
STORED_APP_nextcloud=y
STORED_APP_stalwart=n
STORED_APP_bulwark=n
STORED_APP_excalidraw=y
STORED_APP_jitsi=n
STORED_APP_collabora=n
STORED_APP_euro_office=n
STORED_APP_plane=n
STORED_APP_pod_gateway=n
CONF

output="$(cd "$repository_root" && HOME="$stub_home" SMALLWORLDS_NONINTERACTIVE=1 \
    GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.org \
    GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.org \
    timeout 120 bash admin-tools/prepare-community-repo.sh </dev/null 2>&1)"
status=$?

if [[ "$status" -eq 0 ]]; then pass "completed without a tty"; else
    fail "completed without a tty — exited $status"
fi
check_contains "$output" ">>> ok:repo-commit" "emitted its stage markers"
check_absent "$output" "Enter your target base domain" "no prompt was rendered"

for app in dashboard keycloak nextcloud excalidraw; do
    if [[ -f "$overlay/$app/kustomization.yaml" ]]; then
        pass "selected app present: $app"
    else
        fail "selected app present: $app"
    fi
done
for app in immich forgejo jitsi collabora euro-office plane; do
    if [[ ! -d "$overlay/$app" ]]; then pass "unselected app absent: $app"; else
        fail "unselected app absent: $app"
    fi
done
if grep -q 'path: /data/OFFICE_BACKEND' "$overlay/nextcloud/kustomization.yaml" \
   && grep -q 'value: "none"' "$overlay/nextcloud/kustomization.yaml"; then
    pass "Nextcloud explicitly records no Office backend"
else
    fail "Nextcloud explicitly records no Office backend"
fi
# The Operator Console is deactivated: an overlay must no longer carry it.
if grep -rq "operator-console" "$overlay" 2>/dev/null; then
    fail "no operator-console in the overlay (it is deactivated)"
else
    pass "no operator-console in the overlay (it is deactivated)"
fi
if grep -q 'ref=v1.2.43' "$overlay/kustomization.yaml"; then
    pass "pinned the requested upstream tag"
else
    fail "pinned the requested upstream tag"
fi

# Re-running setup is also the supported switch path. Exercise the full
# none -> Euro-Office -> none cycle against the already-created overlay.
sed -i 's/^STORED_APP_euro_office=n$/STORED_APP_euro_office=y/' \
    "$stub_home/.config/smallworlds/community-setup.conf"
output="$(cd "$repository_root" && HOME="$stub_home" SMALLWORLDS_NONINTERACTIVE=1 \
    GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.org \
    GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.org \
    timeout 120 bash admin-tools/prepare-community-repo.sh </dev/null 2>&1)"
if grep -q '/apps/euro-office.yaml' "$overlay/kustomization.yaml" \
   && grep -q 'value: "euro-office"' "$overlay/nextcloud/kustomization.yaml" \
   && grep -q 'value: office.example.org' "$overlay/euro-office/kustomization.yaml"; then
    pass "a rerun switches the existing overlay to Euro-Office"
else
    fail "a rerun switches the existing overlay to Euro-Office"
fi

sed -i 's/^STORED_APP_euro_office=y$/STORED_APP_euro_office=n/' \
    "$stub_home/.config/smallworlds/community-setup.conf"
output="$(cd "$repository_root" && HOME="$stub_home" SMALLWORLDS_NONINTERACTIVE=1 \
    GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.org \
    GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.org \
    timeout 120 bash admin-tools/prepare-community-repo.sh </dev/null 2>&1)"
if ! grep -q '/apps/euro-office.yaml' "$overlay/kustomization.yaml" \
   && grep -q 'value: "none"' "$overlay/nextcloud/kustomization.yaml" \
   && [[ "$(grep -c 'path: /data/OFFICE_BACKEND' "$overlay/nextcloud/kustomization.yaml")" -eq 1 ]]; then
    pass "a rerun switches Euro-Office off without duplicate patches"
else
    fail "a rerun switches Euro-Office off without duplicate patches"
fi

# --------------------------------------------------------------------------
echo "prepare-community-repo.sh — external object storage"
# --------------------------------------------------------------------------
run_prepare() {
    ( cd "$repository_root" && HOME="$stub_home" SMALLWORLDS_NONINTERACTIVE=1 \
        GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.org \
        GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.org \
        timeout 120 bash admin-tools/prepare-community-repo.sh </dev/null 2>&1 )
}
set_stored() {
    local conf="$stub_home/.config/smallworlds/community-setup.conf"
    sed -i "/^$1=/d" "$conf"
    printf '%s="%s"\n' "$1" "$2" >> "$conf"
}
marker='>>> smallworlds:object-storage'

if grep -rq "$marker" "$overlay"; then
    fail "an overlay that never chose external storage carries no storage patches"
else
    pass "an overlay that never chose external storage carries no storage patches"
fi
check_contains "$(cat "$stub_home/.config/smallworlds/community-setup.conf")" \
    'STORED_OBJECT_STORAGE="garage"' "the default choice is recorded for the installer"

set_stored STORED_OBJECT_STORAGE external
set_stored STORED_S3_ENDPOINT https://s3.provider.example
set_stored STORED_S3_REGION eu-central-1
output="$(run_prepare)"
status=$?
if [[ "$status" -eq 0 ]]; then pass "prepared an overlay for an external provider"; else
    fail "prepared an overlay for an external provider — exited $status: $output"
fi
check_contains "$output" "S3_BUCKET_PREFIX: sw-example-org" "derived a bucket prefix from the domain"
root_overlay="$(cat "$overlay/kustomization.yaml")"
check_contains "$root_overlay" "name: garage-backup" "the root drops the Garage applications"
check_contains "$root_overlay" "value: sw-example-org-velero-backups" "Velero is pointed at the prefixed bucket"
check_contains "$(cat "$overlay/nextcloud/kustomization.yaml")" 'value: "s3.provider.example"' \
    "Nextcloud is pointed at the provider"
check_contains "$(cat "$overlay/keycloak/kustomization.yaml")" \
    "s3://sw-example-org-postgres-backups-keycloak/" "Keycloak's database backups follow"
if grep -q "$marker" "$overlay/excalidraw/kustomization.yaml"; then
    fail "an app that stores nothing in S3 gets no storage patches"
else
    pass "an app that stores nothing in S3 gets no storage patches"
fi

run_prepare >/dev/null
if [[ "$(grep -c "$marker" "$overlay/kustomization.yaml")" -eq 1 \
   && "$(grep -c "$marker" "$overlay/nextcloud/kustomization.yaml")" -eq 1 ]]; then
    pass "a rerun replaces the storage patches instead of appending"
else
    fail "a rerun replaces the storage patches instead of appending"
fi

set_stored STORED_S3_ENDPOINT https://s3.provider.example/my-bucket
before="$(cat "$overlay/kustomization.yaml")"
output="$(run_prepare)"
status=$?
check_contains "$output" "has a path" "a bucket URL in place of the endpoint is refused"
if [[ "$status" -ne 0 && "$(cat "$overlay/kustomization.yaml")" == "$before" ]]; then
    pass "and the overlay is left untouched"
else
    fail "and the overlay is left untouched"
fi
set_stored STORED_S3_ENDPOINT https://s3.provider.example

set_stored STORED_OBJECT_STORAGE garage
run_prepare >/dev/null
if grep -rq "$marker" "$overlay" || grep -rq "sw-example-org" "$overlay"; then
    fail "choosing Garage again removes every storage patch"
else
    pass "choosing Garage again removes every storage patch"
fi

# --------------------------------------------------------------------------
echo "prepare-community-repo.sh — Immich or Gallery"
# --------------------------------------------------------------------------
tenant_line() { grep -o 'kubernetes/tenants/[a-z-]*?ref=[^ ]*' "$overlay/immich/kustomization.yaml"; }
set_stored STORED_APP_immich y
sed -i '/^STORED_APP_immich=n$/d' "$stub_home/.config/smallworlds/community-setup.conf"
output="$(run_prepare)"
check_contains "$output" "PHOTO_BACKEND: immich" "Immich is the photo application unless told otherwise"
check_contains "$(tenant_line)" "tenants/immich?ref=v1.2.43" "the overlay follows the Immich tenant"

set_stored STORED_PHOTO_BACKEND gallery
run_prepare >/dev/null
check_contains "$(tenant_line)" "tenants/gallery?ref=v1.2.43" "choosing Gallery repoints the same overlay directory"
if [[ ! -d "$overlay/gallery" ]] && ! grep -q "apps/gallery.yaml" "$overlay/kustomization.yaml" \
   && grep -q "/apps/immich.yaml" "$overlay/kustomization.yaml"; then
    pass "Gallery is not a second application: one directory, one Application"
else
    fail "Gallery is not a second application: one directory, one Application"
fi
if grep -q "value: photos.example.org" "$overlay/immich/kustomization.yaml"; then
    pass "the domain patches are untouched by the switch"
else
    fail "the domain patches are untouched by the switch"
fi

set_stored STORED_PHOTO_BACKEND immich
run_prepare >/dev/null
check_contains "$(tenant_line)" "tenants/immich?ref=v1.2.43" "choosing Immich again repoints it back"
if [[ "$(grep -c 'kubernetes/tenants/' "$overlay/immich/kustomization.yaml")" -eq 1 ]]; then
    pass "the resource line is rewritten, not duplicated"
else
    fail "the resource line is rewritten, not duplicated"
fi

set_stored STORED_PHOTO_BACKEND picasa
output="$(run_prepare)"
status=$?
check_contains "$output" "immich, gallery" "an unknown photo application is refused"
if [[ "$status" -ne 0 ]]; then pass "and the script stops"; else fail "and the script stops"; fi
set_stored STORED_PHOTO_BACKEND immich
set_stored STORED_APP_immich n

# --------------------------------------------------------------------------
echo "prepare-community-repo.sh — a push the remote refuses"
# --------------------------------------------------------------------------
# A remote that cannot be reached without touching the network. Headless, the
# installer runs next and would only fail on the empty repository.
set_stored STORED_REMOTE_URL "$stub_home/no-such-remote.git"
set_stored STORED_PUSH_CHOICE y
output="$(run_prepare)"
status=$?
check_contains "$output" ">>> fail:repo-push" "the refused push is reported"
check_contains "$output" "Check the Git access token" "with advice on what to do"
if [[ "$status" -ne 0 ]]; then pass "and the script stops instead of handing over an empty repository"
else fail "and the script stops instead of handing over an empty repository"; fi
set_stored STORED_REMOTE_URL "https://github.com/example/my-community-config.git"
set_stored STORED_PUSH_CHOICE n

# --------------------------------------------------------------------------
echo "prepare-community-repo.sh — a missing required value"
# --------------------------------------------------------------------------
rm -rf "$overlay"; mkdir -p "$overlay"
sed -i 's#^STORED_REMOTE_URL=.*#STORED_REMOTE_URL=""#' \
    "$stub_home/.config/smallworlds/community-setup.conf"
output="$(cd "$repository_root" && HOME="$stub_home" SMALLWORLDS_NONINTERACTIVE=1 \
    timeout 120 bash admin-tools/prepare-community-repo.sh </dev/null 2>&1)"
status=$?
check_contains "$output" "!!! missing:REMOTE_URL" "named the missing variable"
if [[ "$status" -eq 78 ]]; then
    pass "exited 78 (EX_CONFIG) instead of looping forever"
else
    fail "exited 78 (EX_CONFIG) instead of looping forever — got $status"
fi

# --------------------------------------------------------------------------
echo "prepare-community-repo.sh — interactive is unchanged"
# --------------------------------------------------------------------------
# `read -e -i` renders an editable pre-filled line, which is the behaviour most
# at risk from the ask_prefilled conversion: a pipe would show none of it.
rm -rf "$overlay"; mkdir -p "$overlay"
sed -i "s#^STORED_REMOTE_URL=.*#STORED_REMOTE_URL=\"https://github.com/example/my-community-config.git\"#" \
    "$stub_home/.config/smallworlds/community-setup.conf"
output="$(cd "$repository_root" && HOME="$stub_home" \
    GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.org \
    GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.org \
    timeout 120 python3 "$temporary_directory/pty-drive.py" "$repository_root" \
    bash admin-tools/prepare-community-repo.sh 2>&1)"

check_contains "$output" \
    "1. Enter your target base domain (e.g. smallworlds.network): example.org" \
    "prompt 1 still renders its pre-filled default"
check_contains "$output" \
    "tag (e.g. v1.0.0, or HEAD to track latest): v1.2.43" \
    "prompt 5 still renders its pre-filled default"
check_contains "$output" "Do you want to install nextcloud? (y/n): y" \
    "the per-app loop still renders its stored answer"
check_contains "$output" \
    "Which Office editor should Files use? (none/collabora/euro-office): none" \
    "the Office prompt renders its stored three-way choice"
check_absent "$output" ">>> step:" "no progress markers in a human's terminal"

echo ""
if [[ "$failures" -eq 0 ]]; then
    echo "All checks passed."
else
    echo "$failures check(s) failed."
    exit 1
fi
