#!/usr/bin/env bash
set -e

# Colors for pretty output
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${CYAN}======================================================${NC}"
echo -e "${CYAN}   SmallWorlds: Community Repo Prepared Tool          ${NC}"
echo -e "${CYAN}======================================================${NC}"
echo ""
echo "This helper script automates Step 1 of the SmallWorlds setup:"
echo "1. Initializes a local directory as a Git repository."
echo "2. Creates the required kustomization.yaml pointing to upstream."
echo "3. Creates basic .gitignore and README.md files."
echo "4. Makes the initial commit."
echo "5. Optionally configures the remote repository and pushes it."
echo ""

# Get the directory of the current script to ensure relative paths work
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

CONFIG_FILE="$HOME/.config/smallworlds/community-setup.conf"
mkdir -p "$(dirname "$CONFIG_FILE")"
if [ -f "$CONFIG_FILE" ]; then
    source "$CONFIG_FILE"
fi

# 1. Ask for Target Domain
if [ -n "$STORED_TARGET_DOMAIN" ]; then
    DEFAULT_DOMAIN="$STORED_TARGET_DOMAIN"
else
    DEFAULT_DOMAIN="smallworlds.network"
fi
read -e -i "$DEFAULT_DOMAIN" -p "1. Enter your target base domain (e.g. smallworlds.network): " TARGET_DOMAIN

# 2. Ask for Environment Extension
if [ -n "$STORED_ENV_EXT" ]; then
    DEFAULT_ENV_EXT="$STORED_ENV_EXT"
else
    DEFAULT_ENV_EXT=""
fi
read -e -i "$DEFAULT_ENV_EXT" -p "2. Enter environment extension (e.g. -dev, or leave empty for prod): " ENV_EXT

# 3. Ask for local repository path
if [ -n "$STORED_REPO_PATH" ]; then
    # If the stored path exists but the extension changed, maybe we shouldn't use the stored path verbatim.
    # But to keep it simple, we just use it if it exists.
    DEFAULT_PATH="$STORED_REPO_PATH"
else
    # Script is in admin-tools, so community repo is normally parallel to smallworlds
    DEFAULT_PATH="$(cd "$SCRIPT_DIR/.." && pwd)/../my-community${ENV_EXT}-config"
fi
read -e -i "$DEFAULT_PATH" -p "3. Enter the path where you want to create the repository: " REPO_PATH

# Resolve to absolute path
mkdir -p "$REPO_PATH"
ABS_REPO_PATH="$(cd "$REPO_PATH" && pwd)"

echo -e "Target directory: ${GREEN}${ABS_REPO_PATH}${NC}"
echo ""

# 4. Ask for the remote Git URL
DEFAULT_URL=""
if [ -n "$STORED_REMOTE_URL" ]; then
    DEFAULT_URL="$STORED_REMOTE_URL"
elif [ -d "$ABS_REPO_PATH/.git" ]; then
    pushd "$ABS_REPO_PATH" > /dev/null
    if git remote | grep -q "^origin$"; then
        DEFAULT_URL=$(git remote get-url origin)
    fi
    popd > /dev/null
fi

read -e -i "n" -p "4. Do you want to automatically create a new private GitHub repository using the GitHub CLI (gh)? (y/n): " CREATE_REPO
if [[ "$CREATE_REPO" =~ ^[Yy]$ ]]; then
    if ! command -v gh &> /dev/null; then
        echo -e "${RED}Error: 'gh' (GitHub CLI) is not installed. Please install it first or answer 'n' to enter an existing URL.${NC}"
        CREATE_REPO="n"
    else
        # Extract default name from REPO_PATH
        DEFAULT_REPO_NAME=$(basename "$ABS_REPO_PATH")
        read -e -i "$DEFAULT_REPO_NAME" -p "   Enter the name for the new repository: " NEW_REPO_NAME
        
        echo -e "${YELLOW}Creating private repository '$NEW_REPO_NAME' on GitHub...${NC}"
        if gh repo create "$NEW_REPO_NAME" --private; then
            # Get the HTTPS URL
            REPO_URL=$(gh repo view "$NEW_REPO_NAME" --json url -q .url)
            REMOTE_URL="${REPO_URL}.git"
            echo -e "${GREEN}Successfully created repository: $REMOTE_URL${NC}"
        else
            echo -e "${RED}Failed to create repository. Falling back to manual URL entry.${NC}"
            CREATE_REPO="n"
        fi
    fi
fi

if [[ ! "$CREATE_REPO" =~ ^[Yy]$ ]]; then
    while true; do
        if [ -n "$DEFAULT_URL" ]; then
            read -e -i "$DEFAULT_URL" -p "4b. Enter your private Git Remote HTTPS URL: " REMOTE_URL
        else
            read -e -p "4b. Enter your private Git Remote HTTPS URL (required, e.g., https://github.com/user/my-community-config.git): " REMOTE_URL
        fi
        
        if [ -n "$REMOTE_URL" ]; then
            break
        else
            echo -e "${RED}Error: Git Remote URL is required to properly configure ArgoCD patches.${NC}"
        fi
    done
