#!/usr/bin/env bash
# Checks the local bootstrap's firewall preflight: a firewall the bootstrap did
# not write, whose input chain drops by default and admits nothing from the pod
# network, must stop the install before anything is installed.
#
# The function is lifted out of infrastructure/local/bootstrap-local-node.sh and
# fed rulesets as text, so this needs neither root nor nftables.
set -uo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
local_bootstrap="$repository_root/infrastructure/local/bootstrap-local-node.sh"

failures=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; failures=$((failures + 1)); }
expect() { if [[ "$2" == "$3" ]]; then pass "$1"; else fail "$1 — got '$2'"; fi; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
sed -n '/^foreign_input_firewalls() {$/,/^}$/p' "$local_bootstrap" > "$work/function.sh"
[[ -s "$work/function.sh" ]] || { fail "foreign_input_firewalls could be extracted"; exit 1; }
# shellcheck disable=SC1091
source "$work/function.sh"

# What `nft list ruleset` printed on the server where this was first met, cut
# down: the operator's own table, the bootstrap's, and one of kube-proxy's.
ruleset() {
    cat <<RULESET
table inet eingang {
	chain eingang {
		type filter hook input priority filter; policy drop;
		ct state established,related accept
		iif "lo" accept
$1
		tcp dport { 80, 443 } accept
	}
}
table inet smallworlds {
	set admitted {
		type inet_service
		elements = { 80, 443 }
	}
	chain perimeter {
		type filter hook prerouting priority mangle; policy accept;
		counter packets 228 bytes 11950 drop
	}
	chain strict {
		type filter hook input priority filter; policy drop;
	}
}
table ip filter {
	chain INPUT {
		type filter hook input priority filter; policy accept;
		counter packets 10 bytes 600 jump KUBE-FIREWALL
	}
	chain FORWARD {
		type filter hook forward priority filter; policy drop;
	}
}
RULESET
}

echo "local bootstrap — a firewall that would cut pods off from the API"
expect "a drop-by-default input chain is named" \
    "$(ruleset "" | foreign_input_firewalls)" "inet eingang eingang"
expect "admitting the pod interfaces clears it" \
    "$(ruleset '		iifname { "cni0", "flannel.1" } accept' | foreign_input_firewalls)" ""
expect "so does admitting the pod network by address" \
    "$(ruleset '		ip saddr 10.42.0.0/16 accept' | foreign_input_firewalls)" ""
expect "the bootstrap's own table, kube-proxy's and a forward chain are not reported" \
    "$(ruleset "" | foreign_input_firewalls | grep -c -v '^inet eingang ')" "0"
expect "an empty ruleset reports nothing" "$(printf '' | foreign_input_firewalls)" ""

if grep -q 'SKIP_FIREWALL_CHECK' "$repository_root/smallworlds-init.sh"; then
    pass "the installer can pass the override through"
else
    fail "the installer can pass the override through"
fi
# It has to run before the downloads and long before k3s.
check_line="$(grep -n 'BLOCKING_CHAINS="\$(nft list ruleset' "$local_bootstrap" | cut -d: -f1)"
install_line="$(grep -n 'INSTALL_K3S_VERSION=' "$local_bootstrap" | head -1 | cut -d: -f1)"
if [[ -n "$check_line" && -n "$install_line" && "$check_line" -lt "$install_line" ]]; then
    pass "the check runs before k3s is installed"
else
    fail "the check runs before k3s is installed"
fi

echo "local bootstrap — the DNS job addresses the apex record as the API expects"
# Hetzner's API answers 404 to rrsets/%40/A and 200 to rrsets/@/A. Encoding the
# name left the old apex record in place, so the job never moved any record.
if grep -q 'rrsets/\$encoded' "$local_bootstrap"; then fail "the apex name is not percent-encoded"
else pass "the apex name is not percent-encoded"; fi
if grep -q 'rrsets/\$name/A' "$local_bootstrap"; then pass "the existing record is deleted by its literal name"
else fail "the existing record is deleted by its literal name"; fi

echo
if [[ "$failures" -eq 0 ]]; then echo "All checks passed."; else echo "$failures check(s) failed."; exit 1; fi
