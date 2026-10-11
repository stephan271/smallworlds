#!/usr/bin/env bash
set -e

# Colors for pretty output
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Shared helpers (cluster_label, kubeconfig_path)
source "$(cd "$(dirname "$0")" && pwd)/admin-tools/lib/cluster-env.sh"
# Shared prompt helpers (ask_with_default, progress markers). Also what lets
# this script run with no tty under SMALLWORLDS_NONINTERACTIVE=1, which is how
# the browser setup wizard drives it — see admin-tools/lib/ask.sh.
source "$(cd "$(dirname "$0")" && pwd)/admin-tools/lib/ask.sh"

echo -e "${CYAN}======================================================${NC}"
echo -e "${CYAN}        Welcome to the SmallWorlds Installer         ${NC}"
echo -e "${CYAN}======================================================${NC}"
echo ""



echo -e "This wizard will spin up your fully automated sovereign cloud."
echo ""

CACHE_FILE=".smallworlds-cache.env"
# Captured before the cache is sourced, because the cache also carries this:
# only a value the operator put in the environment for THIS run may override
# the node that is already running. Asking for a different size is a resize,
# and has to be asked for deliberately.
SW_SERVER_TYPE_FROM_ENV="${HETZNER_SERVER_TYPE:-}"
if [[ -f "$CACHE_FILE" ]]; then
    source "$CACHE_FILE"
fi

# Ensure the DNS zone for $DOMAIN exists in Hetzner DNS (uses $HCLOUD_TOKEN).
# Used by the hetzner target and by internet-exposed local deployments.
ensure_dns_zone() {
    sw_step dns-zone "Ensuring the Hetzner DNS zone exists"
    echo -e "${CYAN}Ensuring DNS Zone '$DOMAIN' exists in Hetzner...${NC}"
    local existing_id api_response
    # A raw grep for "name":"$DOMAIN" against the list response is fragile
    # (trailing dot, key order, whitespace, pagination all cause false
    # negatives — confirmed in practice: it missed an already-existing
    # zone and then double-created it). Filter server-side by name and
    # parse the JSON properly instead.
    existing_id=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/zones?name=$DOMAIN" \
        | python3 -c "
import json, sys
domain = sys.argv[1].rstrip('.')
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for z in data.get('zones', []):
    if z.get('name', '').rstrip('.') == domain:
        print(z['id'])
        break
" "$DOMAIN")

    if [ -z "$existing_id" ]; then
        echo -e "${YELLOW}Zone $DOMAIN not found. Creating it...${NC}"
        api_response=$(curl -s -X POST -H "Content-Type: application/json" -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/zones" -d '{"name":"'"$DOMAIN"'", "mode": "primary", "ttl": 3600}')
        if echo "$api_response" | grep -q "\"error\""; then
            echo -e "${RED}Failed to create zone: $(echo "$api_response" | jq -r '.error.message' 2>/dev/null || echo "$api_response")${NC}"
            sw_step_fail dns-zone "could not create the DNS zone"
            exit 1
        else
            echo -e "${GREEN}Zone created successfully.${NC}"
        fi
    else
        echo -e "${GREEN}Zone $DOMAIN already exists (id: $existing_id).${NC}"
    fi
    sw_step_ok dns-zone
}

# Ensure the shared "SmallWorlds Admin Key" exists in Hetzner Cloud (uses
# $HCLOUD_TOKEN) and set $SSH_KEY_ID to its id. Idempotent, same pattern as
# ensure_dns_zone: whichever deployment (dev or prod, either order) runs
# first uploads it; later ones just find and reuse it. Terraform itself
# never creates/owns this key — see infrastructure/terraform/main.tf locals.
ensure_ssh_key() {
    local pubkey_path="${SSH_PUBLIC_KEY_PATH:-$HOME/.ssh/id_ed25519.pub}"
    sw_step ssh-key "Ensuring the Hetzner admin SSH key is registered"
    if [[ ! -f "$pubkey_path" ]]; then
        echo -e "${RED}SSH public key not found at $pubkey_path — generate one with 'ssh-keygen -t ed25519' first.${NC}"
        sw_step_fail ssh-key "no SSH public key at $pubkey_path"
        exit 1
    fi

    echo -e "${CYAN}Ensuring Hetzner SSH key 'SmallWorlds Admin Key' exists...${NC}"
    SSH_KEY_ID=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/ssh_keys?name=SmallWorlds%20Admin%20Key" \
        | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
keys = data.get('ssh_keys') or []
if keys:
    print(keys[0]['id'])
")

    if [ -z "$SSH_KEY_ID" ]; then
        echo -e "${YELLOW}Key not found. Uploading $pubkey_path...${NC}"
        local pubkey_json api_response
        pubkey_json=$(python3 -c 'import json,sys; print(json.dumps(open(sys.argv[1]).read().strip()))' "$pubkey_path")
        api_response=$(curl -s -X POST -H "Content-Type: application/json" -H "Authorization: Bearer $HCLOUD_TOKEN" "https://api.hetzner.cloud/v1/ssh_keys" -d "{\"name\":\"SmallWorlds Admin Key\", \"public_key\": $pubkey_json}")
        SSH_KEY_ID=$(echo "$api_response" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
key = data.get('ssh_key')
if key:
    print(key['id'])
")
        if [ -z "$SSH_KEY_ID" ]; then
            echo -e "${RED}Failed to upload SSH key: $(echo "$api_response" | jq -r '.error.message' 2>/dev/null || echo "$api_response")${NC}"
            sw_step_fail ssh-key "upload rejected by the Hetzner API"
            exit 1
        fi
        echo -e "${GREEN}SSH key uploaded (id: $SSH_KEY_ID).${NC}"
    else
        echo -e "${GREEN}SSH key already exists in Hetzner (id: $SSH_KEY_ID).${NC}"
    fi
    sw_step_ok ssh-key
}

# ---------------------------------------------------------------------------
# Where the node goes
#
# Hetzner sells a server type out in one location while the others still have
# it, and the apply's answer for that is a bare "resource_unavailable" arriving
# after the firewall and both volumes have already been created. So the choice
# is made here, against the API, before anything exists.
#
# The choice is only free once. A Primary IP is datacenter-bound, and it is the
# community's public address: every A record, the PTR and the tailnet follow
# it. So the scan runs at the moment the Primary IP is created and never again
# — afterwards, the existing Primary IP's location is simply the answer.
# ---------------------------------------------------------------------------

# The three European locations, most preferred first. All in the EU, so member
# data stays inside it, and each can hold the node, both volumes and the
# Primary IP together.
HETZNER_LOCATIONS="${HETZNER_LOCATIONS:-nbg1 fsn1 hel1}"
# 16 GB is the floor: 8 GB nodes saturate once the full app suite deploys, and
# the probe timeouts cascade into CNPG failovers and OOM crashloops.
#
# This is only the fallback. resolve_server_type() below prefers, in order, what
# the operator asked for this run, what the node already running actually is,
# and what the last run used — a hard-coded default that quietly overrules a
# live node proposes a resize nobody asked for.
HETZNER_SERVER_TYPE_DEFAULT="cx43"
HETZNER_SERVER_TYPE="${HETZNER_SERVER_TYPE:-$HETZNER_SERVER_TYPE_DEFAULT}"
# Hetzner's type names are lowercase. "CPX42" is not a different server type,
# it is a name the API will not match — and the honest answer to that is "no
# such type", not "sold out everywhere", which is what an operator staring at a
# Console full of available servers was previously told.
HETZNER_SERVER_TYPE="$(printf '%s' "$HETZNER_SERVER_TYPE" | tr '[:upper:]' '[:lower:]')"

# Ask Hetzner where $HETZNER_SERVER_TYPE can be ordered right now.
#
# Sets:
#   SW_CAPACITY         "<location> <datacenter>" lines, preference order
#   SW_CAPACITY_STATUS  ok       -- answered; SW_CAPACITY may still be empty,
#                                   which then means genuinely sold out
#                       no-type  -- Hetzner does not know that type name
#                       unknown  -- the question could not be answered at all
#
# The three-way split is the point. Treating "could not tell" as "sold out"
# stops an install that would have worked, and blames Hetzner for it. Only a
# real, answered "nowhere" is allowed to block.
hetzner_scan_capacity() {
    SW_CAPACITY=""
    SW_CAPACITY_STATUS="unknown"

    local type_id datacenters
    type_id="$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/server_types?name=$HETZNER_SERVER_TYPE" \
        | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
types = data.get('server_types')
if types is None:
    sys.exit(0)          # not a well-formed answer -- unknown, not 'no type'
if not types:
    print('none')        # a well-formed answer saying no such name
    sys.exit(0)
print(types[0]['id'])
")"

    if [[ -z "$type_id" ]]; then
        return 0                      # could not ask; SW_CAPACITY_STATUS=unknown
    fi
    if [[ "$type_id" == "none" ]]; then
        SW_CAPACITY_STATUS="no-type"
        return 0
    fi

    datacenters="$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/datacenters" \
        | python3 -c "
import json, sys

preferred = sys.argv[1].split()
try:
    type_id = int(sys.argv[2])
except ValueError:
    sys.exit(0)
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)

datacenters = data.get('datacenters')
if not datacenters:
    sys.exit(0)          # nothing to go on -- the caller keeps 'unknown'

# The datacenters endpoint reports availability per datacenter as server type
# ids, which is why the id had to be looked up first.
by_location = {}
for datacenter in datacenters:
    location = (datacenter.get('location') or {}).get('name')
    available = (datacenter.get('server_types') or {}).get('available') or []
    if location:
        by_location.setdefault(location, []).append((datacenter.get('name'), set(available)))

print('answered')
for location in preferred:
    for name, available in by_location.get(location, []):
        if type_id in available:
            print(location, name)
            break
" "$HETZNER_LOCATIONS" "$type_id")"

    if [[ -z "$datacenters" ]]; then
        return 0                      # could not ask; still unknown
    fi
    SW_CAPACITY_STATUS="ok"
    # The first line is the "I did get an answer" marker; the rest is the answer.
    SW_CAPACITY="$(printf '%s\n' "$datacenters" | tail -n +2)"
}

# The first datacenter in $1, e.g. "fsn1" -> "fsn1-dc14". Creating a Primary IP
# needs a datacenter name, not a location name, and this has to work even when
# the capacity scan could not answer.
hetzner_datacenter_in() {
    curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/datacenters" \
        | python3 -c "
import json, sys
wanted = sys.argv[1]
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for datacenter in data.get('datacenters') or []:
    if ((datacenter.get('location') or {}).get('name')) == wanted:
        print(datacenter.get('name') or '')
        break
" "$1"
}

# The locations from the last scan, as "nbg1, fsn1".
hetzner_capacity_list() {
    printf '%s' "$SW_CAPACITY" | awk 'NF {print $1}' | paste -sd, - | sed 's/,/, /g'
}

# Whether the last scan found capacity in $1.
hetzner_has_capacity_in() {
    [[ -n "$1" ]] && printf '%s\n' "$SW_CAPACITY" | awk 'NF {print $1}' | grep -qx "$1"
}

# Say, in the operator's terms, what the scan found and what to do about it.
# Used before an apply that cannot work and after one that failed, because
# Hetzner's own message names neither the type nor a way out.
report_capacity_problem() {
    local pinned="$1" available
    available="$(hetzner_capacity_list)"

    if [[ "$SW_CAPACITY_STATUS" == "no-type" ]]; then
        sw_guidance "Hetzner has no server type called '$HETZNER_SERVER_TYPE'" \
            "Type names are lowercase and exact — cpx42, not CPX42 or 'CPX 42'. Run: curl -s -H \"Authorization: Bearer \$HCLOUD_TOKEN\" https://api.hetzner.cloud/v1/server_types | python3 -m json.tool | grep '\"name\"' to see the ones your project can order, then re-run with HETZNER_SERVER_TYPE=<name>. 16 GB of RAM is the floor."
    elif [[ -n "$available" ]]; then
        sw_guidance "Try one of the locations that has the hardware" \
            "Hetzner has no $HETZNER_SERVER_TYPE in ${pinned:-your location} right now, but does in: $available. The Primary IP smallworlds-ip${ENV_SLUG} is datacenter-bound and pins the location, so moving means giving up that address: delete it in the Hetzner Console under Primary IPs and run the installer again — it creates a new one where the hardware is, and rewrites every DNS record to match."
    else
        sw_guidance "No European location has this size right now" \
            "$HETZNER_SERVER_TYPE is unavailable in all of $HETZNER_LOCATIONS. This is usually temporary — Hetzner restocks within hours. Either wait and run the installer again, or choose another size with HETZNER_SERVER_TYPE=cpx41 ./smallworlds-init.sh (16 GB is the floor; smaller nodes saturate once the applications deploy). If the Console shows this size in stock, check the name is exactly right — it is case-sensitive."
    fi
    sw_guidance "Nothing has to be undone" \
        "The installer is safe to re-run: the DNS zone, the SSH key, the Primary IP and any volumes are found and reused rather than created twice."
}

# The size of the node that already exists, if there is one. Empty otherwise.
hetzner_existing_server_type() {
    local response
    response="$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/servers?name=cc-pilot-node-01${ENV_SLUG}")"
    printf '%s' "$response" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
for server in data.get('servers') or []:
    server_type = server.get('server_type')
    # Nested object today; a bare name or an id would both be reasonable
    # futures, and guessing wrong here proposes a resize of a live node.
    if isinstance(server_type, dict):
        print(server_type.get('name') or '')
    elif isinstance(server_type, str):
        print(server_type)
    elif isinstance(server_type, int):
        print('#%d' % server_type)
    break
"
}

