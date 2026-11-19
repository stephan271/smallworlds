#!/bin/bash
# Tests for reconcile-members.sh with Keycloak (curl) and the headscale CLI
# stubbed. Run: bash infrastructure/kubernetes/tenants/member-network/tests/test-reconcile-members.sh
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/../reconcile-members.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
pass=0
fail=0

ANNA=11111111-1111-4111-8111-111111111111   # still a member
BEN=22222222-2222-4222-8222-222222222222    # deleted in Keycloak
ISSUER=https://identity.example.org/realms/smallworlds

# A fresh stub world: $WORK/bin/curl, $WORK/headscale, fixtures, and a log of
# what the stubs were asked to change.
setup() {
  rm -rf "$WORK"/*; mkdir -p "$WORK/bin" "$WORK/kc"
  : > "$WORK/changes"
  echo '{"access_token":"t0ken"}' > "$WORK/token"
  cat > "$WORK/users.json" <<EOF
[
  {"id": 1, "name": "anna", "provider": "oidc", "provider_id": "$ISSUER/$ANNA"},
  {"id": 2, "name": "ben", "provider": "oidc", "provider_id": "$ISSUER/$BEN"}
]
EOF
  cat > "$WORK/nodes.json" <<EOF
[
  {"id": 1, "name": "member-gateway", "user": {"id": 2147455555, "name": "tagged-devices"}, "tags": ["tag:member-gateway"]},
  {"id": 2, "name": "anna-phone", "user": {"id": 1, "name": "anna"}},
  {"id": 3, "name": "ben-phone", "user": {"id": 2, "name": "ben"}},
  {"id": 4, "name": "ben-laptop", "user": {"id": 2, "name": "ben"}}
]
EOF
  kc "$ANNA" 200 '{"id":"'"$ANNA"'","username":"anna"}'
  kc "$BEN" 404 '{"error":"User not found"}'

  cat > "$WORK/bin/curl" <<'EOF'
#!/bin/bash
# The token request, or GET .../admin/realms/<realm>/users/<id> with
# -w '\n%{http_code}'.
url=""
for arg in "$@"; do [[ "$arg" == http* ]] && url="$arg"; done
case "$url" in
  */protocol/openid-connect/token)
    [[ -s "$WORK/token" ]] && cat "$WORK/token" || exit 7 ;;
  */users/*)
    f="$WORK/kc/${url##*/}"
    [[ -f "$f" ]] || exit 28
    printf '%s\n%s' "$(sed -n 2p "$f")" "$(sed -n 1p "$f")" ;;
  *) exit 6 ;;
esac
EOF
  cat > "$WORK/headscale" <<'EOF'
#!/bin/bash
case "$1 $2" in
  "users list") [[ -f "$WORK/users-fail" ]] && exit 1; cat "$WORK/users.json" ;;
  "nodes list") cat "$WORK/nodes.json" ;;
  "nodes delete") echo "delete node $4" >> "$WORK/changes" ;;
  "users destroy") echo "destroy user $4" >> "$WORK/changes" ;;
  *) echo "unexpected: $*" >&2; exit 2 ;;
esac
EOF
  chmod +x "$WORK/bin/curl" "$WORK/headscale"
}
kc() { printf '%s\n%s\n' "$2" "$3" > "$WORK/kc/$1"; }

run() {
  WORK="$WORK" PATH="$WORK/bin:$PATH" HEADSCALE="$WORK/headscale" \
    KEYCLOAK_URL=http://keycloak KEYCLOAK_REALM=smallworlds CLIENT_ID=r CLIENT_SECRET=s \
    "$@" bash "$SCRIPT" > "$WORK/out" 2>&1
  echo $? > "$WORK/status"
}

check() {  # name, expected exit status, expected changes (one per line, or "")
  local name="$1" status="$2" expected="$3" got_status got
  got_status=$(cat "$WORK/status"); got=$(cat "$WORK/changes")
  if [[ "$got_status" == "$status" && "$got" == "$expected" ]]; then
    echo "PASS $name"; pass=$((pass + 1))
  else
    echo "FAIL $name: exit $got_status (want $status), changes:"; echo "${got:-<none>}" | sed 's/^/    /'
    echo "  want:"; echo "${expected:-<none>}" | sed 's/^/    /'
    sed 's/^/  | /' "$WORK/out"; fail=$((fail + 1))
  fi
}

setup; run
check "a deleted member's devices and user go; others and the gateway stay" 0 \
"delete node 3
delete node 4
destroy user 2"

setup; kc "$BEN" 200 '{"id":"x"}'; run
check "nobody deleted, nothing removed" 0 ""

setup; run env DRY_RUN=1
check "a dry run removes nothing" 0 ""
grep -q "ben-phone" "$WORK/out" && grep -q "ben-laptop" "$WORK/out" \
  && { echo "PASS the dry run names the devices"; pass=$((pass + 1)); } \
  || { echo "FAIL the dry run names the devices"; fail=$((fail + 1)); }

setup; : > "$WORK/token"; run
check "no token: nothing removed, run fails" 1 ""

setup; kc "$BEN" 401 '{"error":"HTTP 401 Unauthorized"}'; run
check "401 from the admin API: nothing removed, run fails" 1 ""

setup; kc "$BEN" 404 '{"error":"Realm not found."}'; run
check "a missing realm is not a deleted member" 1 ""

setup; kc "$BEN" 404 '<html>404 page not found</html>'; run
check "a 404 that is not Keycloak's is not a deleted member" 1 ""

setup; rm "$WORK/kc/$BEN"; run
check "Keycloak timing out removes nothing" 1 ""

setup; kc "$BEN" 500 '{"error":"unknown_error"}'; kc "$ANNA" 404 '{"error":"User not found"}'; run
check "one member's error does not stop another's removal" 1 \
"delete node 2
destroy user 1"

setup
cat > "$WORK/users.json" <<EOF
[ {"id": 2, "name": "ben", "provider": "oidc", "provider_id": "https://identity.example.org/realms/master/$BEN"},
  {"id": 3, "name": "local", "provider": "", "provider_id": ""} ]
EOF
run
check "users of another realm, or not from OIDC, are left alone" 0 ""

setup; touch "$WORK/users-fail"; run
check "Headscale not answering: nothing removed, run fails" 1 ""

echo "$pass passed, $fail failed"
[[ $fail -eq 0 ]]