fi

# 5. Pin to a specific upstream smallworlds release tag (recommended). Pinning
# makes updates deliberate and reproducible: ArgoCD only adopts a new base when
# you bump this tag and commit. Enter HEAD to always track the latest main
# (not recommended for production — ArgoCD picks it up non-deterministically on
# cache expiry).
if [ -n "$STORED_VERSION" ]; then
    DEFAULT_VERSION="$STORED_VERSION"
else
    DEFAULT_VERSION="v1.0.0"
fi
read -e -i "$DEFAULT_VERSION" -p "5. Pin to which upstream smallworlds version tag (e.g. v1.0.0, or HEAD to track latest): " SMALLWORLDS_VERSION
SMALLWORLDS_VERSION="${SMALLWORLDS_VERSION:-HEAD}"
# Derive the "owner/repo" slug from the remote URL so the in-cluster Renovate
# CronJob can scan THIS repo and open weekly base-tag bump PRs against it.
REPO_SLUG=$(printf '%s' "$REMOTE_URL" | sed -E 's#^(https?://[^/]+/|git@[^:]+:|ssh://[^/]+/)##; s#\.git$##; s#/+$##')

echo ""
echo -e "${YELLOW}Initializing repository...${NC}"
echo -e "Pinning upstream base to: ${GREEN}${SMALLWORLDS_VERSION}${NC}"

# Navigate to target directory
cd "$ABS_REPO_PATH"

# Initialize git if not already done
if [ ! -d ".git" ]; then
    git init -b main
else
    echo -e "${YELLOW}Note: Git repository already initialized in this directory.${NC}"
fi

# 3. Ask which apps to install
echo -e "${YELLOW}Selecting Optional Applications...${NC}"
OPTIONAL_APPS=("forgejo" "immich" "nextcloud" "bulwark" "excalidraw" "jitsi" "collabora" "plane")
SELECTED_APPS=()

for app in "${OPTIONAL_APPS[@]}"; do
    # Check stored preference
    var_name="STORED_APP_${app}"
    stored_val="${!var_name}"
    default_choice="${stored_val:-y}"
    
    read -e -i "$default_choice" -p "Do you want to install $app? (y/n): " choice
    if [[ "$choice" =~ ^[Yy]$ ]]; then
        SELECTED_APPS+=("$app")
        eval "STORED_APP_${app}='y'"
    else
        eval "STORED_APP_${app}='n'"
    fi
done

echo -e "${YELLOW}Creating application subdirectories...${NC}"
APPS=("dashboard" "keycloak" "stalwart" "${SELECTED_APPS[@]}")

for app in "${APPS[@]}"; do
    if [ ! -d "$app" ]; then
        mkdir -p "$app"
        cat <<EOF > "$app/kustomization.yaml"
resources:
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes/tenants/$app?ref=${SMALLWORLDS_VERSION}

# Add your patches for $app here
# patches:
#   - target:
#       kind: Ingress
#     patch: |- ...
EOF
    fi
    # Generate domain patches if using a different domain or extension
    if [ "$TARGET_DOMAIN" != "smallworlds.network" ] || [ -n "$ENV_EXT" ]; then
        python3 "$SCRIPT_DIR/generate_domain_patches.py" \
            --app "$app" \
            --domain "$TARGET_DOMAIN" \
            --ext="$ENV_EXT" \
            --kustomization-file "$ABS_REPO_PATH/$app/kustomization.yaml"
    fi
done

# 4. Create or Update root kustomization.yaml
if [ -f "kustomization.yaml" ]; then
    echo -e "${YELLOW}Existing kustomization.yaml found. Upgrading in place...${NC}"
    for app in "${SELECTED_APPS[@]}"; do
        if ! grep -q "apps/$app.yaml" kustomization.yaml; then
            echo -e "Adding $app to kustomization.yaml..."
            # Insert resource before 'patches:' or at the end
            if grep -q "^patches:" kustomization.yaml; then
                awk '/^patches:/{print "  - https://raw.githubusercontent.com/stephan271/smallworlds/'"${SMALLWORLDS_VERSION}"'/infrastructure/kubernetes/apps/'"$app"'.yaml"}1' kustomization.yaml > kustomization.yaml.tmp && mv kustomization.yaml.tmp kustomization.yaml
            else
                echo "  - https://raw.githubusercontent.com/stephan271/smallworlds/${SMALLWORLDS_VERSION}/infrastructure/kubernetes/apps/$app.yaml" >> kustomization.yaml
            fi
            
            # Append the patch at the end
            if ! grep -q "^patches:" kustomization.yaml; then
                echo "" >> kustomization.yaml
                echo "patches:" >> kustomization.yaml
            fi
            cat <<EOF >> kustomization.yaml
  - target:
      group: argoproj.io
      kind: Application
      name: $app
    patch: |-
      - op: replace
        path: /spec/source/repoURL
        value: $REMOTE_URL
      - op: replace
        path: /spec/source/path
        value: $app