# Decide the node size, and say why. Precedence:
#   1. HETZNER_SERVER_TYPE in this run's environment  — an explicit request
#   2. the size the running node actually is          — changing it is a resize
#   3. the size the last run used (cached)            — a second run offers what
#                                                       the first one chose
#   4. the built-in default
resolve_server_type() {
    local existing=""

    if [[ -n "$SW_SERVER_TYPE_FROM_ENV" ]]; then
        HETZNER_SERVER_TYPE="$SW_SERVER_TYPE_FROM_ENV"
    else
        existing="$(hetzner_existing_server_type)"
        if [[ -n "$existing" && "$existing" != \#* ]]; then
            if [[ -n "$HETZNER_SERVER_TYPE" && "$HETZNER_SERVER_TYPE" != "$existing" ]]; then
                echo -e "${YELLOW}Node cc-pilot-node-01${ENV_SLUG} is already a ${existing}; keeping that rather than changing it to ${HETZNER_SERVER_TYPE}.${NC}"
                echo -e "${YELLOW}To resize it deliberately: HETZNER_SERVER_TYPE=<size> ./smallworlds-init.sh${NC}"
            fi
            HETZNER_SERVER_TYPE="$existing"
        fi
    fi

    HETZNER_SERVER_TYPE="$(printf '%s' "${HETZNER_SERVER_TYPE:-$HETZNER_SERVER_TYPE_DEFAULT}" \
        | tr '[:upper:]' '[:lower:]')"
    echo -e "${CYAN}Node size: ${HETZNER_SERVER_TYPE}.${NC}"
}

# Ensure the Primary IP exists, and set $HETZNER_LOCATION to where it lives.
# Idempotent, same pattern as ensure_dns_zone and ensure_ssh_key — and for the
# same reason: it must outlive any one Terraform state. Terraform reads it as a
# data source and follows its location.
ensure_primary_ip() {
    sw_step primary-ip "Ensuring the Hetzner Primary IP exists"
    local ip_name="smallworlds-ip${ENV_SLUG}" found location datacenter

    echo -e "${CYAN}Ensuring Primary IP '$ip_name' exists in Hetzner...${NC}"
    found=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/primary_ips?name=$ip_name" \
        | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)


def location_of(entry):
    \"\"\"Where a Primary IP is, whichever way the API says it.

    Hetzner now reports 'location' on the Primary IP itself; the nested
    'datacenter' object is the older form and is not always sent. Reading only
    the old one put the IP ADDRESS in the location variable and compared that
    against location names for the rest of the install.
    \"\"\"
    location = entry.get('location')
    if isinstance(location, dict):
        return location.get('name') or ''
    if isinstance(location, str) and location:
        return location
    datacenter = entry.get('datacenter')
    if isinstance(datacenter, dict):
        return ((datacenter.get('location') or {}).get('name')) or ''
    if isinstance(datacenter, str) and datacenter:
        # 'fsn1-dc14' -> 'fsn1'
        return datacenter.rsplit('-', 1)[0]
    return ''


for ip in data.get('primary_ips') or []:
    # Tab-separated: an empty field in the middle must not shift the others
    # along, which is exactly how the location silently became the address.
    print('%s\t%s' % (location_of(ip), ip.get('ip') or ''))
    break
")

    hetzner_scan_capacity

    if [[ -n "$found" ]]; then
        HETZNER_LOCATION="$(cut -f1 <<<"$found")"
        local existing_address
        existing_address="$(cut -f2 <<<"$found")"
        if [[ -z "$HETZNER_LOCATION" ]]; then
            # The address exists, so the install can go on; terraform reads the
            # IP itself and will place the node beside it. Only the pre-flight
            # capacity check is lost, which is a warning, not a failure.
            echo -e "${YELLOW}Primary IP $ip_name already exists (${existing_address}), but Hetzner did not say where.${NC}"
            echo -e "${YELLOW}Skipping the placement check — Terraform will follow the IP's own location.${NC}"
            sw_step_ok primary-ip
            return 0
        fi
        echo -e "${GREEN}Primary IP $ip_name already exists in ${HETZNER_LOCATION} (${existing_address}).${NC}"
        # It pins the location, so the only question left is whether the node
        # can still be built there. Asked now rather than by terraform three
        # resources in — but only when the answer is actually known.
        if [[ "$SW_CAPACITY_STATUS" == "ok" ]] && ! hetzner_has_capacity_in "$HETZNER_LOCATION"; then
            echo -e "${RED}Hetzner has no $HETZNER_SERVER_TYPE available in $HETZNER_LOCATION.${NC}"
            sw_step_fail primary-ip "no $HETZNER_SERVER_TYPE in $HETZNER_LOCATION"
            report_capacity_problem "$HETZNER_LOCATION"
            exit 1
        fi
        if [[ "$SW_CAPACITY_STATUS" == "no-type" ]]; then
            echo -e "${RED}Hetzner has no server type called '$HETZNER_SERVER_TYPE'.${NC}"
            sw_step_fail primary-ip "unknown server type $HETZNER_SERVER_TYPE"
            report_capacity_problem "$HETZNER_LOCATION"
            exit 1
        fi
        if [[ "$SW_CAPACITY_STATUS" == "unknown" ]]; then
            echo -e "${YELLOW}Could not check $HETZNER_SERVER_TYPE availability; continuing and letting Hetzner answer.${NC}"
        fi
        sw_step_ok primary-ip
        return 0
    fi

    # Nothing pins the location yet: this is the one moment the scan decides.
    local choice
    choice="$(printf '%s\n' "$SW_CAPACITY" | awk 'NF' | head -1)"
    if [[ -z "$choice" ]]; then
        if [[ "$SW_CAPACITY_STATUS" == "unknown" ]]; then
            # Refusing to create an address because a lookup failed would be a
            # worse outcome than trying: fall back to the first preference and
            # let the Hetzner API give the real answer.
            local fallback_location fallback_datacenter
            fallback_location="$(awk '{print $1}' <<<"$HETZNER_LOCATIONS")"
            fallback_datacenter="$(hetzner_datacenter_in "$fallback_location")"
            if [[ -z "$fallback_datacenter" ]]; then
                echo -e "${RED}Could not reach the Hetzner API to place the Primary IP.${NC}"
                sw_step_fail primary-ip "the Hetzner API did not answer"
                sw_guidance "Check the token and the connection" \
                    "Neither the server types nor the datacenters could be read. Either this machine cannot reach api.hetzner.cloud, or the token is not valid for this project. Everything before this point uses the DNS API, which a token can be scoped differently for."
                sw_guidance "Nothing has to be undone" \
                    "The installer is safe to re-run: the DNS zone, the SSH key, the Primary IP and any volumes are found and reused rather than created twice."
                exit 1
            fi
            choice="$fallback_location $fallback_datacenter"
            echo -e "${YELLOW}Could not check availability; trying ${fallback_location}, the first preferred location.${NC}"
        else
            echo -e "${RED}Cannot place a node: no $HETZNER_SERVER_TYPE offered in $HETZNER_LOCATIONS.${NC}"
            sw_step_fail primary-ip "no $HETZNER_SERVER_TYPE anywhere in $HETZNER_LOCATIONS"
            report_capacity_problem ""
            exit 1
        fi
    fi
    location="$(awk '{print $1}' <<<"$choice")"
    datacenter="$(awk '{print $2}' <<<"$choice")"
    if [[ "$SW_CAPACITY_STATUS" == "ok" ]]; then
        echo -e "${YELLOW}Primary IP not found. Creating it in ${location} (${datacenter}), which has $HETZNER_SERVER_TYPE available...${NC}"
    else
        echo -e "${YELLOW}Primary IP not found. Creating it in ${location} (${datacenter}).${NC}"
    fi

    local api_response created
    api_response=$(curl -s -X POST -H "Content-Type: application/json" \
        -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/primary_ips" \
        -d "{\"name\":\"$ip_name\",\"type\":\"ipv4\",\"datacenter\":\"$datacenter\",\"assignee_type\":\"server\",\"auto_delete\":false}")
    created=$(echo "$api_response" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
except Exception:
    sys.exit(0)
ip = data.get('primary_ip')
if ip:
    print(ip.get('ip') or '')
")
    if [[ -z "$created" ]]; then
        echo -e "${RED}Failed to create the Primary IP: $(echo "$api_response" | python3 -c "
import json, sys
try:
    print((json.load(sys.stdin).get('error') or {}).get('message') or '')
except Exception:
    pass
")${NC}"
        sw_step_fail primary-ip "Hetzner refused to create the Primary IP"
        sw_guidance "Check what the token is allowed to do" \
            "Creating a Primary IP needs a Hetzner Cloud API token with Read & Write on this project. A read-only token passes every earlier check and fails here. Console -> Security -> API tokens."
        exit 1
    fi
    HETZNER_LOCATION="$location"
    echo -e "${GREEN}Primary IP created: $created in ${location}.${NC}"
    sw_step_ok primary-ip
}

# ---------------------------------------------------------------------------
# Talking to a node that did not exist a minute ago
# ---------------------------------------------------------------------------

# Host keys change every time a node is rebuilt and there is no prior trust to
# check against, so checking is theatre. LogLevel=ERROR keeps the resulting
# warning out of the operator's log without hiding real failures.
SW_SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
             -o LogLevel=ERROR -o ConnectTimeout=10 -o BatchMode=yes)

ssh_node() { ssh "${SW_SSH_OPTS[@]}" "root@$SERVER_IP" "$@"; }

# sw_wait_for <seconds> <what> <command...>
#
# Poll until the command succeeds. Two things the old `until ssh ...; sleep 2`
# loops did not do: say something while waiting, and give up. A wait with no
# output and no end is indistinguishable from a hang, and that is exactly how
# it was experienced.
sw_wait_for() {
    local deadline_seconds="$1" description="$2"
    shift 2
    local report_every="${SW_WAIT_REPORT_SECONDS:-30}"
    local started=$SECONDS next_report=$report_every elapsed
    while ! "$@" >/dev/null 2>&1; do
        elapsed=$(( SECONDS - started ))
        if (( elapsed >= deadline_seconds )); then
            return 1
        fi
        if (( elapsed >= next_report )); then
            echo -e "${CYAN}  still waiting for ${description} — ${elapsed}s of up to ${deadline_seconds}s${NC}"
            next_report=$(( next_report + report_every ))
        fi
        sleep 5
    done
    return 0
}

# Wait for cloud-init to finish. Returns 0 done, 1 timed out, 2 cloud-init
# reported an error, 3 could not tell.
#
# This is not politeness. cloud-init's runcmd does
# `rm -rf /var/lib/rancher/k3s/server` and then re-links the directory onto the
# data volume, so ANYTHING copied in before that point is deleted — the
# template says as much about its own write_files. sshd binds early in boot,
# long before runcmd, so "port 22 answers" was never evidence that the node was
# ready to be given the cluster secrets.
wait_for_cloud_init() {
    local deadline_seconds="${SW_CLOUD_INIT_TIMEOUT:-1800}"
    local report_every="${SW_WAIT_REPORT_SECONDS:-30}"
    local started=$SECONDS next_report=$report_every elapsed status unreadable_since=""

    while true; do
        status="$(ssh_node 'cloud-init status 2>/dev/null || true' 2>/dev/null \
                  | awk -F': ' '/^status:/ {print $2; exit}')"
        case "$status" in
            done)     return 0 ;;
            error)    return 2 ;;
            disabled) return 3 ;;
            "")
                # No answer yet: either SSH blipped, or this image has no
                # cloud-init at all. Give it a minute before concluding the
                # latter — an unanswerable question must not become a verdict.
                : "${unreadable_since:=$SECONDS}"
                if (( SECONDS - unreadable_since >= 60 )); then
                    return 3
                fi
                ;;
            *) unreadable_since="" ;;
        esac

        elapsed=$(( SECONDS - started ))
        if (( elapsed >= deadline_seconds )); then
            return 1
        fi
        if (( elapsed >= next_report )); then
            echo -e "${CYAN}  the server is still setting itself up (${status:-no answer yet}) — ${elapsed}s of up to ${deadline_seconds}s${NC}"
            next_report=$(( next_report + report_every ))
        fi
        sleep 5
    done
}

