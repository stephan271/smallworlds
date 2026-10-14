"""Settings, all from the environment (join-config ConfigMap + keycloak-secret)."""
import os

# Keycloak as reached from inside the cluster — the Service, never Traefik, so
# the Private Network allow-list on /admin does not apply (design §6).
KEYCLOAK_URL = os.getenv("KEYCLOAK_URL", "http://keycloak-keycloakx-http.keycloak").rstrip("/")
REALM = os.getenv("KEYCLOAK_REALM", "smallworlds")
# The issuer every minted link must carry. A link built on any other base would
# fail only when the member clicks it, so it is refused before it is handed out.
PUBLIC_IDENTITY_URL = os.getenv("PUBLIC_IDENTITY_URL", "https://identity.smallworlds.network").rstrip("/")

# The `join` client from bases/keycloak-client-job. Its service account creates
# accounts and mints links; Keycloak returns members to its registered redirect.
CLIENT_ID = os.getenv("KEYCLOAK_CLIENT_ID", "join")
CLIENT_SECRET = os.getenv("KEYCLOAK_CLIENT_SECRET", "")

JOIN_URL = os.getenv("JOIN_URL", "https://join.smallworlds.network").rstrip("/")
DASHBOARD_URL = os.getenv("DASHBOARD_URL", "https://dashboard.smallworlds.network").rstrip("/")

DB_PATH = os.getenv("DB_PATH", "/data/invitations.db")
STATIC_DIR = os.getenv("STATIC_DIR", "/static")
PUBLIC_PORT = int(os.getenv("PUBLIC_PORT", "8080"))
# The Invitation Desk's own port. The public Ingress never names it, so a broken
# private route fails closed instead of exposing /desk (design §9).
DESK_PORT = int(os.getenv("DESK_PORT", "8081"))
BIND = os.getenv("BIND", "0.0.0.0")

LOCALES = ("de", "en")
DEFAULT_DAYS = 14
MAX_DAYS = 30

# Failed code lookups allowed per source address and hour. The 50-bit code makes
# guessing pointless already; this keeps scanners from filling the log.
FAILED_LOOKUPS_PER_HOUR = int(os.getenv("FAILED_LOOKUPS_PER_HOUR", "20"))
# Username checks allowed per invitation and ten minutes (debounced typing).
USERNAME_CHECKS_PER_10_MIN = int(os.getenv("USERNAME_CHECKS_PER_10_MIN", "120"))

# Run in this order by Keycloak's priorities, not this list's. No UPDATE_PROFILE:
# the join page already collected everything the realm's profile requires.
# Provider ids, not display names — see the note in admin-tools/bulk-invite.py.
REQUIRED_ACTIONS = ["webauthn-register-passwordless", "CONFIGURE_RECOVERY_AUTHN_CODES"]
PASSKEY_CREDENTIAL_TYPE = "webauthn-passwordless"

# The Desk signs the Operator in as the master-realm `operator` account (password
# + TOTP) through the public `join-desk` client, and accepts only a token that
# carries this master realm role.
DESK_REALM = "master"
DESK_CLIENT_ID = os.getenv("DESK_CLIENT_ID", "join-desk")
DESK_REQUIRED_ROLE = "admin"
DESK_SESSION_SECONDS = int(os.getenv("DESK_SESSION_SECONDS", str(8 * 3600)))
MAX_BULK = 100


def desk_redirect_uri() -> str:
    return f"{JOIN_URL}/desk/callback"
