#!/usr/bin/env python3

import argparse
import csv
import os
import requests
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

def create_user(token, email, phone):
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    
    # We use the part of the email before @ as temporary username, because Forgejo strictly prohibits @ in usernames
    username = email.split('@')[0]
    payload = {
        "username": username,
        "email": email,
        "enabled": True,
        "emailVerified": True, # Assume verified if we invite them
        "attributes": {}
    }
    
    if phone:
        payload["attributes"]["phoneNumber"] = phone

    response = requests.post(url, json=payload, headers=headers)
    if response.status_code == 201:
        # User created, we need to get their ID. We can't get it from location header easily if it's not returned, so we search.
        search_url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users?username={email}"
        search_response = requests.get(search_url, headers=headers)
        if search_response.status_code == 200 and len(search_response.json()) > 0:
            return search_response.json()[0]["id"]
    elif response.status_code == 409:
        print(f"User {email} already exists.", file=sys.stderr)
        username = email.split('@')[0]
        search_url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users?username={username}"
        search_response = requests.get(search_url, headers=headers)
        if search_response.status_code == 200 and len(search_response.json()) > 0:
            return search_response.json()[0]["id"]
    else:
        print(f"Failed to create user {email}: {response.text}", file=sys.stderr)
    return None

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

def main():
    parser = argparse.ArgumentParser(
        description="Bulk invite users to Keycloak with passkey onboarding. By "
                    "default the onboarding links are written to a file for the "
                    "operator to distribute out of band, so no mail capability "
                    "is required.")
    parser.add_argument("csv_file", help="Path to CSV file with headers: email,phone")
    parser.add_argument("--email", action="store_true",
                        help="Have Keycloak mail each link instead of writing them "
                             "out. Requires a working SMTP relay (see doc/mail.md).")
    parser.add_argument("-o", "--output", default="invite-links.csv",
                        help="Where to write email,phone,link rows (default: "
                             "invite-links.csv, mode 0600). Ignored with --email.")
    args = parser.parse_args()

    token = get_admin_token()
    results = []
    failures = 0

    with open(args.csv_file, mode='r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            email = row.get("email")
            phone = row.get("phone", "")

            if not email:
                continue

            print(f"Processing {email}...")
            user_id = create_user(token, email, phone)

            if not user_id:
                failures += 1
                continue

            if args.email:
                if not send_action_email(token, user_id):
                    failures += 1
                continue

            link = generate_link(token, user_id)
            if link:
                results.append((email, phone, link))
            else:
                failures += 1

    if not args.email:
        if results:
            with open_private(args.output) as f:
                writer = csv.writer(f)
                writer.writerow(["email", "phone", "link"])
                writer.writerows(results)
            print(f"\nWrote {len(results)} onboarding link(s) to {args.output} (mode 0600).")
            print(f"Each link is valid for {format_lifespan(get_link_lifespan(token))} "
                  f"and grants access to that account — distribute it over a channel "
                  f"you trust (Signal, a QR code, in person), not e-mail or SMS.")
        else:
            print("\nNo onboarding links were generated.", file=sys.stderr)

    if failures:
        print(f"{failures} user(s) failed — see errors above.", file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