# What to say when a wait ran out. The node exists and is untouched by this,
# which is the first thing an operator watching a long silence wants to know.
report_node_stuck() {
    local what="$1" detail="$2"
    # First, because it is what an operator hits before they can run any of the
    # rest. A rebuilt node keeps its Primary IP and comes up with a new host
    # key, so the ssh commands below refuse to connect until the old one is
    # dropped. The installer's own ssh is unaffected — it does not check.
    sw_guidance "If ssh says the host identification has changed" \
        "That is expected for a node that was rebuilt at the same address: ssh-keygen -R ${SERVER_IP} and try again. It is only a real warning if you did not rebuild the node."
    sw_guidance "Look at the node's own boot log" \
        "ssh root@${SERVER_IP} 'cloud-init status --long; tail -60 /var/log/cloud-init-output.log' — that log is where the first boot installs k3s and pulls the container images, and it names whatever it could not do."
    sw_guidance "Check whether k3s came up at all" \
        "ssh root@${SERVER_IP} 'systemctl status k3s --no-pager; journalctl -u k3s -n 60 --no-pager'"
    sw_guidance "The server is still there, and nothing was rolled back" \
        "${detail:-Waiting for $what timed out; the node itself was not touched.} Re-run ./smallworlds-setup once the boot log says what went wrong — every step finds and reuses what already exists."
}

# Which optional applications this community selected, one per line.
#
# prepare-community-repo.sh records the answers in this file, and the browser
# wizard writes the same one. The DNS records follow it: an A record for
# photos.<domain> with no Immich behind it sends members to a certificate error
# and makes the zone read as though the application were installed.
#
# Parsed in a subshell rather than sourced into this one, so an overlay
# preference file cannot redefine DOMAIN or a credential. A file that predates
# these keys means "no answer recorded", which is not the same as "none
# selected" — fall back to all of them there, since that is the zone such a
# cluster already has.
selected_apps() {
    local config="${SMALLWORLDS_COMMUNITY_CONFIG:-$HOME/.config/smallworlds/community-setup.conf}"
    local all=(forgejo immich nextcloud stalwart bulwark excalidraw jitsi collabora euro-office plane pod-gateway)

    if [[ ! -f "$config" ]]; then
        # Historical installs selected every old optional app. Keep Collabora
        # as that legacy Office choice; never default both exclusive editors.
        local app
        for app in "${all[@]}"; do
            [[ "$app" == "euro-office" ]] || printf '%s\n' "$app"
        done
        return 0
    fi

    local answered
    answered=$(python3 - "$config" "${all[@]}" <<'PYEOF'
import re
import shlex
import sys

path, apps = sys.argv[1], sys.argv[2:]
assignment = re.compile(r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$')
values = {}
with open(path, encoding="utf-8", errors="replace") as handle:
    for line in handle:
        if line.lstrip().startswith("#"):
            continue
        match = assignment.match(line)
        if not match:
            continue
        try:
            parts = shlex.split(match.group(2).strip(), comments=True)
        except ValueError:
            continue
        values[match.group(1)] = parts[0] if parts else ""

keys = {app: "STORED_APP_" + re.sub(r"[^A-Za-z0-9_]", "_", app) for app in apps}
if not any(key in values for key in keys.values()):
    print("\n".join(app for app in apps if app != "euro-office"))
    sys.exit(0)
print("\n".join(app for app in apps if values.get(keys[app]) == "y"))
PYEOF
)
    printf '%s\n' "$answered"
}

# One recorded answer from the overlay preference file, e.g.
# `community_setting STORED_OBJECT_STORAGE`. Parsed, never sourced, for the same
# reason as selected_apps above. Prints nothing when the file or the key is
# absent — "no answer recorded", which callers must not read as a choice.
community_setting() {
    local config="${SMALLWORLDS_COMMUNITY_CONFIG:-$HOME/.config/smallworlds/community-setup.conf}"
    [[ -f "$config" ]] || return 0
    python3 - "$config" "$1" <<'PYEOF'
import re
import shlex
import sys

path, wanted = sys.argv[1], sys.argv[2]
assignment = re.compile(r'^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$')
with open(path, encoding="utf-8", errors="replace") as handle:
    for line in handle:
        match = assignment.match(line)
        if not match or line.lstrip().startswith("#") or match.group(1) != wanted:
            continue
        try:
            parts = shlex.split(match.group(2).strip(), comments=True)
        except ValueError:
            continue
        print(parts[0] if parts else "")
        break
PYEOF
}

# Resolve the overlay repository's current HEAD commit. The bootstrap requires
# the exact reviewed commit rather than a branch name: HEAD must never decide
# what a node deploys between the moment you approve an install and the moment
# Argo CD first syncs. Credentials travel through GIT_ASKPASS instead of the
# URL, so they never reach the process list or git's own error output.
resolve_overlay_head() {
    local url="$1" askpass revision
    askpass="$(mktemp)"
    cat > "$askpass" <<'ASKPASS'
#!/bin/sh
case "$1" in
  *[Uu]sername*) printf '%s' "$GIT_USERNAME" ;;
  *) printf '%s' "$GIT_PASSWORD" ;;
esac
ASKPASS
    chmod 700 "$askpass"
    revision=$(GIT_ASKPASS="$askpass" GIT_TERMINAL_PROMPT=0 \
        GIT_USERNAME="$GITOPS_REPO_USER" GIT_PASSWORD="$GITOPS_REPO_TOKEN" \
        git -c credential.helper= ls-remote "$url" HEAD 2>/dev/null | awk 'NR==1 {print $1}')
    rm -f "$askpass"
    printf '%s' "$revision"
}

# Build (or reuse) the release-pinned bootstrap payload the local bootstrap
# refuses to run without. It is deliberately not an installer that fetches
# "latest" from the network: every k3s installer and Argo CD manifest is
# downloaded once, verified against the SHA-256 declared in
# docs/releases/bootstrap-inputs/, and shipped to the node together with the
# bootstrap script that consumes it. Sets $ASSET_ARCHIVE.
build_bootstrap_assets() {
    local inputs_dir="$REPO_ROOT/docs/releases/bootstrap-inputs"
    local bootstrap_script="$REPO_ROOT/infrastructure/local/bootstrap-local-node.sh"

    if [[ -z "$BOOTSTRAP_RELEASE" ]]; then
        BOOTSTRAP_RELEASE=$(find "$inputs_dir" -maxdepth 1 -name 'v[0-9]*.json' -printf '%f\n' 2>/dev/null \
            | sed 's/\.json$//' | sort -V | tail -1)
    fi
    local inputs="$inputs_dir/${BOOTSTRAP_RELEASE}.json"
    if [[ -z "$BOOTSTRAP_RELEASE" || ! -f "$inputs" ]]; then
        echo -e "${RED}No bootstrap inputs found for release '${BOOTSTRAP_RELEASE:-<none>}' in $inputs_dir.${NC}" >&2
        exit 1
    fi

    local cache="$HOME/.smallworlds/bootstrap-assets"
    ASSET_ARCHIVE="$cache/smallworlds-bootstrap-${BOOTSTRAP_RELEASE}-linux-amd64.tar.gz"

    # Rebuild when the bootstrap script has moved on: the archive carries a copy
    # of it, so a cached payload would otherwise ship a stale script alongside
    # current manifests.
    if [[ -f "$ASSET_ARCHIVE" && ! "$bootstrap_script" -nt "$ASSET_ARCHIVE" ]]; then
        echo -e "${GREEN}Using cached bootstrap assets for ${BOOTSTRAP_RELEASE}.${NC}"
        return
    fi

    echo -e "${CYAN}Building bootstrap assets for ${BOOTSTRAP_RELEASE} (verified downloads)...${NC}"
    mkdir -p "$cache"
    local k3s_version k3s_url k3s_sha argocd_version argocd_url argocd_sha
    eval "$(python3 - "$inputs" <<'PY'
import json, shlex, sys
document = json.load(open(sys.argv[1]))
values = {
    "k3s_version": document["k3s"]["version"],
    "k3s_url": document["k3s"]["installerUrl"],
    "k3s_sha": document["k3s"]["installerSHA256"],
    "argocd_version": document["argocd"]["version"],
    "argocd_url": document["argocd"]["manifestUrl"],
    "argocd_sha": document["argocd"]["manifestSHA256"],
}
for key, value in values.items():
    print(f"{key}={shlex.quote(value)}")
PY
)"
    "$REPO_ROOT/admin-tools/build-bootstrap-assets.sh" \
        --release "$BOOTSTRAP_RELEASE" \
        --k3s-version "$k3s_version" \
        --k3s-installer-url "$k3s_url" \
        --k3s-installer-sha256 "$k3s_sha" \
        --argocd-version "$argocd_version" \
        --argocd-manifest-url "$argocd_url" \
        --argocd-manifest-sha256 "$argocd_sha" \
        --output-directory "$cache" >/dev/null
}

echo -e "${YELLOW}Gathering Configuration...${NC}"
echo -e "Deployment targets:"
echo -e "  ${CYAN}hetzner${NC} — provision a Hetzner Cloud VM + DNS via Terraform (public, internet-facing)"
echo -e "  ${CYAN}local${NC}   — bootstrap an existing Linux machine in your LAN (e.g. a laptop/mini-PC)"
if [[ -z "$DEPLOY_TARGET" ]]; then
    DEPLOY_TARGET="hetzner"
fi
ask_with_default "1. Select deployment target (hetzner or local)" "DEPLOY_TARGET" "false" "required"
if [[ "$DEPLOY_TARGET" != "hetzner" && "$DEPLOY_TARGET" != "local" ]]; then
    echo -e "${YELLOW}Unknown target '$DEPLOY_TARGET' — must be 'hetzner' or 'local'.${NC}"
    exit 1
fi

if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    echo -e "${YELLOW}Note: You must manually register your domain with a registrar of your choice.${NC}"
    echo -e "${YELLOW}This script will only configure the DNS zone in Hetzner Cloud (which is free).${NC}"
    echo -e "${YELLOW}Domain registration itself is not automated and will incur costs at your registrar.${NC}"
else
    echo -e "${YELLOW}Note: For a LAN-only deployment the domain does not need to be registered anywhere —${NC}"
    echo -e "${YELLOW}name resolution happens inside your LAN (router DNS, Pi-hole, or /etc/hosts entries).${NC}"
    echo -e "${YELLOW}If you choose internet exposure later in this wizard, the domain MUST be registered,${NC}"
    echo -e "${YELLOW}with its nameservers pointed at Hetzner DNS (helium/oxygen/hydrogen.ns.hetzner.*).${NC}"
fi
ask_with_default "2. Enter your target domain (e.g. smallworlds.network)" "DOMAIN" "false" "required"
ask_with_default "3. Enter the admin email address" "ADMIN_EMAIL" "false" "required"

# Ensure ONBOARDING_MODE has a valid default if empty
if [[ -z "$ONBOARDING_MODE" ]]; then
    ONBOARDING_MODE="invitation"
fi
ask_with_default "4. Select onboarding mode (invitation or self-registration)" "ONBOARDING_MODE" "false" "required"

ask_with_default "5. Enter environment extension (e.g. .dev, or leave empty for prod)" "ENV_EXT" "false"

echo ""
echo -e "${YELLOW}GitOps Repository Configuration${NC}"
ask_with_default "6. Enter your Git repository URL (e.g., https://github.com/my-community/config.git)" "GITOPS_REPO_URL" "false" "required"
ask_with_default "7. Enter your Git username" "GITOPS_REPO_USER" "false" "required"
ask_with_default "8. Paste your Git Access Token" "GITOPS_REPO_TOKEN" "true" "required"

# Object storage: the in-cluster Garage, or an external S3 provider that
# replaces it (docs/adr/0053). The overlay was already written for one or the
# other by prepare-community-repo.sh, and the two have to agree — an overlay
# without Garage on a node that was given no provider key has nowhere to put a
# single object. So what the overlay recorded is the answer where it exists,
# and only the credentials, which never go into the overlay, are asked here.
RECORDED_OBJECT_STORAGE="$(community_setting STORED_OBJECT_STORAGE)"
if [[ -n "$RECORDED_OBJECT_STORAGE" ]]; then
    OBJECT_STORAGE="$RECORDED_OBJECT_STORAGE"
    echo -e "Object storage: ${CYAN}${OBJECT_STORAGE}${NC} (as recorded when the overlay was prepared)"
else
    if [[ -z "$OBJECT_STORAGE" ]]; then
        OBJECT_STORAGE="garage"
    fi
    ask_with_default "Object storage: garage (in-cluster) or external (an S3 provider)" "OBJECT_STORAGE" "false" "required"
fi
case "$OBJECT_STORAGE" in
    garage|external) ;;
    *)
        echo -e "${YELLOW}Unknown object storage '$OBJECT_STORAGE' — must be 'garage' or 'external'.${NC}"
        exit 1
        ;;