EOF
        fi
    done
else
    echo -e "${YELLOW}Creating root kustomization.yaml...${NC}"
    cat <<EOF > kustomization.yaml
# kustomization.yaml
resources:
  # This line connects your server to the public Central Foundation Repository root
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=${SMALLWORLDS_VERSION}
EOF

    for app in "${SELECTED_APPS[@]}"; do
        cat <<EOF >> kustomization.yaml
  # Include the ArgoCD Application manifest for $app
  - https://raw.githubusercontent.com/stephan271/smallworlds/${SMALLWORLDS_VERSION}/infrastructure/kubernetes/apps/$app.yaml
EOF
    done

    cat <<EOF >> kustomization.yaml

patches:
  # Route all ArgoCD Application definitions to your private repo instead of upstream
EOF

    for app in "${APPS[@]}"; do
        cat <<EOF >> kustomization.yaml
  - target:
      group: argoproj.io
      kind: Application
      name: $app
    patch: |-
      - op: replace
        path: /spec/source/repoURL
        value: $REMOTE_URL
      - op: replace
        path: /spec/source/path
        value: $app
EOF
    done
fi

# Wire the in-cluster Renovate CronJob to also scan THIS overlay repo, so the
# weekly base-tag bump PRs (see renovate.json) are raised here. Operator-specific
# (your private repo), so it lives in the overlay, not the public base. Idempotent.
if ! grep -q 'RENOVATE_REPOSITORIES' kustomization.yaml; then
    echo -e "${YELLOW}Wiring Renovate to scan ${REPO_SLUG}...${NC}"
    if ! grep -q '^patches:' kustomization.yaml; then
        printf '\npatches:\n' >> kustomization.yaml
    fi
    cat <<EOF >> kustomization.yaml
  - target:
      kind: CronJob
      name: renovate
    patch: |-
      apiVersion: batch/v1
      kind: CronJob
      metadata:
        name: renovate
      spec:
        jobTemplate:
          spec:
            template:
              spec:
                containers:
                  - name: renovate
                    env:
                      - name: RENOVATE_REPOSITORIES
                        value: "$REPO_SLUG"
EOF
fi

# Create renovate.json (weekly PRs that bump the pinned smallworlds base tag).
# Written literally (quoted heredoc) so the JSON regex/backslashes are preserved.
if [ ! -f "renovate.json" ]; then
    echo -e "${YELLOW}Creating renovate.json (weekly base-tag update PRs)...${NC}"
    cat <<'EOF' > renovate.json
{
  "$schema": "https://docs.renovatebot.com/renovate-schema.json",
  "extends": ["config:recommended"],
  "kustomize": { "enabled": false },
  "customManagers": [
    {
      "customType": "regex",
      "managerFilePatterns": ["/kustomization\\.yaml$/"],
      "matchStrings": [
        "smallworlds\\.git/[^\\s?]*\\?ref=(?<currentValue>v[0-9][0-9.]*)",
        "raw\\.githubusercontent\\.com/stephan271/smallworlds/(?<currentValue>v[0-9][0-9.]*)/"
      ],
      "depNameTemplate": "stephan271/smallworlds",
      "datasourceTemplate": "github-tags",
      "versioningTemplate": "semver"
    }
  ],
  "packageRules": [
    {
      "matchDepNames": ["stephan271/smallworlds"],
      "groupName": "SmallWorlds base",
      "schedule": ["before 6am on monday"],
      "allowedVersions": "/^v\\d+\\.\\d+\\.\\d+$/",
      "commitMessageTopic": "SmallWorlds base",
      "commitMessageExtra": "to {{{newVersion}}}"
    }
  ]
}
EOF
fi

# Save settings
cat <<EOF > "$CONFIG_FILE"
STORED_REPO_PATH="$ABS_REPO_PATH"
STORED_REMOTE_URL="${REMOTE_URL}"
STORED_VERSION="${SMALLWORLDS_VERSION}"
STORED_TARGET_DOMAIN="${TARGET_DOMAIN}"
STORED_ENV_EXT="${ENV_EXT}"
EOF
for app in "${OPTIONAL_APPS[@]}"; do
    var_name="STORED_APP_${app}"
    echo "${var_name}=${!var_name}" >> "$CONFIG_FILE"
done

