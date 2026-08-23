# Shared prompt helpers for the SmallWorlds setup scripts. Source, don't execute.
#
# Every question smallworlds-init.sh and prepare-community-repo.sh ask goes
# through here, so that both can also run with no tty at all — which is what the
# browser setup wizard (admin-tools/setup-wizard/) does. See
# plans-and-walkthroughs/design-setup-wizard.md.
#
#   SMALLWORLDS_NONINTERACTIVE=1   never read from stdin; take the value that is
#                                  already known, or fail loudly naming it
#   SMALLWORLDS_MARKERS=1          emit machine-readable progress markers
#                                  (implied by SMALLWORLDS_NONINTERACTIVE)
#
# The interactive path is deliberately byte-identical to what these scripts
# printed before this library existed: same prompts, same pre-filled defaults,
# same order. admin-tools/test-noninteractive-install.sh asserts that.

# Exit status for "a required value was not supplied" — EX_CONFIG from
# sysexits.h. Distinct from 1 so a caller can tell a misconfiguration from a
# genuine failure partway through an install.
SW_EX_CONFIG=78

sw_noninteractive() {
    [[ "${SMALLWORLDS_NONINTERACTIVE:-}" == "1" ]]
}

sw_markers_enabled() {
    [[ "${SMALLWORLDS_MARKERS:-}" == "1" ]] || sw_noninteractive
}

# ---------------------------------------------------------------------------
# Progress markers
#
# Single lines on stdout that the wizard parses into a stage list. Silent
# unless asked for, so a human running the script by hand never sees them.
#
#   >>> step:<id> <text>     a stage began
#   >>> ok:<id>              a stage finished
#   >>> fail:<id> <reason>   a stage failed
#   !!! missing:<VAR>        a required value was absent  (on stderr)
#
# Stage ids are a stable interface: preflight, dns-zone, ssh-key,
# terraform-apply, secrets, bootstrap, kubeconfig, argocd-converge, and the
# repo-preparation ids repo-init, repo-apps, repo-commit, repo-push.
sw_step()      { sw_markers_enabled && printf '>>> step:%s %s\n' "$1" "${2:-}"; return 0; }
sw_step_ok()   { sw_markers_enabled && printf '>>> ok:%s\n' "$1"; return 0; }
sw_step_fail() { sw_markers_enabled && printf '>>> fail:%s %s\n' "$1" "${2:-}"; return 0; }

# What an install produced, for whatever is presenting it:
#
#   sw_result <kind> <name> <value>   ->  >>> result\t<kind>\t<name>\t<value>
#
# Kinds: credential, url, kubeconfig, hosts, note. Tab-separated because a name
# has spaces in it and a value can contain very nearly anything.
#
# These carry the generated passwords, so they are emitted only when markers
# are on -- which means only when something asked for them. A person at a
# terminal reads the human-readable block the installer prints instead, and
# never sees these at all.
sw_result() { sw_markers_enabled && printf '>>> result\t%s\t%s\t%s\n' "$1" "$2" "${3:-}"; return 0; }

# A required value was not supplied and cannot be asked for. Named, so the
# wizard can point at the field the operator left blank rather than reporting
# that "the installer failed".
sw_missing() {
    local var_name="$1"
    printf '!!! missing:%s\n' "$var_name" >&2
    printf 'Required value %s was not supplied, and this is a non-interactive run.\n' \
        "$var_name" >&2
    exit "$SW_EX_CONFIG"
}

# Report what a non-interactive run took, without ever printing a secret.
sw_report_taken() {
    local var_name="$1" is_secret="$2" value="$3"
    if [[ "$is_secret" == "true" ]]; then
        printf '  %s: [supplied]\n' "$var_name"
    elif [[ -z "$value" ]]; then
        printf '  %s: (empty)\n' "$var_name"
    else
        printf '  %s: %s\n' "$var_name" "$value"
    fi
}

# ---------------------------------------------------------------------------
# ask_with_default <prompt> <VAR> <is_secret> [required]
#
# smallworlds-init.sh's style: the variable already holds the cached value, the
# prompt shows it, blank keeps it and "-" clears it. Secrets are read silently
# and shown as [saved] rather than echoed back.
#
# Non-interactive: keep whatever the variable already holds. An empty value is
# fine unless the caller marked it required — ENV_EXT is legitimately empty
# (that is what production means), a blank DOMAIN is not.
ask_with_default() {
    local prompt_text="$1"
    local var_name="$2"
    local is_secret="$3"
    local required="${4:-optional}"

    local current_val="${!var_name}"

    if sw_noninteractive; then
        if [[ -z "$current_val" && "$required" == "required" ]]; then
            sw_missing "$var_name"
        fi
        declare -g "$var_name"="$current_val"
        sw_report_taken "$var_name" "$is_secret" "$current_val"
        return 0
    fi

    local input_val
    if [[ -n "$current_val" ]]; then
        if [[ "$is_secret" == "true" ]]; then
            read -s -p "$prompt_text [saved]: " input_val
            echo ""
        else
            read -p "$prompt_text [$current_val] (blank=keep, type - to clear): " input_val
        fi

        if [[ -z "$input_val" ]]; then
            declare -g "$var_name"="$current_val"
        elif [[ "$input_val" == "-" ]]; then
            declare -g "$var_name"=""
        else
            declare -g "$var_name"="$input_val"
        fi
    else
        if [[ "$is_secret" == "true" ]]; then
            read -s -p "$prompt_text: " input_val
            echo ""
        else
            read -p "$prompt_text: " input_val
        fi
        declare -g "$var_name"="$input_val"
    fi
}

# ---------------------------------------------------------------------------
# ask_prefilled <prompt> <VAR> <default> [required]
#
# prepare-community-repo.sh's style: `read -e -i "$default"`, an editable
# pre-filled line. The caller has already worked out the default from its
# stored preferences, so non-interactive simply takes that default — which
# keeps every "where does this value come from" decision at the call site, and
# avoids a stale value leaking from one loop iteration into the next.
ask_prefilled() {
    local prompt_text="$1"
    local var_name="$2"
    local default_val="$3"
    local required="${4:-optional}"

    if sw_noninteractive; then
        if [[ -z "$default_val" && "$required" == "required" ]]; then
            sw_missing "$var_name"
        fi
        declare -g "$var_name"="$default_val"
        sw_report_taken "$var_name" "false" "$default_val"
        return 0
    fi

    local input_val
    if [[ -n "$default_val" ]]; then
        read -e -i "$default_val" -p "$prompt_text" input_val
    else
        read -e -p "$prompt_text" input_val
    fi
    declare -g "$var_name"="$input_val"
}