esac
if [[ "$OBJECT_STORAGE" == "external" ]]; then
    if [[ -n "$RECORDED_OBJECT_STORAGE" ]]; then
        S3_ENDPOINT="$(community_setting STORED_S3_ENDPOINT)"
        S3_REGION="$(community_setting STORED_S3_REGION)"
        S3_BUCKET_PREFIX="$(community_setting STORED_S3_BUCKET_PREFIX)"
        S3_BACKUP_ENDPOINT="$(community_setting STORED_S3_BACKUP_ENDPOINT)"
        S3_BACKUP_REGION="$(community_setting STORED_S3_BACKUP_REGION)"
        echo -e "  endpoint ${CYAN}${S3_ENDPOINT}${NC}, region ${CYAN}${S3_REGION}${NC}, bucket prefix ${CYAN}${S3_BUCKET_PREFIX}${NC}"
        if [[ -z "$S3_ENDPOINT" || -z "$S3_REGION" || -z "$S3_BUCKET_PREFIX" ]]; then
            echo -e "${RED}The overlay records external object storage, but not where it is.${NC}" >&2
            sw_guidance "Prepare the overlay again" \
                "Run admin-tools/prepare-community-repo.sh and answer the object storage questions; it writes the endpoint, region and bucket prefix into the overlay and records them for this installer."
            exit 1
        fi
    else
        ask_with_default "S3 endpoint URL of the provider (e.g. https://s3.example.com)" "S3_ENDPOINT" "false" "required"
        ask_with_default "S3 region" "S3_REGION" "false" "required"
        ask_with_default "Prefix for the bucket names" "S3_BUCKET_PREFIX" "false" "required"
    fi
    ask_with_default "S3 access key ID" "S3_ACCESS_KEY_ID" "false" "required"
    ask_with_default "S3 secret access key" "S3_SECRET_ACCESS_KEY" "true" "required"
    if [[ -z "$RECORDED_OBJECT_STORAGE" ]]; then
        ask_with_default "Separate S3 endpoint for backups (leave empty to use the same provider)" "S3_BACKUP_ENDPOINT" "false"
        if [[ -n "$S3_BACKUP_ENDPOINT" ]]; then
            ask_with_default "S3 region of the backup endpoint" "S3_BACKUP_REGION" "false" "required"
        fi
    fi
    if [[ -n "$S3_BACKUP_ENDPOINT" ]]; then
        ask_with_default "S3 access key ID for backups" "S3_BACKUP_ACCESS_KEY_ID" "false" "required"
        ask_with_default "S3 secret access key for backups" "S3_BACKUP_SECRET_ACCESS_KEY" "true" "required"
    else
        S3_BACKUP_REGION="" S3_BACKUP_ACCESS_KEY_ID="" S3_BACKUP_SECRET_ACCESS_KEY=""
    fi
fi

if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    echo ""
    echo -e "${YELLOW}Hetzner Configuration${NC}"
    ask_with_default "9. Paste your Hetzner Cloud API Token" "HCLOUD_TOKEN" "true" "required"
else
    echo ""
    echo -e "${YELLOW}Local Server Configuration${NC}"
    echo -e "The target machine needs: a systemd-based Linux, SSH access, and sudo/root rights."
    echo -e "Use ${CYAN}user@host${NC} (e.g. root@192.168.1.50), or ${CYAN}localhost${NC} to install on THIS machine."
    ask_with_default "9. Enter the SSH target of your local server" "LOCAL_SSH_TARGET" "false" "required"
    if [[ -z "$DATA_DIR" ]]; then
        DATA_DIR="/var/lib/smallworlds-data"
    fi
    ask_with_default "10. Enter the data directory on the server (all user data lives here)" "DATA_DIR" "false" "required"
    # Only plain Immich keeps photos on the node (Gallery keeps them in S3,
    # docs/adr/0054), and the library is the one thing that outgrows a small
    # fast disk — so it alone may be pointed at another one.
    if selected_apps | grep -qx immich && [[ "$(community_setting STORED_PHOTO_BACKEND)" != "gallery" ]]; then
        echo -e "The photo library can live on a ${CYAN}second, larger disk${NC}. Leave empty to keep it"
        echo -e "inside the data directory."
        ask_with_default "Photo library directory (optional)" "LIBRARY_DIR" "false"
    else
        LIBRARY_DIR=""
    fi
    if [[ -z "$BACKUP_DIR" ]]; then
        BACKUP_DIR="/var/lib/smallworlds-backup"
    fi
    if [[ "$OBJECT_STORAGE" == "external" ]]; then
        # Every Recovery Point goes to the provider, which is further from the
        # operational disk than a second disk in the same machine ever was — so
        # there is no backup directory to ask for (docs/adr/0053).
        echo ""
        echo -e "Backups go to the external S3 provider, so ${CYAN}no second disk is needed${NC}."
    else
        echo ""
        echo -e "Backups must survive the loss of the operational disk, so they need a"
        echo -e "${CYAN}second physical disk${NC} — the bootstrap refuses to continue if this ends up"
        echo -e "on the same device as the data directory (see docs/adr/0048)."
        ask_with_default "11. Enter the backup directory on the server (on a second disk)" "BACKUP_DIR" "false" "required"
    fi

    echo ""
    echo -e "Certificates are issued by Let's Encrypt over a ${CYAN}DNS-01${NC} challenge: cert-manager"
    echo -e "writes a TXT record into Hetzner DNS, so this needs ${CYAN}no port forwarding and no${NC}"
    echo -e "${CYAN}public IP${NC} — a LAN-only cluster gets publicly trusted certificates too."
    echo -e "Requires: a domain whose nameservers are Hetzner DNS, and an API token."
    echo -e ""
    echo -e "${YELLOW}Self-signed is the fallback, and it costs more than a browser warning:${NC} every"
    echo -e "app validates Keycloak's certificate against its own trust store with no override,"
    echo -e "so ${YELLOW}single sign-on does not work at all${NC} — Immich, Nextcloud and Jitsi cannot"
    echo -e "complete OIDC discovery. Devices pinning a certificate (the pod archive) also have"
    echo -e "to be re-pinned at every renewal. Choose it only for a throwaway cluster."
    if [[ -z "$LOCAL_TLS" ]]; then
        LOCAL_TLS="letsencrypt"
    fi
    ask_with_default "12. Certificates: letsencrypt or self-signed" "LOCAL_TLS" "false" "required"
    case "$LOCAL_TLS" in
        self-signed|selfsigned|self|s) LOCAL_TLS="self-signed";;
        *) LOCAL_TLS="letsencrypt";;
    esac

    echo ""
    echo -e "Separately, the apps can be reachable ${CYAN}from the internet${NC}: A records for your"
    echo -e "domain are then kept pointed at your connection's public IP by an in-cluster DDNS"
    echo -e "job. Answering no keeps the cluster LAN-only, resolved through /etc/hosts."
    echo -e "Requires: a public IPv4 (no CGNAT) and these forwards on your router to the"
    echo -e "server: ${CYAN}80/tcp, 443/tcp, 10000/udp${NC}."
    if [[ -z "$LOCAL_PUBLIC" ]]; then
        LOCAL_PUBLIC="no"
    fi
    ask_with_default "13. Expose the apps on the internet? (yes/no)" "LOCAL_PUBLIC" "false" "required"
    case "$LOCAL_PUBLIC" in
        y|Y|yes|Yes|YES) LOCAL_PUBLIC="yes";;
        *) LOCAL_PUBLIC="no";;
    esac
    # A rented server has no router in front of it, so nothing closes the
    # Kubernetes ports unless the machine does it itself (docs/adr/0055). Asked
    # only for an exposed machine, and never assumed: behind a router this
    # would add nothing, and a wrong guess about the SSH port locks one out.
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        if [[ -z "$LOCAL_FIREWALL" ]]; then
            LOCAL_FIREWALL="no"
        fi
        echo ""
        echo -e "If this machine is ${CYAN}directly on the internet${NC} — a rented VPS with a public"
        echo -e "address and no router in front — the installer should close every inbound"
        echo -e "port except SSH, the web ports and those the selected apps need."
        ask_with_default "Set up a host firewall on the machine? (yes/no)" "LOCAL_FIREWALL" "false" "required"
        case "$LOCAL_FIREWALL" in
            y|Y|yes|Yes|YES) LOCAL_FIREWALL="yes";;
            *) LOCAL_FIREWALL="no";;
        esac
    else
        LOCAL_FIREWALL="no"
    fi
    # Who keeps the DNS records (docs/adr/0056). "hetzner": the installer does,
    # through the Hetzner DNS API, which needs the domain delegated there and a
    # token. "manual": you create one wildcard record at whatever DNS provider
    # you already use, and certificates are proven over HTTP instead of DNS —
    # no Hetzner account at all. Manual is only offered for an exposed machine:
    # the HTTP proof is Let's Encrypt fetching a file from port 80, which a
    # LAN-only cluster cannot answer.
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        if [[ -z "$LOCAL_DNS" ]]; then
            LOCAL_DNS="hetzner"
        fi
        echo ""
        echo -e "DNS records can be ${CYAN}managed for you${NC} through Hetzner DNS (needs the domain's"
        echo -e "nameservers pointed at Hetzner and an API token), or kept ${CYAN}by hand${NC}: you create"
        echo -e "one wildcard record ${CYAN}*${ENV_EXT}.${DOMAIN}${NC} pointing at the server, at any DNS provider."
        ask_with_default "DNS records: hetzner (managed) or manual" "LOCAL_DNS" "false" "required"
        case "$LOCAL_DNS" in
            manual|m|hand) LOCAL_DNS="manual";;
            *) LOCAL_DNS="hetzner";;
        esac
    else
        LOCAL_DNS="hetzner"
    fi
    # Both managed paths talk to the same Hetzner DNS API: DNS-01 to solve
    # challenges, DDNS to maintain the A records.
    if [[ "$LOCAL_DNS" != "manual" && ( "$LOCAL_TLS" == "letsencrypt" || "$LOCAL_PUBLIC" == "yes" ) ]]; then
        ask_with_default "14. Paste your Hetzner API Token (used for DNS records only)" "HCLOUD_TOKEN" "true" "required"
    fi
fi

