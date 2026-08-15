#!/usr/bin/env python3

import argparse
import csv
import os
import requests
import shutil
import subprocess
import sys

# Default Keycloak settings
KEYCLOAK_URL = os.getenv("KEYCLOAK_URL", "https://identity.smallworlds.network")
REALM = os.getenv("KEYCLOAK_REALM", "smallworlds")
CLIENT_ID = os.getenv("KEYCLOAK_CLIENT_ID", "bulk-invite")
CLIENT_SECRET = os.getenv("KEYCLOAK_CLIENT_SECRET")

# Update the profile to choose a real username, then register a passkey. The
# realm has no password form at all (see keycloak-config-instructions.md), so
# this link is the member's only way in until they hold a passkey.
REQUIRED_ACTIONS = ["UPDATE_PROFILE", "webauthn-register-passwordless"]
ACCOUNT_CLIENT_ID = "account"
ACCOUNT_REDIRECT_URI = f"{KEYCLOAK_URL}/realms/{REALM}/account/"

def get_admin_token():
    url = f"{KEYCLOAK_URL}/realms/{REALM}/protocol/openid-connect/token"
    if not CLIENT_SECRET:
        print("KEYCLOAK_CLIENT_SECRET environment variable is required.", file=sys.stderr)
        sys.exit(1)

    payload = {
        "client_id": CLIENT_ID,
        "client_secret": CLIENT_SECRET,
        "grant_type": "client_credentials"
    }
    response = requests.post(url, data=payload)
    if response.status_code != 200:
        print(f"Failed to authenticate: {response.text}", file=sys.stderr)
        sys.exit(1)
    return response.json()["access_token"]

def find_user_by_username(token, username):
    """Exact-match lookup. Keycloak's `username` filter is a partial match by
    default, so `exact=true` is required and the result is re-checked here: a
    prefix match would otherwise hand back somebody else's account."""
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users"
    headers = {"Authorization": f"Bearer {token}"}
    response = requests.get(url, headers=headers,
                            params={"username": username, "exact": "true"})
    if response.status_code != 200:
        return None
    for user in response.json():
        if user.get("username", "").lower() == username.lower():
            return user
    return None

def create_user(token, username, email, phone):
    """Create the member, or return the existing account when it is provably the
    same person. Returns (user_id, None) or (None, reason)."""
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    payload = {
        "username": username,
        "enabled": True,
        "attributes": {}
    }
    if email:
        payload["email"] = email
        # Nothing verifies this address — onboarding is out of band and the
        # realm never mails the member. It is contact metadata, not a proof.
        payload["emailVerified"] = True
    if phone:
        payload["attributes"]["phoneNumber"] = phone

    response = requests.post(url, json=payload, headers=headers)
    if response.status_code == 201:
        # Keycloak returns the new id in Location. Searching for it instead is
        # what made the previous version fail on every genuinely new user.
        location = response.headers.get("Location", "")
        if location:
            return location.rstrip("/").rsplit("/", 1)[-1], None
        existing = find_user_by_username(token, username)
        return (existing["id"], None) if existing else (None, "created but could not be looked up")

    if response.status_code == 409:
        existing = find_user_by_username(token, username)
        if not existing:
            return None, "username is taken but the account could not be read back"
        # A 409 means the username is taken, not that this is the same person.
        # Minting a link without checking would log the wrong member into it.
        existing_email = (existing.get("email") or "").lower()
        if email and existing_email and existing_email != email.lower():
            return None, (f"username '{username}' already belongs to {existing_email} — "
                          f"refusing to issue a link for {email}. Choose a distinct username.")
        if email and not existing_email:
            return None, (f"username '{username}' already exists with no email on record — "
                          f"refusing to guess whether this is {email}.")
        print(f"User {username} already exists — re-issuing.", file=sys.stderr)
        return existing["id"], None

    return None, response.text

def generate_link(token, user_id):
    """Mint the onboarding action token and return its URL instead of mailing it.

    Served by the action-token-generator SPI in infrastructure/keycloak-spi/,
    which ships with the Keycloak tenant and is mounted into providers/. It
    builds the same ExecuteActionsActionToken that execute-actions-email would,
    with the same lifespan — only the mailer step is dropped. This is the path
    that lets a cluster onboard members with no mail capability at all.
    """
    url = f"{KEYCLOAK_URL}/realms/{REALM}/action-token-link/generate-link"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    payload = {
        "userId": user_id,
        "actions": REQUIRED_ACTIONS,
        "clientId": ACCOUNT_CLIENT_ID,
        # Keycloak validates this against the client's registered redirects; a
        # value it rejects fails only when the member clicks the link.
        "redirectUri": ACCOUNT_REDIRECT_URI,
    }

    response = requests.post(url, json=payload, headers=headers)
    if response.status_code == 200:
        return response.json()["link"]
    if response.status_code == 404:
        print(f"Failed to generate link for user {user_id}: the action-token-link SPI "
              f"is not deployed on this Keycloak. Use --email, or see "
              f"admin-tools/keycloak-config-instructions.md.", file=sys.stderr)
        return None
    print(f"Failed to generate link for user {user_id}: {response.text}", file=sys.stderr)
    return None

