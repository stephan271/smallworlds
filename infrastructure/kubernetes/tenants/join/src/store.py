"""Member Invitations in SQLite (design §5–§6).

Only what `join` itself needs is kept here: the code's hash, the Operator's
label and note, the language, the timestamps, and — once the member has chosen
it — which Keycloak account the invitation belongs to. Names, addresses and
credentials live in Keycloak alone. The file is deliberately not backed up:
losing it loses open invitations, never a member.
"""
import sqlite3
import threading
import time
from dataclasses import dataclass

import codes
import config

_SCHEMA = """
CREATE TABLE IF NOT EXISTS invitations (
    id               INTEGER PRIMARY KEY,
    kind             TEXT    NOT NULL CHECK (kind IN ('new', 'recovery')),
    code_hash        TEXT    NOT NULL UNIQUE,
    label            TEXT    NOT NULL DEFAULT '',
    note             TEXT    NOT NULL DEFAULT '',
    locale           TEXT    NOT NULL,
    created_at       INTEGER NOT NULL,
    expires_at       INTEGER NOT NULL,
    revoked_at       INTEGER,
    claimed_at       INTEGER,
    completed_at     INTEGER,
    keycloak_user_id TEXT,
    username         TEXT
)
"""

# Serialises the read-check-write sequences below and the redeem in member.py,
# which spans a Keycloak call. One replica, so a process lock is enough.
lock = threading.RLock()


@dataclass(frozen=True)
class Invitation:
    id: int
    kind: str
    code_hash: str
    label: str
    note: str
    locale: str
    created_at: int
    expires_at: int
    revoked_at: int | None
    claimed_at: int | None
    completed_at: int | None
    keycloak_user_id: str | None
    username: str | None

    def state(self, now: float | None = None) -> str:
        """open, started, done, expired or revoked — see the diagram in design §5."""
        now = time.time() if now is None else now
        if self.revoked_at is not None:
            return "revoked"
        if self.completed_at is not None:
            return "done"
        if now >= self.expires_at:
            return "expired"
        return "started" if self.claimed_at is not None else "open"


def _connect() -> sqlite3.Connection:
    connection = sqlite3.connect(config.DB_PATH, timeout=10, isolation_level=None)
    connection.row_factory = sqlite3.Row
    return connection


def _row(row) -> Invitation | None:
    return Invitation(**dict(row)) if row else None


def init() -> None:
    with lock:
        connection = _connect()
        try:
            connection.execute("PRAGMA journal_mode=WAL")
            connection.execute(_SCHEMA)
        finally:
            connection.close()


def create(kind: str, locale: str, days: int = config.DEFAULT_DAYS, label: str = "",
           note: str = "", user_id: str | None = None, username: str | None = None,
           now: float | None = None) -> tuple[Invitation, str]:
    """A new invitation and its code. The code is returned once and never stored."""
    if kind not in ("new", "recovery"):
        raise ValueError(f"unknown kind: {kind}")
    if locale not in config.LOCALES:
        raise ValueError(f"unsupported locale: {locale}")
    if not 1 <= days <= config.MAX_DAYS:
        raise ValueError(f"validity must be 1 to {config.MAX_DAYS} days")
    if (kind == "recovery") != (user_id is not None):
        raise ValueError("a recovery invitation names an account; a new one does not")
    now = int(time.time() if now is None else now)
    code = codes.new_code()
    with lock:
        connection = _connect()
        try:
            cursor = connection.execute(
                "INSERT INTO invitations (kind, code_hash, label, note, locale, created_at,"
                " expires_at, keycloak_user_id, username) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
                (kind, codes.digest(code), label, note, locale, now, now + days * 86400,
                 user_id, username))
            return by_id(cursor.lastrowid, connection), code
        finally:
            connection.close()


def by_id(invitation_id: int, connection: sqlite3.Connection | None = None) -> Invitation | None:
    own = connection is None
    connection = connection or _connect()
    try:
        return _row(connection.execute(
            "SELECT * FROM invitations WHERE id = ?", (invitation_id,)).fetchone())
    finally:
        if own:
            connection.close()


def by_code(code: str) -> Invitation | None:
    """The invitation for a normalized code, whatever its state."""
    connection = _connect()
    try:
        return _row(connection.execute(
            "SELECT * FROM invitations WHERE code_hash = ?", (codes.digest(code),)).fetchone())
    finally:
        connection.close()


def all_invitations() -> list[Invitation]:
    connection = _connect()
    try:
        return [_row(r) for r in connection.execute(
            "SELECT * FROM invitations ORDER BY created_at DESC, id DESC")]
    finally:
        connection.close()


def _update(sql: str, args: tuple) -> bool:
    with lock:
        connection = _connect()
        try:
            return connection.execute(sql, args).rowcount == 1
        finally:
            connection.close()


def claim(invitation_id: int, user_id: str, username: str, now: float | None = None) -> bool:
    """Bind a new-member invitation to the account just created for it.

    Only an unclaimed, live invitation can be claimed, so a second redeem of the
    same code can never bind a second account.
    """
    now = int(time.time() if now is None else now)
    return _update(
        "UPDATE invitations SET claimed_at = ?, keycloak_user_id = ?, username = ?"
        " WHERE id = ? AND kind = 'new' AND claimed_at IS NULL AND revoked_at IS NULL"
        " AND completed_at IS NULL AND expires_at > ?",
        (now, user_id, username, invitation_id, now))


def mark_started(invitation_id: int, now: float | None = None) -> None:
    """Record the first link minted for a recovery invitation."""
    now = int(time.time() if now is None else now)
    _update("UPDATE invitations SET claimed_at = ? WHERE id = ? AND claimed_at IS NULL",
            (now, invitation_id))


def complete(invitation_id: int, now: float | None = None) -> None:
    now = int(time.time() if now is None else now)
    _update("UPDATE invitations SET completed_at = ? WHERE id = ? AND completed_at IS NULL",
            (now, invitation_id))


def revoke(invitation_id: int, now: float | None = None) -> bool:
    now = int(time.time() if now is None else now)
    return _update(
        "UPDATE invitations SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL"
        " AND completed_at IS NULL", (now, invitation_id))
