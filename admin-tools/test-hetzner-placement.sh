#!/usr/bin/env bash
# Asserts where smallworlds-init.sh decides to put a Hetzner node, and which
# DNS records it asks Terraform to create.
#
#   1. A location is chosen by asking Hetzner what it can actually sell, not by
#      a hard-coded default -- a sold-out server type in one location must not
#      stop an install the other two could have served.
#   2. An existing Primary IP pins the location, because it is the community's
#      public address. If that location has no capacity, the install stops
#      BEFORE anything is created, and says what can be done about it.
#   3. Only the applications this community selected get an A record. A record
#      for photos.<domain> with no Immich behind it is a link to a certificate
#      error.
#   4. Every failure carries sw_guidance markers, so the wizard's last screen
#      and the terminal give the same advice.
#
# Nothing here touches a real cloud account: curl is a stub that answers the
# Hetzner API from fixtures, and terraform records its arguments and stops.
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
# Stubs
# --------------------------------------------------------------------------
stub_bin="$temporary_directory/bin"
stub_home="$temporary_directory/home"
fixtures="$temporary_directory/fixtures"
mkdir -p "$stub_bin" "$stub_home/.ssh" "$stub_home/.config/smallworlds" "$fixtures"

printf 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITESTKEYTESTKEYTESTKEYTESTKEY test\n' \
    > "$stub_home/.ssh/id_ed25519.pub"

# The Hetzner API, as far as smallworlds-init.sh uses it. Which fixture the
# capacity and Primary IP endpoints return is set per scenario, so one stub
# covers "plenty available", "sold out where the IP is" and "sold out
# everywhere" without three copies of it.
cat > "$stub_bin/curl" <<'STUB'
#!/usr/bin/env bash
url=""
for argument in "$@"; do
    case "$argument" in https://*) url="$argument" ;; esac
done
posting=""
for argument in "$@"; do
    case "$argument" in POST) posting=1 ;; esac
done

case "$url" in
    *"/v1/zones"*)        cat "$FIXTURES/zones.json" ;;
    *"/v1/ssh_keys"*)     cat "$FIXTURES/ssh_keys.json" ;;
    *"/v1/primary_ips"*)
        if [ -n "$posting" ]; then cat "$FIXTURES/primary_ip_created.json"
        else cat "$FIXTURES/primary_ips.json"; fi ;;
    *"/v1/server_types"*) cat "$FIXTURES/server_types.json" ;;
    *"/v1/datacenters"*)  cat "$FIXTURES/datacenters.json" ;;
    *"/v1/images"*)       printf '{"images":[]}\n' ;;
    *)                    printf '{}\n' ;;
esac
exit 0
STUB

# Records the variables it was given and then fails, which is where this test
# wants the install to stop: everything after `apply` waits on a real machine.
cat > "$stub_bin/terraform" <<'STUB'
#!/usr/bin/env bash
case "${1:-}" in
    apply)
        for argument in "$@"; do
            case "$argument" in -var-file=*) cp "${argument#-var-file=}" "$FIXTURES/applied.tfvars" ;; esac
        done
        echo "Error: resource_unavailable (stub)" >&2
        exit 1 ;;
esac
exit 0
STUB

for stub in ssh scp kubectl jq; do
    printf '#!/bin/sh\nexit 0\n' > "$stub_bin/$stub"