# 5. Create a basic .gitignore if missing
if [ ! -f ".gitignore" ]; then
    echo -e "${YELLOW}Creating .gitignore...${NC}"
    cat <<EOF > .gitignore
# Ignore system/IDE specific files
.DS_Store
.idea/
.vscode/

# Ignore any local variables/secrets you might drop here by accident
*.secret
*.env
kubeconfig*
EOF
fi

# 6. Create a basic README.md if missing
if [ ! -f "README.md" ]; then
    echo -e "${YELLOW}Creating README.md...${NC}"
    cat <<EOF > README.md
# My SmallWorlds Community Configuration

This is the private GitOps overlay repository for my SmallWorlds sovereign cloud.

## Repository Structure
- \`kustomization.yaml\`: Connects this cluster to the upstream public SmallWorlds repository and stores configuration overrides (patches).

## Running Updates
This repo pins the upstream SmallWorlds base to a fixed release tag (currently \`${SMALLWORLDS_VERSION}\`) in every \`kustomization.yaml\` (\`?ref=<tag>\` and the raw \`.../smallworlds/<tag>/...\` App manifest URLs).

To adopt a newer upstream release, bump that tag everywhere and commit — ArgoCD watches this repo and will sync the change deterministically:

\`\`\`sh
# from this repo root, e.g. moving v1.0.0 -> v1.1.0
grep -rl 'ref=${SMALLWORLDS_VERSION}\\|/smallworlds/${SMALLWORLDS_VERSION}/' . \\
  | xargs sed -i 's#ref=${SMALLWORLDS_VERSION}#ref=v1.1.0#g; s#/smallworlds/${SMALLWORLDS_VERSION}/#/smallworlds/v1.1.0/#g'
git commit -am "Bump upstream smallworlds base to v1.1.0" && git push
\`\`\`

Pinning keeps updates deliberate, auditable and reproducible. Using \`HEAD\` instead would let ArgoCD pick up upstream changes non-deterministically (on cache expiry) — avoid it in production.

### Automated update proposals (Renovate)
\`renovate.json\` configures the in-cluster Renovate CronJob to open **one pull request every Monday** bumping the pinned base tag to the newest \`smallworlds\` release. It does **not** auto-merge — review and merge when ready (the merge is what ArgoCD deploys). This repo (\`${REPO_SLUG}\`) is added to the CronJob's scan list via a patch in \`kustomization.yaml\`. The Git token Renovate uses must have pull-request/write access to this repo.
EOF
fi

# 7. Commit the files
echo -e "${YELLOW}Committing updates...${NC}"
git add .
if [ -n "$(git status --porcelain)" ]; then
    git commit -m "Automated update: Synchronized SmallWorlds applications"
else
    echo -e "${GREEN}No changes to commit.${NC}"
fi

# 8. Configure remote and optionally push
echo ""
echo -e "${YELLOW}Configuring remote URL...${NC}"
# Check if origin already exists
if git remote | grep -q "^origin$"; then
    git remote set-url origin "$REMOTE_URL"
else
    git remote add origin "$REMOTE_URL"
fi

echo -e "Remote 'origin' set to: ${GREEN}${REMOTE_URL}${NC}"

read -e -i "y" -p "Would you like to attempt pushing to origin main now? (y/n): " PUSH_CHOICE

if [[ "$PUSH_CHOICE" =~ ^[Yy]$ ]]; then
    echo -e "${YELLOW}Pushing to remote repository...${NC}"
    if git push -u origin main; then
        echo -e "${GREEN}Successfully pushed to remote!${NC}"
    else
        echo -e "${RED}Warning: Failed to push to remote repository.${NC}"
        echo -e "Please ensure your repository exists on the host and your SSH keys / access credentials are set up correctly."
        echo -e "You can try pushing manually later using: ${CYAN}git push -u origin main${NC}"
    fi
fi

echo ""
echo -e "${GREEN}======================================================${NC}"
echo -e "${GREEN}           Repository Prepared Successfully!           ${NC}"
echo -e "${GREEN}======================================================${NC}"
echo ""
echo -e "Your Community Configuration Repository is ready at:"
echo -e "  ${CYAN}${ABS_REPO_PATH}${NC}"
echo ""
echo "Next Steps:"
echo -e "1. Make sure this repository is set to ${YELLOW}Private${NC} on your Git host."
echo -e "2. Go to Step 2 in the main README.md to configure your Hetzner Cloud account."
echo -e "3. In Step 3, run the installer from the smallworlds directory:"
echo -e "   ${CYAN}./smallworlds-init.sh${NC}"
echo -e "   When prompted for the GitOps repository details, provide the repository URL:"
echo -e "   ${CYAN}${REMOTE_URL:-"(your remote repository URL)"}${NC}"
echo ""
echo -e "${GREEN}======================================================${NC}"
