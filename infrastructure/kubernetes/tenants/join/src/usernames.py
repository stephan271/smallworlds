"""The usernames a member may choose (design §7).

The narrowest rules every Community Application accepts, so a name allowed here
never fails later: Forgejo is the tightest — no '@', no separator at either end
or twice in a row. Keycloak lower-cases usernames itself; doing it here first
means the member sees the name they will actually sign in with.
"""
import re

MIN_LENGTH = 3
MAX_LENGTH = 32
_PATTERN = re.compile(r"^[a-z0-9]+([._-][a-z0-9]+)*$")

# Stalwart's role addresses, the master realm's accounts, and names that would
# let someone pass as the Operator.
RESERVED = frozenset({
    "admin", "administrator", "operator", "root", "system", "postmaster",
    "hostmaster", "webmaster", "abuse", "noreply", "no-reply", "mailer-daemon",
    "security", "support", "info",
})


def normalize(text) -> str:
    return text.strip().lower() if isinstance(text, str) else ""


def problem(username: str) -> str | None:
    """Why `username` (already normalized) cannot be used, or None if it can.

    The reasons are message keys the page translates.
    """
    if not MIN_LENGTH <= len(username) <= MAX_LENGTH:
        return "username_length"
    if not _PATTERN.match(username):
        return "username_characters"
    if username in RESERVED:
        return "username_reserved"
    return None