# Auto-convert SSH URLs to HTTPS if access token is used
if [[ -n "$GITOPS_REPO_TOKEN" ]]; then
    if [[ "$GITOPS_REPO_URL" =~ ^git@([^:]+):(.+)$ ]]; then
        echo -e "${YELLOW}Auto-converting SSH Git URL to HTTPS for PAT authentication...${NC}"
        GITOPS_REPO_URL="https://${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
    elif [[ "$GITOPS_REPO_URL" =~ ^ssh://git@([^/]+)/(.+)$ ]]; then
        echo -e "${YELLOW}Auto-converting SSH Git URL to HTTPS for PAT authentication...${NC}"
        GITOPS_REPO_URL="https://${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
    fi
fi

# The profile that owns this installation. The bootstrap records it on the node
# and refuses to adopt a node claimed by a different one, so it has to stay
# stable across re-runs — hence derived from the cluster's identity and cached,
# never regenerated.
if [[ -z "$PROFILE_ID" ]]; then
    PROFILE_ID="smallworlds-$(cluster_label "$ENV_EXT")-${DOMAIN}"
fi

# Generate passwords if not cached
if [[ -z "$KC_PASS" ]]; then KC_PASS=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi
# Human administrator of the Keycloak master realm. Separate from `admin`,
# which is the automation credential every OIDC client-registration job
# authenticates with and therefore cannot carry a second factor.
if [[ -z "$KC_OPERATOR_PASS" ]]; then KC_OPERATOR_PASS=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi
if [[ -z "$INVITE_SECRET" ]]; then INVITE_SECRET=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi
if [[ -z "$GARAGE_RPC_SECRET" ]]; then GARAGE_RPC_SECRET=$(openssl rand -hex 32); fi
if [[ -z "$GARAGE_ADMIN_TOKEN" ]]; then GARAGE_ADMIN_TOKEN=$(openssl rand -hex 32); fi
# Distinct credentials for the backup instance — see docs/adr/0048.
if [[ -z "$GARAGE_BACKUP_RPC_SECRET" ]]; then GARAGE_BACKUP_RPC_SECRET=$(openssl rand -hex 32); fi
if [[ -z "$GARAGE_BACKUP_ADMIN_TOKEN" ]]; then GARAGE_BACKUP_ADMIN_TOKEN=$(openssl rand -hex 32); fi
# Signs the in-cluster Operator Console's session cookies. DEACTIVATED
# (2026-08-23) along with the console itself — see the note in
# infrastructure/kubernetes/kustomization.yaml. Kept, not deleted: an existing
# cache file still carries the value, so re-activating the console restores the
# same key rather than logging everyone out.
# if [[ -z "$CONSOLE_SESSION_KEY" ]]; then CONSOLE_SESSION_KEY=$(openssl rand -hex 32); fi
if [[ -z "$GRAFANA_PASS" ]]; then GRAFANA_PASS=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 32); fi

# Save values to cache for next time
cat <<EOF > "$CACHE_FILE"
DEPLOY_TARGET="${DEPLOY_TARGET}"
LOCAL_SSH_TARGET="${LOCAL_SSH_TARGET}"
DATA_DIR="${DATA_DIR}"
BACKUP_DIR="${BACKUP_DIR}"
PROFILE_ID="${PROFILE_ID}"
BOOTSTRAP_RELEASE="${BOOTSTRAP_RELEASE}"
LOCAL_PUBLIC="${LOCAL_PUBLIC}"
LOCAL_TLS="${LOCAL_TLS}"
LOCAL_FIREWALL="${LOCAL_FIREWALL}"
LOCAL_DNS="${LOCAL_DNS}"
LIBRARY_DIR="${LIBRARY_DIR}"
DOMAIN="${DOMAIN}"
ENV_EXT="${ENV_EXT}"
ADMIN_EMAIL="${ADMIN_EMAIL}"
ONBOARDING_MODE="${ONBOARDING_MODE}"
HCLOUD_TOKEN="${HCLOUD_TOKEN}"
HETZNER_SERVER_TYPE="${HETZNER_SERVER_TYPE}"
GITOPS_REPO_URL="${GITOPS_REPO_URL}"
GITOPS_REPO_USER="${GITOPS_REPO_USER}"
GITOPS_REPO_TOKEN="${GITOPS_REPO_TOKEN}"
KC_PASS="${KC_PASS}"
KC_OPERATOR_PASS="${KC_OPERATOR_PASS}"
INVITE_SECRET="${INVITE_SECRET}"
GARAGE_RPC_SECRET="${GARAGE_RPC_SECRET}"
CONSOLE_SESSION_KEY="${CONSOLE_SESSION_KEY}"
GARAGE_ADMIN_TOKEN="${GARAGE_ADMIN_TOKEN}"
GARAGE_BACKUP_RPC_SECRET="${GARAGE_BACKUP_RPC_SECRET}"
GARAGE_BACKUP_ADMIN_TOKEN="${GARAGE_BACKUP_ADMIN_TOKEN}"
GRAFANA_PASS="${GRAFANA_PASS}"
OBJECT_STORAGE="${OBJECT_STORAGE}"
S3_ENDPOINT="${S3_ENDPOINT}"
S3_REGION="${S3_REGION}"
S3_BUCKET_PREFIX="${S3_BUCKET_PREFIX}"
S3_ACCESS_KEY_ID="${S3_ACCESS_KEY_ID}"
S3_SECRET_ACCESS_KEY="${S3_SECRET_ACCESS_KEY}"
S3_BACKUP_ENDPOINT="${S3_BACKUP_ENDPOINT}"
S3_BACKUP_REGION="${S3_BACKUP_REGION}"
S3_BACKUP_ACCESS_KEY_ID="${S3_BACKUP_ACCESS_KEY_ID}"
S3_BACKUP_SECRET_ACCESS_KEY="${S3_BACKUP_SECRET_ACCESS_KEY}"
EOF
chmod 600 "$CACHE_FILE"

echo ""

echo -e "${CYAN}Generating configuration...${NC}"

# Update ONBOARDING_MODE in the job manifest
sed -i -E "s/value: \"(invitation|self-registration)\"/value: \"$ONBOARDING_MODE\"/g" infrastructure/kubernetes/tenants/keycloak/realm-config-job.yaml

# Token that lands in cluster secrets (stalwart-dns-secrets and cert-manager's
# `hetzner`): withheld only from a local deployment that does no DNS at all —
# self-signed certificates AND no public exposure. Withholding it whenever the
# cluster was merely LAN-only used to leave cert-manager with an empty token
# while the ACME issuer was live, so every certificate stayed pending with
# nothing saying why. Never blank HCLOUD_TOKEN itself: it stays cached for
# future hetzner or internet-exposed runs.
SECRETS_HCLOUD_TOKEN="$HCLOUD_TOKEN"
if [[ "$DEPLOY_TARGET" == "local" && "$LOCAL_TLS" != "letsencrypt" && "$LOCAL_PUBLIC" != "yes" ]]; then
    SECRETS_HCLOUD_TOKEN=""
fi
# With DNS kept by hand nothing in the cluster talks to Hetzner, whatever a
# cache file from an earlier Hetzner install still holds.
if [[ "$DEPLOY_TARGET" == "local" && "$LOCAL_DNS" == "manual" ]]; then
    SECRETS_HCLOUD_TOKEN=""
fi

# Generate the secrets manifest — deployed into the node's k3s auto-apply
# manifests directory on both deployment targets
SECRETS_FILE="/tmp/smallworlds-${DOMAIN}-secrets.yaml"
cat <<EOF > "$SECRETS_FILE"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: smallworlds-global-config
  namespace: default
data:
  ADMIN_EMAIL: "${ADMIN_EMAIL}"
  DOMAIN: "${DOMAIN}"
  ENV_EXT: "${ENV_EXT}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: keycloak
---
apiVersion: v1
kind: Secret
metadata:
  name: keycloak-admin-creds
  namespace: keycloak
type: Opaque
stringData:
  admin-password: "${KC_PASS}"
  operator-password: "${KC_OPERATOR_PASS}"
  bulk-invite-secret: "${INVITE_SECRET}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: stalwart
---
apiVersion: v1
kind: Secret
metadata:
  name: stalwart-dns-secrets
  namespace: stalwart
type: Opaque
stringData:
  HCLOUD_TOKEN: "${SECRETS_HCLOUD_TOKEN}"
  DOMAIN: "${DOMAIN}"
  ENV_EXT: "${ENV_EXT}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager
---
apiVersion: v1
kind: Secret
metadata:
  name: hetzner
  namespace: cert-manager
type: Opaque
stringData:
  token: "${SECRETS_HCLOUD_TOKEN}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: argocd
---
apiVersion: v1
kind: Secret
metadata:
  name: repo-git-creds
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
stringData:
  url: "${GITOPS_REPO_URL}"
  username: "${GITOPS_REPO_USER}"
  password: "${GITOPS_REPO_TOKEN}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
---
apiVersion: v1
kind: Secret
metadata:
  name: grafana-admin-creds
  namespace: monitoring
type: Opaque
stringData:
  admin-user: "admin"
  admin-password: "${GRAFANA_PASS}"
EOF

if [[ "$OBJECT_STORAGE" == "external" ]]; then
    # The whole switch, as far as the cluster is concerned: every init job that
    # would otherwise mint a key on Garage finds this Secret and uses the
    # provider instead (bases/garage-init-job, docs/adr/0053). Empty backup*
    # values mean "the same account as the primary".
    cat <<EOF >> "$SECRETS_FILE"
---
apiVersion: v1
kind: Secret
metadata:
  name: smallworlds-object-storage
  namespace: default
type: Opaque
stringData:
  endpoint: "${S3_ENDPOINT}"
  region: "${S3_REGION}"
  bucketPrefix: "${S3_BUCKET_PREFIX}"
  accessKeyId: "${S3_ACCESS_KEY_ID}"
  secretAccessKey: "${S3_SECRET_ACCESS_KEY}"
  backupEndpoint: "${S3_BACKUP_ENDPOINT}"
  backupRegion: "${S3_BACKUP_REGION}"
  backupAccessKeyId: "${S3_BACKUP_ACCESS_KEY_ID}"
  backupSecretAccessKey: "${S3_BACKUP_SECRET_ACCESS_KEY}"
EOF
else
    cat <<EOF >> "$SECRETS_FILE"
---
apiVersion: v1
kind: Namespace
metadata:
  name: garage-system
---
apiVersion: v1
kind: Secret
metadata:
  name: garage-auth-secret
  namespace: garage-system
type: Opaque
stringData:
  rpcSecret: "${GARAGE_RPC_SECRET}"
  adminToken: "${GARAGE_ADMIN_TOKEN}"
---
apiVersion: v1
kind: Namespace
metadata:
  name: garage-backup-system
---
# The backup-only Garage instance (docs/adr/0048). Its own RPC secret and admin
# token: the two instances are separate clusters that must never join each
# other's gossip, and a credential that reaches the operational instance should
# not also reach the backups.
apiVersion: v1
kind: Secret
metadata:
  name: garage-auth-secret
  namespace: garage-backup-system
type: Opaque
stringData:
  rpcSecret: "${GARAGE_BACKUP_RPC_SECRET}"
  adminToken: "${GARAGE_BACKUP_ADMIN_TOKEN}"
EOF
fi
chmod 600 "$SECRETS_FILE"

if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    # ==================================================================
    # Target: Hetzner Cloud — Terraform provisions VM + DNS, cloud-init
    # bootstraps k3s/ArgoCD on first boot.
    # ==================================================================

    # Export Hetzner Token as environment variable so Terraform can find it
    export HCLOUD_TOKEN=$HCLOUD_TOKEN

    # Hetzner resource names use the dash form of the extension: ".dev" is DNS
    # syntax, "-dev" is what a resource is called. Mirrors local.env_slug in
    # infrastructure/terraform/main.tf.
    ENV_SLUG="${ENV_EXT//./-}"

    # Ensure DNS Zone exists in Hetzner Cloud DNS
    ensure_dns_zone

    # Ensure the shared admin SSH key exists (sets $SSH_KEY_ID)
    ensure_ssh_key

    # Settle the node size before anything asks Hetzner about capacity: the
    # scan, the tfvars and every failure message all quote it.
    resolve_server_type

    # Ensure the Primary IP exists and decide where the node goes (sets
    # $HETZNER_LOCATION). Terraform reads the IP as a data source, so this has
    # to happen first — and it is where a location that cannot sell the server
    # type is caught, while the community still has no address tied to one.
    ensure_primary_ip

    # Boot from the golden image (preloaded k3s + container images) if one exists
    GOLDEN_COUNT=$(curl -s -H "Authorization: Bearer $HCLOUD_TOKEN" \
        "https://api.hetzner.cloud/v1/images?type=snapshot&label_selector=smallworlds-golden%3Dtrue" \
        | grep -c '"id"' || true)
    if [ "$GOLDEN_COUNT" -gt 0 ]; then
        echo -e "${GREEN}Golden image found — fast boot enabled (skips updates, k3s download and image pulls).${NC}"
        export TF_VAR_use_golden_image=true
    else
        echo -e "${YELLOW}No golden image found — booting plain Ubuntu (build one with admin-tools/build-golden-image.sh).${NC}"
    fi

    # Set Terraform Git variables
    TF_GIT_USER="${GITOPS_REPO_USER}"
    TF_GIT_TOKEN="${GITOPS_REPO_TOKEN}"

    # 2. Generate temporary tfvars file
    TFVARS_FILE="/tmp/smallworlds-${DOMAIN}.tfvars"
    # The selected applications decide which A records exist, so the list is
    # rendered as an HCL array here rather than left to a default.
    TF_APPS_LIST="$(selected_apps | python3 -c "
import json, sys
print(', '.join(json.dumps(line) for line in sys.stdin.read().split()))
")"
    cat <<EOF > "$TFVARS_FILE"
domain_name       = "${DOMAIN}"
env_ext           = "${ENV_EXT}"
git_url        = "${GITOPS_REPO_URL}"
git_username   = "${TF_GIT_USER}"
git_password   = "${TF_GIT_TOKEN}"
hcloud_token      = "${HCLOUD_TOKEN}"
ssh_key_id        = ${SSH_KEY_ID}
location          = "${HETZNER_LOCATION}"
server_type       = "${HETZNER_SERVER_TYPE}"
apps              = [${TF_APPS_LIST}]

EOF
    _SELECTED_LIST="$(selected_apps | paste -sd, - | sed 's/,/, /g')"
    echo -e "${CYAN}DNS records: identity, dashboard, monitoring, deploy, vpn${_SELECTED_LIST:+, and one for each of: }${_SELECTED_LIST}${NC}"

    # 3. Execute Terraform
    sw_step terraform-apply "Provisioning the Hetzner VM and DNS records"
    echo -e "${CYAN}Initializing infrastructure... This will take a few minutes.${NC}"
    cd infrastructure/terraform

    terraform init -input=false > /dev/null

    # SMALLWORLDS_REPLACE_NODE=1 rebuilds the node from scratch.
    #
    # cloud-init only runs on a node's first boot, and the server resource
    # ignores changes to user_data, so a fix to the bootstrap template reaches a
    # running node by exactly one route: replacing it. Doing that by hand means
    # detaching both volumes first — Hetzner refuses to delete a server while
    # they are attached — and Terraform already knows that order: destroy the
    # attachments, destroy the server, create the new one, re-attach.
    #
    # The volumes and the Primary IP survive. They carry the community's data
    # and its address, and neither is replaced here.
    TF_APPLY_ARGS=()
    if [[ "${SMALLWORLDS_REPLACE_NODE:-}" == "1" ]]; then
        if terraform state list 2>/dev/null | grep -qx 'hcloud_server.smallworlds_pilot_node'; then
            echo -e "${YELLOW}SMALLWORLDS_REPLACE_NODE=1 — the node will be destroyed and rebuilt.${NC}"
            echo -e "${YELLOW}Both volumes and the Primary IP are kept; anything living only on the${NC}"
            echo -e "${YELLOW}node's own disk is not. Argo CD re-deploys the cluster from your overlay.${NC}"
            TF_APPLY_ARGS+=(-replace=hcloud_server.smallworlds_pilot_node)
        else
            # -replace on a resource Terraform does not have would abort the
            # apply, and there is nothing to replace anyway.
            echo -e "${CYAN}No existing node in the Terraform state — it will simply be created.${NC}"
        fi
    fi

    if ! terraform apply ${TF_APPLY_ARGS[@]+"${TF_APPLY_ARGS[@]}"} -var-file="$TFVARS_FILE" -auto-approve; then
        rm -f "$TFVARS_FILE"
        sw_step_fail terraform-apply "Hetzner could not build the server"
        echo -e "${RED}Terraform could not provision the node.${NC}"
        # Capacity is the common cause and the one an operator can act on, so
        # ask the API again and say what it answers. Anything else falls
        # through to advice that fits any Terraform failure.
        hetzner_scan_capacity
        if [[ "$SW_CAPACITY_STATUS" == "no-type" ]] || \
           { [[ "$SW_CAPACITY_STATUS" == "ok" ]] && ! hetzner_has_capacity_in "$HETZNER_LOCATION"; }; then
            report_capacity_problem "$HETZNER_LOCATION"
        else
            sw_guidance "Read the last Terraform error above" \
                "It names the resource and the Hetzner API's reason. Quota limits (servers, volumes, Primary IPs) are the usual one and are raised from Console -> Limits; a read-only API token is the other."
            sw_guidance "Nothing has to be undone" \
                "The installer is safe to re-run: the DNS zone, the SSH key, the Primary IP and any volumes are found and reused rather than created twice."
        fi
        exit 1
    fi
    sw_step_ok terraform-apply

    # 4. Capture Outputs
    SERVER_IP=$(terraform output -raw server_ipv4)
    cd ../..
    rm -f "$TFVARS_FILE"

    # 5. Wait for the node to finish building itself, then plant the secrets
    sw_step node-ready "Waiting for the server to finish setting itself up"
    echo -e "${CYAN}Waiting for SSH on $SERVER_IP...${NC}"
    if ! sw_wait_for "${SW_SSH_TIMEOUT:-300}" "SSH" ssh_node true; then
        echo -e "${RED}Could not log in to $SERVER_IP over SSH.${NC}"
        echo -e "${YELLOW}$(ssh "${SW_SSH_OPTS[@]}" "root@$SERVER_IP" true 2>&1 | tail -3)${NC}"
        sw_step_fail node-ready "no SSH access to the new node"
        sw_guidance "If ssh says the host identification has changed" \
            "A node rebuilt at the same address comes up with a new host key: ssh-keygen -R ${SERVER_IP}, then try again."
        sw_guidance "Check the key the node was given" \
            "The node trusts the key uploaded as 'SmallWorlds Admin Key' — the public half of ${SSH_PUBLIC_KEY_PATH:-$HOME/.ssh/id_ed25519.pub}. If you have more than one key, or the agent is offering a different one, ssh -i <the matching private key> root@${SERVER_IP} will say so."
        sw_guidance "The server is still there, and nothing was rolled back" \
            "Re-run ./smallworlds-setup once you can log in — every step finds and reuses what already exists."
        exit 1
    fi
    echo -e "${GREEN}SSH is up.${NC}"

    # The node is not ready just because it answers. cloud-init's runcmd purges
    # /var/lib/rancher/k3s/server and relocates the directory onto the data
    # volume, so secrets planted before it finishes are deleted by it.
    echo -e "${CYAN}Waiting for the first boot to finish — it installs k3s and pulls the container images...${NC}"
    cloud_init_state=0
    wait_for_cloud_init || cloud_init_state=$?
    case "$cloud_init_state" in
        0)
            echo -e "${GREEN}The server finished setting itself up.${NC}"
            ;;
        2)
            echo -e "${RED}cloud-init reported an error on the node.${NC}"
            sw_step_fail node-ready "the first boot failed"
            report_node_stuck "the first boot" \
                "cloud-init ran and failed. The node exists and is reachable; nothing has been rolled back."
            exit 1
            ;;
        3)
            echo -e "${YELLOW}Could not read cloud-init's status; continuing and waiting for k3s itself.${NC}"
            ;;
        *)
            echo -e "${RED}The first boot did not finish in time.${NC}"
            sw_step_fail node-ready "the first boot did not finish"
            report_node_stuck "the first boot" \
                "It may simply be slow — a plain Ubuntu boot installs k3s and pulls several GB of images. Raise the limit with SW_CLOUD_INIT_TIMEOUT=3600 if the boot log shows it still working."
            exit 1
            ;;
    esac
    sw_step_ok node-ready

    sw_step secrets "Planting the cluster secrets on the node"
    echo -e "${CYAN}Deploying secrets to the server securely...${NC}"
    secrets_error=""
    if ! secrets_error="$(ssh_node "mkdir -p /var/lib/rancher/k3s/server/manifests" 2>&1)" \
       || ! secrets_error="$(scp "${SW_SSH_OPTS[@]}" "$SECRETS_FILE" \
              "root@$SERVER_IP:/var/lib/rancher/k3s/server/manifests/smallworlds-secrets.yaml" 2>&1)"; then
        rm -f "$SECRETS_FILE"
        echo -e "${RED}Could not copy the cluster secrets to the node: ${secrets_error}${NC}"
        sw_step_fail secrets "the secrets could not be copied to the node"
        sw_guidance "Nothing was lost — the secrets are regenerated" \
            "They are held in .smallworlds-cache.env on this machine and rebuilt on every run, so re-running is enough. What must not happen is the cluster starting without them, which is why this stops here."
        exit 1
    fi
    # Copied is not the same as present: this is the exact file the first boot
    # deletes if it is planted too early, so it is read back rather than
    # assumed. A green tick for a step that silently did nothing is worse than
    # no tick at all.
    if ! ssh_node "test -s /var/lib/rancher/k3s/server/manifests/smallworlds-secrets.yaml"; then
        rm -f "$SECRETS_FILE"
        echo -e "${RED}The secrets file is not on the node after copying it.${NC}"
        sw_step_fail secrets "the secrets did not survive on the node"
        sw_guidance "The first boot removed them" \
            "cloud-init clears /var/lib/rancher/k3s/server while it relocates k3s onto the data volume. Re-run the installer: it waits for that to finish before copying, so a second run plants them safely."
        exit 1
    fi
    rm -f "$SECRETS_FILE"
    echo -e "${GREEN}Cluster secrets planted.${NC}"
    sw_step_ok secrets

    sw_step kubeconfig "Waiting for k3s and retrieving the kubeconfig"
    echo -e "${CYAN}Waiting for k3s to generate its kubeconfig...${NC}"
    if ! sw_wait_for "${SW_K3S_TIMEOUT:-900}" "k3s" \
            ssh_node "[ -f /etc/rancher/k3s/k3s.yaml ]"; then
        echo -e "${RED}k3s did not come up on the node.${NC}"
        sw_step_fail kubeconfig "k3s did not start"
        report_node_stuck "k3s" "The node is up and reachable over SSH; k3s itself never wrote its kubeconfig."
        exit 1
    fi

    KUBECONFIG_LOCAL="$(kubeconfig_path "$(cluster_label "$ENV_EXT")")"
    if ! ssh_node "cat /etc/rancher/k3s/k3s.yaml" > "$KUBECONFIG_LOCAL" 2>/dev/null \
       || [[ ! -s "$KUBECONFIG_LOCAL" ]]; then
        rm -f "$KUBECONFIG_LOCAL"
        echo -e "${RED}Could not read the kubeconfig from the node.${NC}"
        sw_step_fail kubeconfig "the kubeconfig could not be retrieved"
        report_node_stuck "the kubeconfig" "k3s wrote its kubeconfig but it could not be copied back."
        exit 1
    fi
    sed -i "s|127.0.0.1|$SERVER_IP|g" "$KUBECONFIG_LOCAL"
    chmod 600 "$KUBECONFIG_LOCAL"
    echo -e "${GREEN}Cluster access saved to $KUBECONFIG_LOCAL.${NC}"
    sw_step_ok kubeconfig

    echo -e "${CYAN}Retrieving Argo CD's initial admin password...${NC}"
    if sw_wait_for "${SW_ARGOCD_TIMEOUT:-900}" "Argo CD" \
            ssh_node "kubectl -n argocd get secret argocd-initial-admin-secret"; then
        ARGOCD_PASS=$(ssh_node "kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath=\"{.data.password}\" | base64 -d" 2>/dev/null)
    else
        # Not fatal: the cluster is up, Argo CD is simply still starting, and
        # the password can be read out later. Handing over a blank one without
        # saying so would be worse.
        echo -e "${YELLOW}Argo CD has not created its admin secret yet.${NC}"
        ARGOCD_PASS="(not ready yet — read it later with: kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"
    fi
