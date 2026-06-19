#!/usr/bin/env python3

import argparse
import csv
import os
import requests
import sys

# Default Keycloak settings
KEYCLOAK_URL = os.getenv("KEYCLOAK_URL", "http://localhost:8080")
REALM = os.getenv("KEYCLOAK_REALM", "master")
ADMIN_USER = os.getenv("KEYCLOAK_ADMIN_USER", "admin")
ADMIN_PASS = os.getenv("KEYCLOAK_ADMIN_PASS", "admin")
CLIENT_ID = os.getenv("KEYCLOAK_CLIENT_ID", "admin-cli")

def get_admin_token():
    url = f"{KEYCLOAK_URL}/realms/master/protocol/openid-connect/token"
    payload = {
        "client_id": CLIENT_ID,
        "username": ADMIN_USER,
        "password": ADMIN_PASS,
        "grant_type": "password"
    }
    response = requests.post(url, data=payload)
    if response.status_code != 200:
        print(f"Failed to authenticate: {response.text}", file=sys.stderr)
        sys.exit(1)
    return response.json()["access_token"]

def create_user(token, email, phone):
    url = f"{KEYCLOAK_URL}/admin/realms/{REALM}/users"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    
    # We use email as temporary username
    payload = {
        "username": email,
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

def generate_action_link(token, user_id):
    # This calls our custom SPI
    url = f"{KEYCLOAK_URL}/realms/{REALM}/action-token-link/generate-link"
    headers = {"Authorization": f"Bearer {token}", "Content-Type": "application/json"}
    payload = {
        "userId": user_id,
        "redirectUri": f"{KEYCLOAK_URL}/realms/{REALM}/account/",
        "clientId": "account",
        "actions": ["UPDATE_PROFILE", "webauthn-register-passwordless"]
    }
    
    response = requests.post(url, json=payload, headers=headers)
    if response.status_code == 200:
        return response.json().get("link")
    else:
        print(f"Failed to generate link for user {user_id}: {response.text}", file=sys.stderr)
    return None

def send_message(email, phone, link):
    # Placeholder for actual Email / WhatsApp sending logic
    print("--------------------------------------------------")
    print(f"TO: {email} (Phone: {phone})")
    print("SUBJECT: Welcome! Please complete your registration")
    print(f"BODY:\nHello,\n\nPlease click the link below to choose your username and register your passkey for passwordless login.\n\n{link}")
    print("--------------------------------------------------")

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
                link = generate_action_link(token, user_id)
                if link:
                    send_message(email, phone, link)

if __name__ == "__main__":
    main()