def send_action_email(token, user_id):
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users/{user_id}/execute-actions-email?client_id={ACCOUNT_CLIENT_ID}&redirect_uri={ACCOUNT_REDIRECT_URI}"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}

    response = requests.put(url, json=REQUIRED_ACTIONS, headers=headers)
    if response.status_code == 204:
        print(f"Successfully sent onboarding email to user {user_id}.")
        return True
    else:
        print(f"Failed to send email to user {user_id}: {response.text}", file=sys.stderr)
        return False

def get_link_lifespan(token):
    """Seconds an admin-generated action token stays valid, or None if unreadable."""
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}"
    headers = {"Authorization": f"Bearer {token}"}
    response = requests.get(url, headers=headers)
    if response.status_code != 200:
        return None
    return response.json().get("actionTokenGeneratedByAdminLifespan")

def format_lifespan(seconds):
    if seconds is None:
        return "an unknown period"
    if seconds >= 3600:
        return f"{seconds // 3600} hour(s)"
    return f"{seconds // 60} minute(s)"

def open_private(path):
    """Open for writing with 0600 — every row is a bearer credential."""
    return os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600),
                     "w", encoding="utf-8", newline="")

def resolve_username(row):
    """Username from the CSV, or derived from the address for legacy files.

    Explicit usernames are preferred because deriving them collides: ana@a.test
    and ana@b.test both want 'ana', and the loser of that race would otherwise
    be handed a link into the winner's account.
    """
    username = (row.get("username") or "").strip()
    if username:
        return username
    email = (row.get("email") or "").strip()
    if not email:
        return None
    # Forgejo rejects '@' in usernames, so the local part is all that can be used.
    return email.split("@")[0]

def write_qr(link, username, directory):
    """One printable SVG per member. Vector, so it scales to whatever the sheet
    needs; an onboarding link is ~600 characters, which is a dense symbol —
    print it at 4 cm or more or phone cameras will struggle."""
    path = os.path.join(directory, f"{username}.svg")
    try:
        import qrcode
        import qrcode.image.svg
        img = qrcode.make(link, image_factory=qrcode.image.svg.SvgPathImage)
        img.save(path)
    except ImportError:
        if not shutil.which("qrencode"):
            print("  --qr needs either the 'qrcode' Python package "
                  "(pip install qrcode) or the 'qrencode' binary.", file=sys.stderr)
            return None
        subprocess.run(["qrencode", "-t", "SVG", "-l", "M", "-o", path, link],
                       check=True)
    os.chmod(path, 0o600)
    return path

def main():
    parser = argparse.ArgumentParser(
        description="Bulk invite users to Keycloak with passkey onboarding. By "
                    "default the onboarding links are written to a file for the "
                    "operator to distribute out of band, so no mail capability "
                    "is required.")
    parser.add_argument("csv_file",
                        help="CSV with headers: username,phone[,email]. 'email' "
                             "alone is accepted for older files, in which case the "
                             "username is its local part.")
    parser.add_argument("--email", action="store_true",
                        help="Have Keycloak mail each link instead of writing them "
                             "out. Requires a working SMTP relay (see doc/mail.md).")
    parser.add_argument("-o", "--output", default="invite-links.csv",
                        help="Where to write username,phone,email,link rows "
                             "(default: invite-links.csv, mode 0600). Ignored "
                             "with --email.")
    parser.add_argument("--qr", metavar="DIR", nargs="?", const="invite-qr",
                        help="Also write one printable SVG QR code per member into "
                             "DIR (default: invite-qr). For handing out on paper or "
                             "showing 1:1; never to a group.")
    args = parser.parse_args()

    token = get_admin_token()
    results = []
    failures = 0
    seen = {}

    if args.qr:
        os.makedirs(args.qr, mode=0o700, exist_ok=True)

    with open(args.csv_file, mode='r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            email = (row.get("email") or "").strip()
            phone = (row.get("phone") or "").strip()
            username = resolve_username(row)

            if not username:
                continue

            print(f"Processing {username}...")

            # Catch collisions inside the file itself, which Keycloak cannot:
            # the second row would look like an ordinary re-invite.
            if username.lower() in seen:
                print(f"  Duplicate username '{username}' in {args.csv_file} "
                      f"(already used by {seen[username.lower()]}) — skipped.",
                      file=sys.stderr)
                failures += 1
                continue
            seen[username.lower()] = email or username

            user_id, reason = create_user(token, username, email, phone)
            if not user_id:
                print(f"  {reason}", file=sys.stderr)
                failures += 1
                continue

            if args.email:
                if not send_action_email(token, user_id):
                    failures += 1
                continue

            link = generate_link(token, user_id)
            if not link:
                failures += 1
                continue

            results.append((username, phone, email, link))
            if args.qr and not write_qr(link, username, args.qr):
                failures += 1

    if not args.email:
        if results:
            with open_private(args.output) as f:
                writer = csv.writer(f)
                writer.writerow(["username", "phone", "email", "link"])
                writer.writerows(results)
            print(f"\nWrote {len(results)} onboarding link(s) to {args.output} (mode 0600).")
            if args.qr:
                print(f"Wrote {len(results)} QR code(s) to {args.qr}/ — print at 4 cm "
                      f"or larger.")
            print(f"Each link is valid for {format_lifespan(get_link_lifespan(token))} "
                  f"and logs the holder into that account. Hand it over in person, or "
                  f"1:1 in a Jitsi guest room; never to a group, and not by e-mail or SMS.")
        else:
            print("\nNo onboarding links were generated.", file=sys.stderr)

    if failures:
        print(f"{failures} user(s) failed — see errors above.", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