else
    # ==================================================================
    # Target: local server — an existing Linux machine in your LAN is
    # bootstrapped in place by infrastructure/local/bootstrap-local-node.sh
    # (the shell counterpart of the cloud-init template). No Terraform,
    # no public DNS, self-signed certificates.
    # ==================================================================
    REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"

    if [[ "$LOCAL_DNS" == "manual" ]]; then
        # The one thing the operator had to do by hand, checked before any
        # machine is touched. Let's Encrypt will ask this very name for a file
        # on port 80; if it points elsewhere every certificate fails, and
        # failed attempts count against a weekly limit.
        sw_step dns-zone "Checking that your DNS record points at the server"
        CHECK_HOST="identity${ENV_EXT}.${DOMAIN}"
        if [[ "$LOCAL_SSH_TARGET" == "localhost" ]]; then
            NODE_PUBLIC_IP=$(curl -4 -sf --max-time 10 https://api.ipify.org || true)
        else
            NODE_PUBLIC_IP=$(ssh $SSH_OPTS "$LOCAL_SSH_TARGET" \
                "curl -4 -sf --max-time 10 https://api.ipify.org" 2>/dev/null || true)
        fi
        RESOLVED_IP=$(python3 -c 'import socket, sys
try:
    print(socket.gethostbyname(sys.argv[1]))
except OSError:
    pass' "$CHECK_HOST")
        echo -e "  server's public address: ${CYAN}${NODE_PUBLIC_IP:-unknown}${NC}"
        echo -e "  ${CHECK_HOST} resolves to: ${CYAN}${RESOLVED_IP:-nothing}${NC}"
        if [[ "${SW_SKIP_DNS_CHECK:-}" == "1" ]]; then
            echo -e "${YELLOW}SW_SKIP_DNS_CHECK=1 — not comparing them.${NC}"
        elif [[ -z "$NODE_PUBLIC_IP" ]]; then
            echo -e "${YELLOW}Could not ask the server for its public address; not comparing.${NC}"
        elif [[ "$RESOLVED_IP" != "$NODE_PUBLIC_IP" ]]; then
            sw_step_fail dns-zone "${CHECK_HOST} does not point at the server"
            echo -e "${RED}${CHECK_HOST} does not point at ${NODE_PUBLIC_IP}.${NC}" >&2
            sw_guidance "Create the DNS record" \
                "At your DNS provider, add an A record named *${ENV_EXT} (a wildcard) for ${DOMAIN} with the value ${NODE_PUBLIC_IP}, wait until '${CHECK_HOST}' resolves to that address, then run the install again. A new record can take from a minute to an hour to be visible."
            exit 1
        fi
        sw_step_ok dns-zone
    fi

    command -v git >/dev/null 2>&1 || { echo -e "${RED}git is required to resolve the overlay commit.${NC}"; exit 1; }

    # The node is pointed at one exact overlay commit, not at a branch.
    ROOT_APP_GIT_REVISION="$(resolve_overlay_head "$GITOPS_REPO_URL")"
    if [[ -z "$ROOT_APP_GIT_REVISION" ]]; then
        echo -e "${RED}Could not resolve HEAD of ${GITOPS_REPO_URL}.${NC}"
        echo -e "${YELLOW}The overlay repository must exist and already carry its first commit before a${NC}"
        echo -e "${YELLOW}node can be pointed at it. Run ./admin-tools/prepare-community-repo.sh first,${NC}"
        echo -e "${YELLOW}and check the URL, username and access token you entered above.${NC}"
        exit 1
    fi
    echo -e "${GREEN}Overlay pinned to commit ${ROOT_APP_GIT_REVISION}.${NC}"

    build_bootstrap_assets

    # Identifies this attempt in the durable checkpoints the bootstrap leaves on
    # the node, so an install interrupted by a dropped SSH session resumes at a
    # safe boundary instead of repeating creation steps.
    BOOTSTRAP_RUN_ID="run-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 4)"
    RUN_DIR="/tmp/smallworlds-bootstrap-${BOOTSTRAP_RUN_ID}"

    # Two independent decisions that both need the Hetzner DNS API:
    #
    #   ACME_EMAIL non-empty -> the bootstrap creates the ACME ClusterIssuer and
    #     certificates come from Let's Encrypt over DNS-01. This works on a
    #     LAN-only cluster: the challenge is a TXT record, never an inbound
    #     connection. Empty -> self-signed issuer, which breaks OIDC between the
    #     apps, so it is opt-in rather than the LAN default.
    #   MANAGE_DNS -> the in-cluster DDNS CronJob maintains the A records, which
    #     only matters when the apps should be reachable from outside.
    #
    # With DNS kept by hand (docs/adr/0056) neither applies: the operator's
    # wildcard record is the A records, and the challenge is HTTP-01.
    LOCAL_ACME_EMAIL=""
    ACME_CHALLENGE="dns01"
    MANAGE_DNS=""
    if [[ "$LOCAL_TLS" == "letsencrypt" ]]; then
        LOCAL_ACME_EMAIL="$ADMIN_EMAIL"
    fi
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        MANAGE_DNS="true"
    fi
    if [[ "$LOCAL_DNS" == "manual" ]]; then
        ACME_CHALLENGE="http01"
        MANAGE_DNS=""
    fi

    # Either managed path writes records into the zone, so it has to exist
    # first. With DNS kept by hand there is no zone of ours to create; the
    # record was checked at the very start of this branch.
    if [[ "$LOCAL_DNS" != "manual" && ( -n "$LOCAL_ACME_EMAIL" || -n "$MANAGE_DNS" ) ]]; then
        ensure_dns_zone
    fi

    if [[ "$LOCAL_PUBLIC" == "yes" && "$LOCAL_DNS" != "manual" ]]; then
        PUBLIC_IP=$(curl -4 -sf --max-time 10 https://api.ipify.org || true)
        echo -e "${CYAN}Detected public IP: ${PUBLIC_IP:-unknown}${NC}"
        if [[ "$LOCAL_FIREWALL" == "yes" ]]; then
            echo -e "${CYAN}The machine is directly on the internet; the bootstrap sets up its firewall.${NC}"
        else
            echo -e "${YELLOW}Reminder: forward 80/tcp, 443/tcp and 10000/udp on your router to the server,${NC}"
            echo -e "${YELLOW}or the apps will not be reachable from the internet.${NC}"
            echo -e "${YELLOW}(Certificates do not depend on this — DNS-01 needs no inbound connection.)${NC}"
        fi

        # The DDNS CronJob (created by the bootstrap) reads the token from
        # this secret; the A records appear within its first run (~5 min).
        cat <<EOF >> "$SECRETS_FILE"
---
apiVersion: v1
kind: Namespace
metadata:
  name: ddns
---
apiVersion: v1
kind: Secret
metadata:
  name: hetzner-dns-token
  namespace: ddns
type: Opaque
stringData:
  HCLOUD_TOKEN: "${HCLOUD_TOKEN}"
EOF
    fi

    # The host firewall's allow-list, for a machine directly on the internet.
    # It mirrors the Hetzner target's cloud firewall (infrastructure/terraform/
    # main.tf) so the two targets expose the same things: web, the Kubernetes
    # API the kubeconfig points at, and what the selected apps listen on. SSH
    # is added by the bootstrap itself, from the port sshd actually uses.
    HOST_FIREWALL=""
    FIREWALL_TCP_PORTS="80 443 6443"
    FIREWALL_UDP_PORTS=""
    if [[ "$LOCAL_FIREWALL" == "yes" ]]; then
        HOST_FIREWALL="true"
        if selected_apps | grep -qx stalwart; then
            FIREWALL_TCP_PORTS="$FIREWALL_TCP_PORTS 25 587 993"
        fi
        if selected_apps | grep -qx jitsi; then
            FIREWALL_UDP_PORTS="10000"
        fi
    fi

    # Config consumed by the bootstrap script on the target machine.
    LOCAL_ENV_FILE="/tmp/smallworlds-${DOMAIN}-local.env"
    cat <<EOF > "$LOCAL_ENV_FILE"
DOMAIN="${DOMAIN}"
ENV_EXT="${ENV_EXT}"
ROOT_APP_GIT_URL="${GITOPS_REPO_URL}"
ROOT_APP_GIT_REVISION="${ROOT_APP_GIT_REVISION}"
ACME_EMAIL="${LOCAL_ACME_EMAIL}"
ACME_CHALLENGE="${ACME_CHALLENGE}"
MANAGE_DNS="${MANAGE_DNS}"
DATA_DIR="${DATA_DIR}"
BACKUP_DIR="${BACKUP_DIR}"
OBJECT_STORAGE="${OBJECT_STORAGE}"
LIBRARY_DIR="${LIBRARY_DIR}"
HOST_FIREWALL="${HOST_FIREWALL}"
FIREWALL_TCP_PORTS="${FIREWALL_TCP_PORTS}"
FIREWALL_UDP_PORTS="${FIREWALL_UDP_PORTS}"
NODE_NAME="smallworlds-local-node"
PROFILE_ID="${PROFILE_ID}"
BOOTSTRAP_RUN_ID="${BOOTSTRAP_RUN_ID}"
SECRETS_MANIFEST="/tmp/smallworlds-${DOMAIN}-secrets.yaml"
EOF
    chmod 600 "$LOCAL_ENV_FILE"

    # The bootstrap script travels inside the asset payload, never on its own:
    # the runner in the archive is what points it at the pinned k3s installer
    # and Argo CD manifest sitting next to it, and a script separated from its
    # assets simply refuses to run.
    REMOTE_KUBECONFIG="/tmp/smallworlds-kubeconfig.yaml"
    sw_step bootstrap "Bootstrapping k3s and Argo CD on the node"
    if [[ "$LOCAL_SSH_TARGET" == "localhost" ]]; then
        echo -e "${CYAN}Bootstrapping THIS machine (sudo password may be requested)...${NC}"
        rm -rf "$RUN_DIR" && mkdir -p "$RUN_DIR"
        tar xzf "$ASSET_ARCHIVE" -C "$RUN_DIR"
        sudo bash "$RUN_DIR/run-local-node-bootstrap.sh" "$LOCAL_ENV_FILE"
        rm -rf "$RUN_DIR"
        KUBECONFIG_FETCH() { cp "$REMOTE_KUBECONFIG" "$1" && rm -f "$REMOTE_KUBECONFIG"; }
    else
        # root logs in directly; any other user gets a sudo prefix
        SUDO_PREFIX="sudo "
        if [[ "$LOCAL_SSH_TARGET" == root@* ]]; then SUDO_PREFIX=""; fi

        echo -e "${CYAN}Copying the bootstrap payload to ${LOCAL_SSH_TARGET}...${NC}"
        scp $SSH_OPTS "$ASSET_ARCHIVE" "$LOCAL_SSH_TARGET:${RUN_DIR}.tar.gz" >/dev/null
        scp $SSH_OPTS "$LOCAL_ENV_FILE" "$LOCAL_SSH_TARGET:$LOCAL_ENV_FILE" >/dev/null
        scp $SSH_OPTS "$SECRETS_FILE" "$LOCAL_SSH_TARGET:$SECRETS_FILE" >/dev/null

        echo -e "${CYAN}Bootstrapping ${LOCAL_SSH_TARGET} (sudo password may be requested)...${NC}"
        # -t: allocate a TTY so sudo can prompt for a password if needed.
        # The payload is removed whether the bootstrap succeeded or not, and its
        # exit status is what this script sees.
        ssh -t $SSH_OPTS "$LOCAL_SSH_TARGET" \
            "rm -rf $RUN_DIR && mkdir -p $RUN_DIR && tar xzf ${RUN_DIR}.tar.gz -C $RUN_DIR || exit 1; \
             ${SUDO_PREFIX}bash $RUN_DIR/run-local-node-bootstrap.sh $LOCAL_ENV_FILE; \
             status=\$?; rm -rf $RUN_DIR ${RUN_DIR}.tar.gz; exit \$status"
        KUBECONFIG_FETCH() {
            scp $SSH_OPTS "$LOCAL_SSH_TARGET:$REMOTE_KUBECONFIG" "$1" >/dev/null
            ssh $SSH_OPTS "$LOCAL_SSH_TARGET" "rm -f $REMOTE_KUBECONFIG" 2>/dev/null || true
        }
    fi
    rm -f "$SECRETS_FILE" "$LOCAL_ENV_FILE"
    sw_step_ok bootstrap

    sw_step kubeconfig "Retrieving the kubeconfig from the node"
    # Local clusters get their own kubeconfig label so they never clobber a
    # real production/dev kubeconfig from a Hetzner deployment.
    KUBECONFIG_LABEL="local"
    [[ -n "$ENV_EXT" ]] && KUBECONFIG_LABEL="local-${ENV_EXT#.}"
    KUBECONFIG_LOCAL="$(kubeconfig_path "$KUBECONFIG_LABEL")"
    KUBECONFIG_FETCH "$KUBECONFIG_LOCAL"
    chmod 600 "$KUBECONFIG_LOCAL"
    sw_step_ok kubeconfig

    # The bootstrap rewrote 127.0.0.1 to the node's LAN IP — read it back
    SERVER_IP=$(sed -n 's|.*server: https://\([^:]*\):6443.*|\1|p' "$KUBECONFIG_LOCAL" | head -1)

    echo -e "${CYAN}Retrieving ArgoCD initial admin password...${NC}"
    ARGOCD_PASS="(retrieve with: kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"
    if command -v kubectl >/dev/null 2>&1; then
        for i in $(seq 1 60); do
            if KUBECONFIG="$KUBECONFIG_LOCAL" kubectl -n argocd get secret argocd-initial-admin-secret >/dev/null 2>&1; then
                ARGOCD_PASS=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)
                break
            fi
            sleep 2
        done
    fi

    # LAN name resolution: nothing manages DNS for a local deployment, so
    # hand the operator a ready-made hosts block for their router/Pi-hole
    # or the /etc/hosts of each device.
    mkdir -p "$HOME/.smallworlds"
    HOSTS_SNIPPET="$HOME/.smallworlds/hosts-${KUBECONFIG_LABEL}.txt"
    {
        printf '%s' "$SERVER_IP"
        for sub in identity dashboard files photos git mail webmail monitoring whiteboard meet office plan pod; do
            printf ' %s%s.%s' "$sub" "$ENV_EXT" "$DOMAIN"
        done
        printf '\n'
    } > "$HOSTS_SNIPPET"
fi

echo ""
echo -e "${GREEN}======================================================${NC}"
echo -e "${GREEN}             Deployment Successful!                   ${NC}"
echo -e "${GREEN}======================================================${NC}"
echo ""
if [[ "$DEPLOY_TARGET" == "hetzner" ]]; then
    echo -e "Your applications will take a few minutes to boot up and fetch their SSL certificates."
elif [[ "$LOCAL_PUBLIC" == "yes" && "$LOCAL_FIREWALL" == "yes" ]]; then
    if [[ "$LOCAL_DNS" == "manual" ]]; then
        echo -e "Your applications will take a few minutes to boot up. Let's Encrypt certificates are"
        echo -e "issued one by one as each application starts — expect certificate warnings until then."
        echo ""
        echo -e "${YELLOW}You keep the DNS records yourself:${NC} the wildcard ${CYAN}*${ENV_EXT}.${DOMAIN}${NC} covers every"
        echo -e "application. If the server's address ever changes, change that record."
        if selected_apps | grep -qx stalwart; then
            echo -e "${YELLOW}Mail records (MX, SPF, DKIM, DMARC) were NOT created${NC} — see doc/mail.md for"
            echo -e "what to enter at your DNS provider; mail will not be delivered without them."
        fi
    else
        echo -e "Your applications will take a few minutes to boot up. An in-cluster job creates the"
        echo -e "public DNS records on its first run (within ~5 minutes); Let's Encrypt certificates"
        echo -e "are issued shortly after — expect certificate warnings until then."
    fi
    echo ""
    echo -e "${YELLOW}Notes for a rented server:${NC}"
    echo -e " - The host firewall admits SSH, 80/tcp, 443/tcp, 6443/tcp and what the selected"
    echo -e "   apps listen on; everything else arriving from the internet is dropped."
    echo -e "   Check it on the server with ${CYAN}smallworlds-firewall status${NC}."
    echo -e " - SSH and 6443 are still open to the internet. Once the node is on your tailnet"
    echo -e "   (${CYAN}admin-tools/setup-vpn.sh${NC}), close them with ${CYAN}smallworlds-firewall close${NC}."
    if selected_apps | grep -qx stalwart; then
        echo -e " - Mail: set the reverse DNS (PTR) of ${CYAN}${SERVER_IP}${NC} to ${CYAN}mail${ENV_EXT}.${DOMAIN}${NC}"
        echo -e "   in your provider's control panel — the installer cannot do that outside"
        echo -e "   Hetzner, and most receivers reject mail from an address without it."
    fi
elif [[ "$LOCAL_PUBLIC" == "yes" ]]; then
    echo -e "Your applications will take a few minutes to boot up. The in-cluster DDNS job creates"
    echo -e "the public DNS records on its first run (within ~5 minutes); Let's Encrypt certificates"
    echo -e "are issued shortly after — expect certificate warnings until then."
    echo ""
    echo -e "${YELLOW}Notes for internet-exposed local deployments:${NC}"
    echo -e " - Router forwards required: ${CYAN}80/tcp, 443/tcp, 10000/udp${NC} -> ${CYAN}${SERVER_IP}${NC}."
    echo -e " - If devices INSIDE your LAN cannot reach the apps, your router does not"
    echo -e "   support hairpin NAT — add this line to your router DNS/Pi-hole//etc/hosts"
    echo -e "   (saved to ${CYAN}${HOSTS_SNIPPET}${NC}):"
    echo ""
    cat "$HOSTS_SNIPPET"
    echo ""
    echo -e " - Stalwart mail deploys and its DNS records are automated, but real mail"
    echo -e "   delivery from a home connection is unreliable (ISPs block port 25, no PTR"
    echo -e "   record, home-IP blocklists) — consider an SMTP relay for outbound mail."
else
    echo -e "Your applications will take a few minutes to boot up (certificates are self-signed on a local deployment)."
    echo ""
    echo -e "${YELLOW}LAN name resolution — required before the URLs below work:${NC}"
    echo -e "Nothing manages DNS for a LAN-only deployment. Point the app hostnames at your"
    echo -e "server (${CYAN}${SERVER_IP}${NC}) in your router's DNS / Pi-hole, or add this line to the"
    echo -e "/etc/hosts of every device that should access the cloud:"
    echo ""
    cat "$HOSTS_SNIPPET"
    echo ""
    echo -e "(The line above is saved to ${CYAN}${HOSTS_SNIPPET}${NC}. To apply it on this machine:"
    echo -e " ${YELLOW}sudo tee -a /etc/hosts < ${HOSTS_SNIPPET}${NC})"
    echo ""
    echo -e "${YELLOW}Notes for local deployments:${NC}"
    echo -e " - Browsers will warn about the self-signed certificates — this is expected."
    echo -e " - Stalwart mail deploys, but external mail delivery needs a public IP, PTR"
    echo -e "   record and open port 25 — it will not work from behind a home NAT."
fi
echo ""
# The same facts as the block below, in a form something else can present.
# Silent unless markers were asked for — see sw_result in admin-tools/lib/ask.sh.
sw_result kubeconfig "kubeconfig" "$KUBECONFIG_LOCAL"
sw_result credential "Keycloak Operator (operator)" "$KC_OPERATOR_PASS"
sw_result note "Keycloak Operator (operator)" "your login; sets up a TOTP authenticator at first sign-in"
sw_result credential "Keycloak Admin (admin)" "$KC_PASS"
sw_result note "Keycloak Admin (admin)" "automation and break-glass; reachable only from the Private Network"
sw_result credential "Argo CD Admin (admin)" "$ARGOCD_PASS"
sw_result credential "Grafana Admin (admin)" "$GRAFANA_PASS"
sw_result credential "Bulk Invite Secret" "$INVITE_SECRET"

echo -e "Kubernetes Access (kubeconfig):"
echo -e "  Saved to:                  ${CYAN}${KUBECONFIG_LOCAL}${NC}"
echo -e "  To use with kubectl:       ${YELLOW}export KUBECONFIG=${KUBECONFIG_LOCAL}${NC}"
echo -e "                             (or link it: ln -sf ${KUBECONFIG_LOCAL} ~/.kube/config)"
echo ""
echo -e "Here are your auto-generated admin credentials. Save them somewhere safe!"
echo -e "Keycloak Admin (admin):      ${CYAN}${KC_PASS}${NC}"
echo -e "  ^ automation + break-glass; reachable only from the Private Network"
echo -e "Keycloak Operator (operator):${CYAN}${KC_OPERATOR_PASS}${NC}"
echo -e "  ^ your login; sets up a TOTP authenticator at first sign-in"
echo -e "ArgoCD Admin (admin):        ${CYAN}${ARGOCD_PASS}${NC}"
echo -e "Grafana Admin (admin):       ${CYAN}${GRAFANA_PASS}${NC}"
echo -e "Bulk Invite Secret:          ${CYAN}${INVITE_SECRET}${NC}"
echo ""
echo -e "Note: Passwords for optional apps (Nextcloud, Immich, Forgejo) and Stalwart are automatically"
echo -e "generated securely upon installation. You can retrieve them via kubectl later."
echo ""
# Only what this community selected. Listing an address for an application
# that was never installed is a link to a certificate error, and the same
# selection decides which A records exist (var.apps in
# infrastructure/terraform/main.tf).
declare -A _APP_ADDRESS=(
    [nextcloud]="files|Files|Files (Nextcloud)"
    [immich]="photos|Photos|Photos (Immich)"
    [forgejo]="git|Git|Git (Forgejo)"
    [excalidraw]="whiteboard|Whiteboard|Whiteboard"
    [jitsi]="meet|Meet|Meet (Jitsi)"
    [collabora]="office|Office|Office (Collabora)"
    [euro-office]="office|Office|Office (Euro-Office)"
    [plane]="plan|Plan|Plan"
    [stalwart]="mail|Mail|Mail (Stalwart)"
    [bulwark]="webmail|Webmail|Webmail (Bulwark)"
    [pod-gateway]="pod|Personal data pods|Personal data pods"
)
_ADDRESSES=("dashboard|Dashboard|Dashboard" "identity|Identity|Identity"
            "monitoring|Monitoring|Monitoring")
while read -r _app; do
    if [[ -n "$_app" && -n "${_APP_ADDRESS[$_app]:-}" ]]; then
        _ADDRESSES+=("${_APP_ADDRESS[$_app]}")
    fi
done < <(selected_apps)

echo -e "Application URLs:"
for _entry in "${_ADDRESSES[@]}"; do
    IFS='|' read -r _sub _name _display <<<"$_entry"
    printf "  %-22s ${CYAN}https://%s${ENV_EXT}.${DOMAIN}${NC}\n" "${_display}:" "$_sub"
done
echo ""
for _entry in "${_ADDRESSES[@]}"; do
    IFS='|' read -r _sub _name _display <<<"$_entry"
    sw_result url "$_name" "https://${_sub}${ENV_EXT}.${DOMAIN}"
done
if [[ "$DEPLOY_TARGET" == "local" && -f "${HOSTS_SNIPPET:-}" ]]; then
    sw_result hosts "hosts" "$(cat "$HOSTS_SNIPPET")"
fi

echo -e "ArgoCD Dashboard:            ${CYAN}https://localhost:8080${NC} (requires port-forward)"
echo -e "  To port-forward:           ${YELLOW}kubectl port-forward svc/argocd-server -n argocd 8080:443${NC}"
echo -e "${GREEN}======================================================${NC}"

# Watch the ArgoCD rollout until every application is Synced+Healthy.
# ArgoCD parks a sync permanently once its retries are exhausted, so any app
# whose operation ended in Failed/Error gets re-kicked automatically.
if command -v kubectl >/dev/null 2>&1; then
    sw_step argocd-converge "Waiting for every Argo CD application to go Healthy"
    CONVERGE_ATTEMPTS="${SW_CONVERGE_ATTEMPTS:-120}"
    CONVERGE_INTERVAL="${SW_CONVERGE_INTERVAL:-20}"
    echo -e "${CYAN}Watching application rollout (up to $(( CONVERGE_ATTEMPTS * CONVERGE_INTERVAL / 60 )) minutes; safe to Ctrl+C — the cluster continues on its own)...${NC}"
    export KUBECONFIG="$KUBECONFIG_LOCAL"
    CONVERGED=false
    for i in $(seq 1 "$CONVERGE_ATTEMPTS"); do
        for app in $(kubectl get application -n argocd -o jsonpath='{range .items[?(@.status.operationState.phase=="Failed")]}{.metadata.name}{" "}{end}' 2>/dev/null) \
                   $(kubectl get application -n argocd -o jsonpath='{range .items[?(@.status.operationState.phase=="Error")]}{.metadata.name}{" "}{end}' 2>/dev/null); do
            echo -e "  ${YELLOW}Sync of '$app' gave up — retriggering...${NC}"
            # A manually patched operation does NOT inherit the app's
            # spec.syncPolicy.syncOptions — a bare {"sync":{}} drops
            # SkipDryRunOnMissingResource and the sync then aborts on any
            # CR whose CRD isn't installed yet (e.g. AlertmanagerConfig
            # before kube-prometheus-stack). Copy the options explicitly.
            APP_SYNC_OPTS=$(kubectl get application "$app" -n argocd -o json 2>/dev/null \
                | python3 -c 'import json,sys; print(json.dumps((json.load(sys.stdin)["spec"].get("syncPolicy") or {}).get("syncOptions") or []))' 2>/dev/null)
            [ -z "$APP_SYNC_OPTS" ] && APP_SYNC_OPTS='[]'
            kubectl patch application "$app" -n argocd --type merge \
                -p '{"operation":{"initiatedBy":{"username":"installer-watchdog"},"sync":{"syncOptions":'"$APP_SYNC_OPTS"'}}}' >/dev/null 2>&1 || true
        done

        # Get all ArgoCD apps that are not healthy
        UNHEALTHY=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get apps -n argocd -o jsonpath='{.items[?(@.status.health.status!="Healthy")].metadata.name}' 2>/dev/null)
        HEALTHY=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get apps -n argocd -o jsonpath='{.items[?(@.status.health.status=="Healthy")].metadata.name}' 2>/dev/null | wc -w)
        TOTAL=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get apps -n argocd -o jsonpath='{.items[*].metadata.name}' 2>/dev/null | wc -w)
        ROOT_STATE=$(KUBECONFIG="$KUBECONFIG_LOCAL" kubectl get app smallworlds-root -n argocd -o jsonpath='{.status.sync.status}/{.status.health.status}' 2>/dev/null)
        if [ "$TOTAL" -gt 1 ] && [ "$HEALTHY" -eq "$TOTAL" ] && [ "$ROOT_STATE" = "Synced/Healthy" ]; then
            echo -e "${GREEN}All $TOTAL applications are Healthy!${NC}"
            sw_step_ok argocd-converge
            CONVERGED=true
            break
        fi
        echo -e "  [$i/$CONVERGE_ATTEMPTS] $HEALTHY/$TOTAL apps healthy (root: ${ROOT_STATE:-pending})"
        sleep "$CONVERGE_INTERVAL"
    done
    if [ "$CONVERGED" = false ]; then
        sw_step_fail argocd-converge "not all applications were Healthy in time"
        echo -e "${YELLOW}Rollout did not fully converge. Inspect with:${NC}"
        echo -e "  kubectl get application -n argocd"
        sw_guidance "The cluster is up and Argo CD keeps trying" \
            "This is not a failed install: Argo CD reconciles on its own and slow first syncs are normal while images are pulled. Watch it with: KUBECONFIG=${KUBECONFIG_LOCAL} kubectl get application -n argocd"
    fi
