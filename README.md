# SmallWorlds: Community Setup Guide

Welcome! This guide explains how to spin up your very own decentralized SmallWorlds server. 

SmallWorlds uses a **GitOps architecture**. This means your server is controlled entirely by a Git repository that **you** own. This guarantees your data sovereignty and gives you the freedom to customize your infrastructure, while still seamlessly receiving upstream updates from the central SmallWorlds project.

> [!TIP]
> **Curious how it all fits together?** Check out the interactive [SmallWorlds Architecture Diagram](smallworlds_architecture.html) to visualize the data flows and system components.

Follow these steps to launch your "Small World".

---

## Available Applications
SmallWorlds includes a curated suite of powerful, privacy-respecting applications. You can choose exactly which apps to install during the initial setup:
- **Nextcloud (Files)**: A secure file hosting, collaboration, and synchronization platform.
- **Immich (Photos)**: A high-performance, self-hosted photo and video backup solution.
- **Forgejo (Git)**: A lightweight software forge for version control and collaborative software development.
- **Roundcube & Stalwart (Webmail)**: A modern IMAP webmail client powered by an all-in-one, secure mail server.
- **Keycloak (Auth)**: The central identity provider that manages your community's single sign-on (SSO) and passkeys.
- **Homepage (Dashboard)**: A beautiful, dynamic landing page hosted at `dashboard.your-domain` that automatically discovers and displays only the applications you have chosen to install.

---

## Step 1: Prepare your Community Configuration Repository
Before launching the server, you need a private place to store its unique configuration. This is your **Community Configuration Repository**.

