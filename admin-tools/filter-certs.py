#!/usr/bin/env python3
import sys
import json
import os

def is_letsencrypt_prod(item):
    """
    Check if the certificate item was created by the 'letsencrypt-prod' issuer.
    """
    # The letsencrypt-prod cluster issuer (the account registration) itself
    if item.get("metadata", {}).get("name") == "letsencrypt-prod":
        return True

    ann = item.get("metadata", {}).get("annotations", {})
    issuer = ann.get("cert-manager.io/cluster-issuer-name") or ann.get("cert-manager.io/issuer-name")
    
    return issuer == "letsencrypt-prod"

def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} <input-file> <output-file>")
        print("Example: ./filter-certs.py certs-backup.yaml filtered-certs.yaml")
        sys.exit(1)

    input_file = sys.argv[1]
    output_file = sys.argv[2]

    if not os.path.exists(input_file):
        print(f"Error: Input file '{input_file}' not found.")
        sys.exit(1)

    try:
        with open(input_file, "r") as f:
            data = json.load(f)
    except json.JSONDecodeError as e:
        print(f"Error reading JSON from '{input_file}': {e}")
        print("Note: This script expects the backup file to be in JSON format (even if the extension is .yaml).")
        sys.exit(1)

    filtered_items = []
    
    # Handle both List objects and single items
    if "items" in data:
        items_to_process = data["items"]
    elif "metadata" in data:
        items_to_process = [data]
    else:
        print("Error: Input file does not appear to be a valid Kubernetes resource list.")
        sys.exit(1)

    for item in items_to_process:
        if is_letsencrypt_prod(item):
            filtered_items.append(item)

    # Wrap back into a Kubernetes List
    output_data = {
        "apiVersion": "v1",
        "kind": "List",
        "items": filtered_items
    }

    with open(output_file, "w") as f:
        json.dump(output_data, f, indent=2)

    print(f"✅ Filtered certificates. Kept {len(filtered_items)} out of {len(items_to_process)} items.")
    print(f"💾 Saved to {output_file}")

if __name__ == "__main__":
    main()
