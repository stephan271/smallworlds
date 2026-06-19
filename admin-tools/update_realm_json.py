import json

file_path = "infrastructure/kubernetes/tenants/keycloak/smallworlds-realm.json"

with open(file_path, "r") as f:
    data = json.load(f)

# 1. Enable Edit Username
data["editUsernameAllowed"] = True

# 2. Modify 'forms' flow to only require WebAuthn Passwordless
for flow in data.get("authenticationFlows", []):
    if flow.get("alias") == "forms":
        # Clear existing executions in the 'forms' flow
        flow["authenticationExecutions"] = [
            {
                "authenticator": "webauthn-authenticator-passwordless",
                "authenticatorFlow": False,
                "requirement": "REQUIRED",
                "priority": 10,
                "autheticatorFlow": False,
                "userSetupAllowed": False
            }
        ]
        break

# Write back
with open(file_path, "w") as f:
    json.dump(data, f, indent=4)

print("Successfully updated smallworlds-realm.json for Passkey-only login!")