**What goes in this repository?**
The initial installation script (which you'll run in Step 3) handles the "Hard Bootup"—renting the server, configuring DNS, and installing the raw Kubernetes engine. But once the server is alive, it switches to **GitOps Mode**. From that point on, any *application-level* changes or additions must be committed to this private repository!

It acts as an overlay that stores:
1. **The Remote Pointer:** Tells your server to download the core apps from the public `smallworlds` Foundation repo.
2. **Configuration Overrides (Patches):** If you want to change a setting (e.g., enable open registration on Keycloak), you store a tiny patch here. Your patch dynamically overrides the Foundation's default setting.
3. **Custom Apps:** If you want to run third-party apps alongside the Foundation apps (e.g., a Minecraft server or WordPress blog), you put their Kubernetes manifests here.
4. **Private Integrations:** A safe, private place to store API tokens or secret OAuth keys for external services.

You can automate this step using our helper script, or follow the manual instructions below.

### Option A: Automate with the helper script (Recommended)
Run the script from the root of this repository:
```bash
./prepare-community-repo.sh
```
This script will guide you through:
- Interactively selecting exactly which optional apps you want to install.
- Initializing the local directory as a Git repository.
- Generating a customized `kustomization.yaml` tailored to your app choices, along with a `.gitignore` and `README.md`.
- Creating the initial commit.
- Optionally setting up the Git remote and pushing to your private repository.

---

### Option B: Manual Setup
1. Create a **new, empty Git repository** on GitLab, GitHub, or your own Forgejo instance (e.g., `my-community-config`). Make sure it is set to **Private**.
2. Clone your new repository to your local machine.
3. Inside your new repository, create a file named `kustomization.yaml` and paste the following code:

```yaml
# kustomization.yaml
resources:
  # This line connects your server to the public Central Foundation Repository.
  # When the central repo updates, your foundation apps (Nextcloud, Immich) update too.
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=HEAD

patches:
  # This is where you will add your specific domain overrides later
  # - target:
  #     kind: Ingress
  #   patch: |- ...
```

4. Commit and push this file to your new repository.

---

## Step 2: Prepare your Cloud Account
We use Hetzner Cloud because of its strict European privacy laws and excellent performance-to-cost ratio.

1. Go to the [Hetzner Cloud Console](https://console.hetzner.cloud/) and create an account.
2. Create a new project (e.g., "My Community Cloud").
3. Go to **Security > API Tokens** and generate a new token with **Read & Write** permissions.
4. Copy the token immediately. You will need it for the installer.

---

## Step 3: Run the SmallWorlds Installer
Now you are ready to provision your server. You will run the automated script from the public **Central Foundation Repository**, but you will point it to your private **Community Configuration Repository**.

1. Clone the Central Foundation Repository to your local machine:
   ```bash
   git clone https://github.com/stephan271/smallworlds.git
   cd smallworlds
   ```

2. Run the initialization script:
   ```bash
   ./smallworlds-init.sh
   ```

3. **Important:** When the script asks for your GitOps Repository Configuration:
   * **URL:** Provide the **HTTPS URL** of your repository (e.g., `https://github.com/your-user/my-community-config.git`). **SSH URLs are not supported.**
   * **Username:** Your Git hosting platform username.
   * **Access Token (PAT):** You must use a **Personal Access Token (PAT)**. For security, use a Fine-Grained token scoped strictly to this private repository, with **Read-only** permissions for **Contents** (and metadata). Do not use your primary platform password.

The script will now provision your server on Hetzner, install Kubernetes, and configure ArgoCD to listen to your **Community Configuration Repository**.

---

## Step 4: Configure DNS
Once the script finishes successfully, it will print your server's new public IP address.

Because you provided your Hetzner API token during the setup, the script automatically configures your DNS records using the Hetzner DNS service! All required subdomains (e.g. auth, files, photos, webmail, git) are automatically created and pointed to your new server.

---

## Step 5: Configure Onboarding Mode (Optional)
By default, your SmallWorlds instance is set to **invitation-only** mode (closed registration).
If you want to allow **self-registration** (anyone can sign up), you can enable it by adding a simple patch to your `kustomization.yaml` file in your **Community Configuration Repository**.

Add the following to your `patches:` block:
```yaml
patches:
  - target:
      kind: Job
      name: keycloak-realm-config
      namespace: keycloak
    patch: |-
      - op: replace
        path: /spec/template/spec/containers/0/env/1/value
        value: "self-registration"
```
ArgoCD will automatically detect this change and configure Keycloak for you!

---

## Step 6: Add Custom Apps (Optional)
Because you own your **Community Configuration Repository**, you are not limited to the standard SmallWorlds apps!

If you want to host a Wordpress blog or a Minecraft server, simply add the Kubernetes `.yaml` files to your repository, and list them under the `resources:` block in your `kustomization.yaml` file. ArgoCD will automatically detect the changes and deploy your custom apps alongside the SmallWorlds foundation.

Welcome to your sovereign cloud!

---

## Rebuilding Your Server

There may come a time when you need to recreate your SmallWorlds server from scratch (for example, if you want to upgrade the underlying VM size or start with a completely fresh Kubernetes state).

SmallWorlds supports two options for rebuilding:

### Option 1: Restart, but KEEP your data
This option destroys the underlying Virtual Machine but **keeps your persistent volume** intact. When the new VM boots, it reconnects to the persistent volume, and your entire cluster (including Kubernetes state and application data) wakes up exactly as it was.

1. Destroy just the server instance:
   ```bash
   cd infrastructure/terraform
   terraform destroy -target=hcloud_server.smallworlds_pilot_node
   ```
2. Re-apply the Terraform configuration to spin up a new server:
   ```bash
   terraform apply
   ```

### Option 2: True Nuke (Wipe Data, Keep Certificates)
This option will completely wipe the Kubernetes database and all application data, giving you a 100% clean slate. However, it automatically backs up your Let's Encrypt certificates so you do not hit rate limits when you restart.

1. Run the fresh rebuild script to backup your certificates and wipe the data from the volume:
   ```bash
   ./admin-tools/prepare-fresh-rebuild.sh
   ```
2. Destroy the server instance:
   ```bash
   cd infrastructure/terraform
   terraform destroy -target=hcloud_server.smallworlds_pilot_node
   ```
3. Re-apply the Terraform configuration:
   ```bash
   terraform apply
   ```
The new server will boot, automatically inject the saved certificates, and spin up an entirely fresh set of applications.

---

## Keeping Your Cloud Up to Date
Because your server relies on the **Central Foundation Repository** as a base, you will occasionally want to pull in updates (e.g., when a new version of Nextcloud is released, or when a security patch is applied to the central infrastructure).

### Manual Updates
Look at your `kustomization.yaml` file:
```yaml
  - https://github.com/stephan271/smallworlds.git/infrastructure/kubernetes?ref=HEAD
```
Currently, `ref=HEAD` means it always pulls the absolute latest commit from the **Central Foundation Repository**. Every time ArgoCD syncs (which it does automatically every few minutes), it will apply any new changes made upstream.

If you prefer more stability, you can "pin" your infrastructure to a specific release tag (e.g., `ref=v1.2.0`). When you are ready to update to `v1.3.0`, you simply edit your `kustomization.yaml` file, commit the change, and ArgoCD will handle the rest!

### Automated Updates (Recommended)
You can install a dependency-update bot like **RenovateBot** or **Dependabot** on your **Community Configuration Repository**. 
If your `kustomization.yaml` is pinned to a specific version tag, the bot will automatically detect when the **Central Foundation Repository** releases a new version and open a Pull Request/Merge Request in your **Community Configuration Repository**. 
You simply review the request, click "Merge", and your server updates itself automatically!