else
    echo -e "${YELLOW}kubectl not found locally — skipping rollout watch. Check progress with the ArgoCD dashboard.${NC}"
fi

# Open the dashboard
DASHBOARD_URL="https://dashboard${ENV_EXT}.${DOMAIN}"
echo ""
echo -e "${YELLOW}Please note: The infrastructure is currently being provisioned in the background.${NC}"
echo -e "${YELLOW}It may take 5-10 minutes for all services to come online and for SSL certificates to be issued.${NC}"
echo -e "${YELLOW}If you see a 'Bad Gateway' or SSL warning, simply wait a few minutes and refresh.${NC}"
if [[ "$DEPLOY_TARGET" == "local" ]]; then
    if [[ "$LOCAL_PUBLIC" == "yes" ]]; then
        echo -e "${YELLOW}Remember: the dashboard only resolves once the DDNS job has created the DNS records (~5 min).${NC}"
    else
        echo -e "${YELLOW}Remember: the dashboard only resolves once the hosts entries above are in place.${NC}"
    fi
fi
echo ""
# Opening a browser is right for someone at a terminal and wrong for the setup
# wizard, whose operator is already looking at one — it hands over to the
# dashboard itself.
if sw_noninteractive; then
    echo -e "${CYAN}Your dashboard: ${DASHBOARD_URL}${NC}"
else
    echo -e "${CYAN}Opening your dashboard: ${DASHBOARD_URL}${NC}"
    python3 -c "import webbrowser; webbrowser.open('${DASHBOARD_URL}')" >/dev/null 2>&1 || echo -e "${CYAN}Please manually navigate to: ${DASHBOARD_URL}${NC}"
fi
