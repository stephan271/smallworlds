#!/bin/bash
# Removes the devices of members whose Keycloak account has been deleted
# (docs/adr/0066, "Leaving removes the devices"). Run by the CronJob in
# member-reconciler.yaml.
#
# The member Headscale records each member as a user whose provider_id is
# `<issuer>/<Keycloak user id>`. For every such user this asks Keycloak for that
# id. Only Keycloak's own "User not found" removes anything: then the user's
# devices are deleted, and the Headscale user with them. Any other answer (a
# timeout, a 401, a missing realm, a reply that is not Keycloak's) leaves the
# user alone and makes the run fail, so an outage can never remove a member.
# A disabled account keeps its devices: only deletion counts as leaving.
#
# Tagged devices (the Member Gateway) belong to no member and are never
# touched. Neither are users from another realm.
#
# Environment:
#   KEYCLOAK_URL    Keycloak inside the cluster, e.g. http://keycloak-keycloakx-http.keycloak
#   KEYCLOAK_REALM  the members' realm (smallworlds)
#   CLIENT_ID, CLIENT_SECRET  the reconciler's service account (view-users only)
#   HEADSCALE       how to run the headscale CLI (default: kubectl exec into
#                   the member Headscale); the tests replace it
#   DRY_RUN=1       only say what would be removed
set -uo pipefail

: "${KEYCLOAK_URL:?}" "${KEYCLOAK_REALM:?}" "${CLIENT_ID:?}" "${CLIENT_SECRET:?}"
HEADSCALE="${HEADSCALE:-kubectl exec deploy/headscale -c headscale -- headscale}"

hs() { $HEADSCALE "$@"; }
log() { echo "$(date -u +%FT%TZ) $*"; }

token=$(curl -sS --max-time 20 -X POST \
          "$KEYCLOAK_URL/realms/$KEYCLOAK_REALM/protocol/openid-connect/token" \
          -d grant_type=client_credentials \
          --data-urlencode "client_id=$CLIENT_ID" \
          --data-urlencode "client_secret=$CLIENT_SECRET" | jq -r '.access_token // empty') || token=""
if [[ -z "$token" ]]; then
  log "No token from Keycloak for $CLIENT_ID; nothing removed."
  exit 1
fi

# An empty list comes out as `null` (Ring 1, 2026-11-23: every run failed until
# the first member enrolled). Anything else that is not a list still fails.
as_list() { jq -ce 'if . == null then [] elif type == "array" then . else error end' 2>/dev/null; }
if ! users=$(hs users list -o json) || ! users=$(as_list <<<"$users"); then
  log "Cannot list the member Headscale's users; nothing removed."
  exit 1
fi
if ! nodes=$(hs nodes list -o json) || ! nodes=$(as_list <<<"$nodes"); then
  log "Cannot list the member Headscale's devices; nothing removed."
  exit 1
fi

failed=0
kept=0
removed=0
# One line per OIDC user: id, name, provider_id.
while IFS=$'\t' read -r id name provider_id; do
  # The Keycloak user id is the last segment after /realms/<realm>/, and an id
  # is a UUID. Anything else came from somewhere this job does not judge.
  sub="${provider_id##*/realms/"$KEYCLOAK_REALM"/}"
  if [[ "$sub" == "$provider_id" || ! "$sub" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]; then
    log "Leaving $name alone: $provider_id is not an account of realm $KEYCLOAK_REALM."
    continue
  fi

  body=$(curl -sS --max-time 20 -w '\n%{http_code}' -H "Authorization: Bearer $token" \
           "$KEYCLOAK_URL/admin/realms/$KEYCLOAK_REALM/users/$sub") || body=$'\n000'
  code="${body##*$'\n'}"
  body="${body%$'\n'*}"

  if [[ "$code" == 200 ]]; then
    kept=$((kept + 1))
    continue
  fi
  if [[ "$code" != 404 ]] || ! jq -e '.error == "User not found"' >/dev/null 2>&1 <<<"$body"; then
    log "Keycloak answered $code for $name ($sub); leaving it alone."
    failed=1
    continue
  fi

  devices=$(jq -r --argjson user "$id" '.[] | select(.user.id == $user) | "\(.id) \(.name)"' <<<"$nodes")
  log "The account of $name ($sub) is deleted; removing its devices: ${devices:-none}."
  if [[ "${DRY_RUN:-}" == 1 ]]; then
    continue
  fi
  while read -r node_id node_name; do
    [[ -z "$node_id" ]] && continue
    if ! hs nodes delete --identifier "$node_id" --force >/dev/null; then
      log "Could not delete device $node_name ($node_id) of $name."
      failed=1
      continue 2
    fi
  done <<<"$devices"
  if ! hs users destroy --identifier "$id" --force >/dev/null; then
    log "Could not remove the Headscale user $name ($id)."
    failed=1
    continue
  fi
  removed=$((removed + 1))
done < <(jq -r '.[] | select(.provider == "oidc" and (.provider_id // "") != "")
                 | [.id, .name, .provider_id] | @tsv' <<<"$users")

log "Members kept: $kept, removed: $removed."
exit "$failed"