done
chmod +x "$stub_bin"/*

# smallworlds-init.sh sed-edits a tracked manifest and writes its cache beside
# itself, so it runs from a copy rather than the working tree.
init_root="$temporary_directory/init-root"
mkdir -p "$init_root/admin-tools" \
         "$init_root/infrastructure/kubernetes/tenants/keycloak" \
         "$init_root/infrastructure/terraform"
cp "$repository_root/smallworlds-init.sh" "$init_root/"
cp -r "$repository_root/admin-tools/lib" "$init_root/admin-tools/"
cp "$repository_root/infrastructure/kubernetes/tenants/keycloak/realm-config-job.yaml" \
   "$init_root/infrastructure/kubernetes/tenants/keycloak/"

cat > "$init_root/.smallworlds-cache.env" <<'CACHE'
DEPLOY_TARGET="hetzner"
DOMAIN="example.org"
ENV_EXT=""
ADMIN_EMAIL="admin@example.org"
ONBOARDING_MODE="invitation"
HCLOUD_TOKEN="hetzner-token"
GITOPS_REPO_URL="https://github.com/example/my-community-config.git"
GITOPS_REPO_USER="example"
GITOPS_REPO_TOKEN="git-token"
CACHE

# The community selected two applications out of ten. Written the way both
# prepare-community-repo.sh and the browser wizard write it.
community_config="$stub_home/.config/smallworlds/community-setup.conf"
cat > "$community_config" <<'CONF'
STORED_REPO_PATH="/tmp/overlay"
STORED_APP_forgejo=n
STORED_APP_immich=n
STORED_APP_nextcloud=y
STORED_APP_stalwart=n
STORED_APP_bulwark=n
STORED_APP_excalidraw=y
STORED_APP_jitsi=n
STORED_APP_collabora=n
STORED_APP_plane=n
STORED_APP_pod_gateway=n
CONF

printf '{"zones":[{"id":1,"name":"example.org"}]}\n'       > "$fixtures/zones.json"
printf '{"ssh_keys":[{"id":42,"name":"SmallWorlds Admin Key"}]}\n' > "$fixtures/ssh_keys.json"
printf '{"server_types":[{"id":104,"name":"cx43"}]}\n'     > "$fixtures/server_types.json"
printf '{"primary_ip":{"id":7,"ip":"203.0.113.9"}}\n'      > "$fixtures/primary_ip_created.json"

no_primary_ip() { printf '{"primary_ips":[]}\n' > "$fixtures/primary_ips.json"; }
# What Hetzner sends today: `location` on the Primary IP itself, no nested
# `datacenter` object at all. Reading only the old shape put the IP ADDRESS in
# the location variable, and every comparison after it compared an address
# against location names.
primary_ip_in() {
    printf '{"primary_ips":[{"id":7,"name":"smallworlds-ip","ip":"203.0.113.9",
        "location":{"name":"%s","city":"Somewhere"}}]}\n' "$1" \
        > "$fixtures/primary_ips.json"
}
# The older shape, still accepted.
legacy_primary_ip_in() {
    printf '{"primary_ips":[{"id":7,"name":"smallworlds-ip","ip":"203.0.113.9",
        "datacenter":{"name":"%s-dc1","location":{"name":"%s"}}}]}\n' "$1" "$1" \
        > "$fixtures/primary_ips.json"
}
# Neither shape — the address is known, where it lives is not.
placeless_primary_ip() {
    printf '{"primary_ips":[{"id":7,"name":"smallworlds-ip","ip":"203.0.113.9"}]}\n' \
        > "$fixtures/primary_ips.json"
}
unknown_server_type() { printf '{"server_types":[]}\n' > "$fixtures/server_types.json"; }
known_server_type()   { printf '{"server_types":[{"id":104,"name":"cx43"}]}\n' > "$fixtures/server_types.json"; }
api_unreachable()     { printf '' > "$fixtures/server_types.json"; printf '' > "$fixtures/datacenters.json"; }
# available_in <location>...  -- cx43 (id 104) is orderable exactly there.
available_in() {
    local wanted=" $* " entries=""
    for location in nbg1 fsn1 hel1 ash; do
        local available="[22]"
        case "$wanted" in *" $location "*) available="[104,22]" ;; esac
        entries="${entries:+$entries,}{\"name\":\"${location}-dc1\",
            \"location\":{\"name\":\"$location\"},
            \"server_types\":{\"supported\":[104],\"available\":$available}}"
    done
    printf '{"datacenters":[%s]}\n' "$entries" > "$fixtures/datacenters.json"
}

run_init() {
    rm -f "$fixtures/applied.tfvars"
    ( cd "$init_root" \
      && env PATH="$stub_bin:$PATH" HOME="$stub_home" FIXTURES="$fixtures" \
             SMALLWORLDS_NONINTERACTIVE=1 \
         timeout 120 bash smallworlds-init.sh </dev/null 2>&1 )
}

# --------------------------------------------------------------------------
echo "a fresh project — the location is scanned, not assumed"
# --------------------------------------------------------------------------
no_primary_ip
available_in fsn1 hel1     # nbg1, the first preference, is sold out
output="$(run_init)"

check_contains "$output" ">>> step:primary-ip" "the Primary IP became a stage of its own"
check_contains "$output" ">>> ok:primary-ip"   "it was created rather than demanded up front"
check_contains "$output" "Creating it in fsn1" "skipped the sold-out first preference"
check_absent   "$output" "Creating it in nbg1" "did not pick a location that has no hardware"

applied="$(cat "$fixtures/applied.tfvars" 2>/dev/null)"
check_contains "$applied" 'location          = "fsn1"'  "terraform was told where the Primary IP is"
check_contains "$applied" 'server_type       = "cx43"'  "the server type travels with the location"

# --------------------------------------------------------------------------
echo "the DNS records follow the selected applications"
# --------------------------------------------------------------------------
check_contains "$applied" '"nextcloud"'  "a selected application is passed to terraform"
check_contains "$applied" '"excalidraw"' "so is the other one"
check_absent   "$applied" '"immich"'     "an unselected application is not"
check_absent   "$applied" '"stalwart"'   "and neither is mail, which is opt-in"

# --------------------------------------------------------------------------
echo "a failed apply says what can be done about it"
# --------------------------------------------------------------------------
check_contains "$output" ">>> fail:terraform-apply" "the stage is marked failed"
check_contains "$output" ">>> guidance" "guidance markers reach the wizard"
check_contains "$output" "safe to re-run" "and say the install can simply be repeated"

# --------------------------------------------------------------------------
echo "an existing Primary IP pins the location"
# --------------------------------------------------------------------------
no_primary_ip
available_in nbg1 fsn1 hel1
primary_ip_in hel1
output="$(run_init)"
check_contains "$output" "already exists in hel1" "the existing address decides"
applied="$(cat "$fixtures/applied.tfvars" 2>/dev/null)"
check_contains "$applied" 'location          = "hel1"' "even though nbg1 is preferred and available"

# --------------------------------------------------------------------------
echo "no capacity where the Primary IP is — stop before creating anything"
# --------------------------------------------------------------------------
primary_ip_in hel1
available_in nbg1 fsn1
output="$(run_init)"
check_contains "$output" ">>> fail:primary-ip" "the install stops at the placement check"
check_contains "$output" "Try one of the locations that has the hardware" "names the way out"
check_contains "$output" "nbg1, fsn1" "and which locations actually have it"
check_absent   "$output" ">>> step:terraform-apply" "nothing was provisioned"
if [[ -f "$fixtures/applied.tfvars" ]]; then
    fail "terraform was not run"
else
    pass "terraform was not run"
fi

# --------------------------------------------------------------------------
echo "no capacity anywhere in Europe"
# --------------------------------------------------------------------------
no_primary_ip
available_in ash
output="$(run_init)"
check_contains "$output" ">>> fail:primary-ip" "the install stops before reserving an address"
check_contains "$output" "No European location has this size right now" "says so plainly"
check_contains "$output" "HETZNER_SERVER_TYPE" "and offers the escape hatch"
check_absent   "$output" "Creating it in ash" "never leaves the EU"

# --------------------------------------------------------------------------
echo "the Primary IP's location is read whichever way Hetzner reports it"
# --------------------------------------------------------------------------
known_server_type
available_in nbg1 fsn1 hel1
legacy_primary_ip_in fsn1
output="$(run_init)"
check_contains "$output" "already exists in fsn1" "the older nested datacenter shape still works"
check_absent   "$output" "already exists in 203.0.113.9" "the address is never mistaken for a location"
applied="$(cat "$fixtures/applied.tfvars" 2>/dev/null)"
check_contains "$applied" 'location          = "fsn1"' "and reaches terraform"

placeless_primary_ip
output="$(run_init)"
check_contains "$output" "did not say where" "an unplaceable Primary IP is reported honestly"
check_contains "$output" ">>> ok:primary-ip" "and does not stop the install"
check_contains "$output" ">>> step:terraform-apply" "terraform runs and follows the IP itself"

# --------------------------------------------------------------------------
echo "a server type Hetzner does not know is not 'sold out'"
# --------------------------------------------------------------------------
primary_ip_in fsn1
unknown_server_type
available_in nbg1 fsn1 hel1
output="$(run_init)"
check_contains "$output" ">>> fail:primary-ip" "the install stops"
check_contains "$output" "no server type called" "and says the name is wrong"
check_absent   "$output" "No European location has this size" "rather than blaming Hetzner's stock"

# Case is the way this happens in practice: HETZNER_SERVER_TYPE=CPX42.
known_server_type
printf '{"server_types":[{"id":104,"name":"cpx42"}]}\n' > "$fixtures/server_types.json"
available_in nbg1 fsn1 hel1
output="$(rm -f "$fixtures/applied.tfvars"; cd "$init_root" \
    && env PATH="$stub_bin:$PATH" HOME="$stub_home" FIXTURES="$fixtures" \
           SMALLWORLDS_NONINTERACTIVE=1 HETZNER_SERVER_TYPE=CPX42 \
       timeout 120 bash smallworlds-init.sh </dev/null 2>&1)"
applied="$(cat "$fixtures/applied.tfvars" 2>/dev/null)"
check_contains "$applied" 'server_type       = "cpx42"' "an upper-case type name is folded, not rejected"
check_absent   "$output" "no server type called" "so it is not reported as unknown"

# --------------------------------------------------------------------------
echo "an API that will not answer does not stop an install"
# --------------------------------------------------------------------------
# "Could not tell" is not "sold out". Blocking on a failed lookup stops an
# install that would have worked, and blames Hetzner for it.
known_server_type
primary_ip_in fsn1
printf '' > "$fixtures/datacenters.json"
output="$(run_init)"
check_contains "$output" "Could not check" "the failed lookup is admitted"
check_contains "$output" ">>> ok:primary-ip" "and the install carries on"
check_contains "$output" ">>> step:terraform-apply" "as far as letting Hetzner answer for itself"

echo ""
if [[ "$failures" -eq 0 ]]; then
    echo "All checks passed."
else
    echo "$failures check(s) failed."
    exit 1
fi
