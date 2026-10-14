"""Member Invitation codes: 10 characters of Crockford base32, 50 bits.

Crockford's alphabet leaves out I, L, O and U, so a code read off a printed card
or typed from a phone survives the usual confusions: input is upper-cased,
separators are dropped, and O/I/L are read as 0/1/1. Only a hash is stored.
"""
import hashlib
import secrets

ALPHABET = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
LENGTH = 10
_READ_AS = str.maketrans({"O": "0", "I": "1", "L": "1"})


def new_code() -> str:
    return "".join(secrets.choice(ALPHABET) for _ in range(LENGTH))


def normalize(text: str) -> str | None:
    """The canonical form of a typed or scanned code, or None if it cannot be one."""
    if not isinstance(text, str):
        return None
    code = "".join(ch for ch in text.upper() if ch not in " -_.\t").translate(_READ_AS)
    if len(code) != LENGTH or any(ch not in ALPHABET for ch in code):
        return None
    return code


def display(code: str) -> str:
    return f"{code[:5]}-{code[5:]}"


def digest(code: str) -> str:
    return hashlib.sha256(code.encode("ascii")).hexdigest()
