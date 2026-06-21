#!/usr/bin/env python3

import argparse
import csv
import os
import requests
import sys

# Default Keycloak settings
KEYCLOAK_URL = os.getenv("KEYCLOAK_URL", "https://identity.smallworlds.network")
REALM = os.getenv("KEYCLOAK_REALM", "smallworlds")
ADMIN_USER = os.getenv("KEYCLOAK_ADMIN_USER", "admin")
ADMIN_PASS = os.getenv("KEYCLOAK_ADMIN_PASS", "admin")
CLIENT_ID = os.getenv("KEYCLOAK_CLIENT_ID", "bulk-invite")
CLIENT_SECRET = os.getenv("KEYCLOAK_CLIENT_SECRET")

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
        # Fetch existing user ID
        search_url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users?username={email}"
        search_response = requests.get(search_url, headers=headers)
        if search_response.status_code == 200 and len(search_response.json()) > 0:
            return search_response.json()[0]["id"]
    else:
        print(f"Failed to create user {email}: {response.text}", file=sys.stderr)
    return None

def send_action_email(token, user_id):
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users/{user_id}/execute-actions-email?client_id=account&redirect_uri={KEYCLOAK_URL}/realms/{REALM}/account/"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    
    # Required actions: update profile to choose username, then register passkey
    payload = ["UPDATE_PROFILE", "webauthn-register-passwordless"]
    
    response = requests.put(url, json=payload, headers=headers)
    if response.status_code == 204:
        print(f"Successfully sent onboarding email to user {user_id}.")
        return True
    else:
        print(f"Failed to send email to user {user_id}: {response.text}", file=sys.stderr)
        return False

def main():
    parser = argparse.ArgumentParser(description="Bulk Invite Users to Keycloak with Passkey Onboarding")
    parser.add_argument("csv_file", help="Path to CSV file with headers: email,phone")
    args = parser.parse_args()

    token = get_admin_token()

    with open(args.csv_file, mode='r', encoding='utf-8') as f:
        reader = csv.DictReader(f)
        for row in reader:
            email = row.get("email")
            phone = row.get("phone", "")
            
            if not email:
                continue

            print(f"Processing {email}...")
            user_id = create_user(token, email, phone)
            
            if user_id:
                send_action_email(token, user_id)

if __name__ == "__main__":
    main()
