# Installing SmallWorlds on a Contabo VPS with Datalix S3 storage

This guide covers one fixed setup and nothing else:

| | |
|---|---|
| Server | One Contabo Cloud VPS 8 with NVMe storage, no extra disk |
| Storage | S3 object storage at Datalix, for files, photos and all backups |
| Applications | Files (Nextcloud), Photos (Gallery), Meet (Jitsi), plus the always-installed sign-in and dashboard |
| Backups | In separate buckets in the same Datalix account |
| DNS | One wildcard record that you create at your own DNS provider |
| Install route | The browser setup wizard |

> **Status: this exact combination is untried.** Nobody has yet completed it
> against a real Contabo server and a real Datalix account, or with a DNS
> record kept by hand. Use a domain and a storage account you can throw away
> for the first run, and expect to report what breaks.
>
> The same path was completed once on a rented server at another provider, with
> an S3 provider, Gallery and DNS managed through Hetzner (2026-10-11),
> including joining the private network and closing the admin ports. Steps 9
> to 12 and 14 follow that run; step 13 is written from the tool, not from a
> confirmed run.

Allow about two hours: one for the install, most of it waiting, and one for the
steps after it.

## What you end up with

- Your community's apps at `files.`, `photos.` and `meet.<your-domain>`, with
  single sign-on at `identity.<your-domain>` and a start page at
  `dashboard.<your-domain>`, all with Let's Encrypt certificates.
- Files, photos and backups stored at Datalix. The VPS disk holds only the
  databases, the software and temporary files.
- A firewall on the VPS that admits only web traffic and Jitsi's media port
  from the internet. SSH, the Kubernetes API and the operator pages are
  reachable only over the cluster's own private network.
- An account with a passkey for every member, and none with a password.

Two things to know before you start:

- **Datalix can read what is stored there.** SmallWorlds does not encrypt
  files, photos or backups before sending them.
- **Backups share the Datalix account with the live data.** They survive losing
  the VPS, not losing the account.

## 1. What you need

Accounts, and what each is for:

| Account | Why |
|---|---|
| Contabo | The server |
| Datalix | S3 object storage |
| A domain registrar or DNS provider | A domain you own, for example `example.org`, where you can add a DNS record |
| GitHub | A private repository that holds your cluster's settings |

On your own computer (Linux or macOS): `git`, `python3`, `ssh`, `curl`,
`openssl`, and an SSH key pair. `kubectl` is useful afterwards but not needed
to install.

## 2. Order the server at Contabo

1. Order a **Cloud VPS 8** and choose the **NVMe** storage option.
2. Choose **Debian 12** or **Ubuntu 24.04** as the image.
3. Add your SSH public key so that you can log in as `root`.
4. When the server is ready, note its IPv4 address and confirm you can log in:

   ```bash
   ssh root@<server-ip>
   ```

5. On the server, make sure the firewall tool is present:

   ```bash
   apt update && apt install -y nftables
   ```

   The installer stops if it is missing.

Do not add a second disk. Nothing in this setup uses one.

### If SSH is not on port 22

The wizard has no field for an SSH port, and `root@<server-ip>:2222` does not
work. Give the server a name in `~/.ssh/config` on your own computer instead:

```
Host sw-node
    HostName <server-ip>
    Port 2222
```

Check it with `ssh root@sw-node`, and from here on use `root@sw-node` wherever
this guide says `root@<server-ip>`, including in the wizard.

The firewall the install sets up keeps the port open that your SSH server
listens on, whichever it is.

### If you log in as another user than root

A user with `sudo` works too, for example `ubuntu@<server-ip>`. It must be able
to use `sudo` without a password (`ssh ubuntu@<server-ip> sudo -n true` prints
nothing and succeeds). Use that login wherever this guide says
`root@<server-ip>`, and put `sudo` in front of the commands this guide runs on
the server.

### If the server already has a firewall

A fresh Contabo server has none, and you can skip this. A server that was set up
for something else may have one, and it must let the cluster's internal traffic
in, or the install stops and tells you so. Look for it:

```bash
nft list ruleset | grep -B3 'policy drop'
```

If that prints an `input` chain, add these lines inside that chain, in the file
that loads it at boot (usually `/etc/nftables.conf`):

```
iifname { "cni0", "flannel.1" } accept
tcp dport { 80, 443, 6443 } accept
udp dport 10000 accept
```

Load the file with `nft -f /etc/nftables.conf`. **Never restart the `nftables`
service once the cluster runs:** stopping it empties every firewall table,
including the cluster's own, and the cluster's networking breaks.

## 3. Rent the storage at Datalix

1. Order S3-compatible object storage.
2. Create an access key. Note four values from the Datalix panel:
   - the **endpoint URL**, for example `https://s3.example-datalix.tld` (the
     endpoint only, without a bucket name);
   - the **region**;
   - the **access key ID**;
   - the **secret access key**.
3. Do not create any buckets. The install creates seven, all named with a
   prefix derived from your domain (for `example.org`: `sw-example-org-…`):
   `nextcloud`, `nextcloud-backup`, `immich`, `immich-backup`,
   `postgres-backups`, `postgres-backups-keycloak` and `velero-backups`.

   If your key is not allowed to create buckets, create these seven by hand
   first, with exactly those names after the prefix.

## 4. Create the DNS record

At your domain's DNS provider, add one record:

| Type | Name | Value |
|---|---|---|
| A | `*` (a wildcard, i.e. `*.example.org`) | the server's IPv4 address from step 2 |

That single record covers every application. Wait until it is visible (a
minute to an hour):

```bash
dig +short identity.example.org      # must print the server's address
```

The install checks this first and stops, having changed nothing, if the name
does not point at the server yet.

## 5. Create a GitHub token

Under **GitHub → Settings → Developer settings → Personal access tokens**,
create a classic token with the `repo` scope. The wizard uses it to create the
private settings repository, and the cluster uses it to read that repository.

## 6. Run the wizard

On your own computer:

```bash
git clone https://github.com/stephan271/smallworlds.git
cd smallworlds
./smallworlds-setup
```

A browser opens. Fill in the five panels as follows.

**Panel 1 — Your community**

| Field | Enter |
|---|---|
| Domain | your domain, e.g. `example.org` |
| Your email address | your address |
| Everything else | leave as offered |

A note under the domain field about Hetzner nameservers can be ignored; it
clears once you have chosen the kind of machine on the next panel.

**Panel 2 — Where it runs**

| Field | Enter |
|---|---|
| Kind of machine | **A server you rent elsewhere** |
| The machine | `root@<server-ip>` (or `root@sw-node`, see step 2, if SSH is not on port 22) |
| Data directory | leave as offered |
| Photo library directory | leave empty |
| Backup directory | ignore it — it disappears with the storage choice below |
| DNS records | **I create one record myself** (already selected) |

Then scroll down past the **Check this machine** button. **Where files and
backups are kept** is a section of its own at the bottom of the panel, with two
large choices, not a field in the list above:

| Field | Enter |
|---|---|
| Where files and backups are kept | **At an S3 storage provider** |
| S3 endpoint, Region, Access key ID, Secret access key | the four values from step 3 |
| Bucket name prefix | leave as offered |
| Send backups to a different provider | leave closed |

Choosing the provider removes the Backup directory field and shows the S3
fields. If the panel has no such section at all, your checkout predates it:
run `git pull` in the `smallworlds` directory and start `./smallworlds-setup`
again.

Press **Check this machine**. Wait for the storage check under the secret key
to turn green before you continue; a red result means the endpoint, region or
key is wrong, and the install would fail later for the same reason.

**Panel 3 — Your settings repository**

| Field | Enter |
|---|---|
| Git access token | the token from step 5 |
| Repository | **Create one for me** |
| Advanced settings → SmallWorlds release to pin | `HEAD` — see the note below |

> The wizard suggests the newest release tag, but the newest release predates
> this setup. Until a release that includes it is published, type `HEAD`.

**Panel 4 — What your community gets**

Tick **Files**, **Photos** and **Meet**; untick everything else. Under the app
list, set **Photos runs** to **Gallery**. Leave the Office editor at "No online
office editor".

**Panel 5 — Ready to install**

Read the plan. It should say the server is a rented server, that files and
backups go to your Datalix endpoint, and that Photos runs Gallery. Then press
**Install**.

The install takes fifteen to thirty minutes. You can close the tab and reopen
the link from your terminal; the install carries on. At the end the wizard
shows the admin passwords once. **Save them.**

If the install stops, the page says what to do. Fix that, then press **Install
again** on the same page; it carries on from where it got to. **Change my
answers** takes you back to the panels first.

## 7. After the install: the order matters

The cluster is running, but it is not ready to use yet: nobody has an account,
and SSH and the Kubernetes API are still open to the internet. Do the remaining
steps in this order.

| Step | What | Why it comes here |
|---|---|---|
| 8 | Save the passwords, check the cluster | The wizard shows the passwords once |
| 9 | Join the server to its private network | Everything after needs it |
| 10 | Join your own computer | Needs the server joined first |
| 11 | Check access over the private network | Must work before anything is closed |
| 12 | Close SSH and the Kubernetes API to the internet | Only safe after step 11 |
| 13 | Create the member accounts | The tool only works over the private network |
| 14 | Check from outside | Proves the operator pages are closed |

Two kinds of account exist, and they are easy to confuse:

- **`admin` and `operator`** are Keycloak's own administrators. Their passwords
  are among those the wizard showed. They work only at
  `https://identity.<your-domain>/admin/` and cannot sign in to the dashboard or
  any application.
- **Members** sign in to the dashboard and the applications with a passkey.
  None exist after the install, so every name is answered with "Invalid
  username or email" until you have done step 13.

## 8. Save the passwords and check the cluster

1. **Save the passwords** with **Save to a file** in the wizard. If the page is
   gone: the Keycloak and Grafana ones are in `.smallworlds-cache.env` in the
   checkout (`KC_PASS`, `KC_OPERATOR_PASS`, `GRAFANA_PASS`, `INVITE_SECRET`),
   and the Argo CD one is read from the cluster:

   ```bash
   export KUBECONFIG=~/.smallworlds/kubeconfigs/local.yaml
   kubectl -n argocd get secret argocd-initial-admin-secret \
     -o jsonpath='{.data.password}' | base64 -d; echo
   ```

2. **Check that everything arrived.** Every line should end in
   `Synced Healthy` and every certificate should read `True`; give it a few
   minutes after the install.

   ```bash
   kubectl get applications -n argocd
   kubectl get certificates -A
   ```

3. **Open `https://dashboard.<your-domain>`.** You should see the sign-in page.
   You cannot sign in yet. If the browser gets no answer at all while
   `dig +short dashboard.<your-domain> @1.1.1.1` prints the server's address,
   your computer still remembers an older address for the name: run
   `sudo resolvectl flush-caches` and restart the browser.

4. **Look at Datalix.** The seven buckets should exist. After the first night,
   the `…-backup`, `…-postgres-backups` and `…-velero-backups` buckets should
   contain data.

5. **Turn on versioning and an expiry rule** for the buckets in the Datalix
   panel, if Datalix offers them. The install does not, and without them a
   deleted file is gone from the live bucket for good (the nightly backup copy
   keeps earlier versions under `versions/<date>`).

## 9. Join the server to its private network

The operator pages — `deploy.<your-domain>` (Argo CD), `monitoring.<your-domain>`
(Grafana) and `identity.<your-domain>/admin/` (Keycloak administration) — answer
only to devices on the cluster's own private network, a tailnet run by the
cluster itself. From anywhere else they answer `403`. The server joins first:

```bash
./admin-tools/setup-vpn.sh
```

It stops three times:

1. To install the Tailscale client on the server. Answer `y`.
2. To have you create one more DNS record. It prints `admin.<your-domain>` and
   the server's tailnet address (it starts with `100.`). Create that A record
   at your DNS provider and press Enter. It has to be its own record; the wildcard would
   otherwise answer for it with the public address. **Note the address**; you
   need it in step 11.
3. To restart K3s for a few seconds, so that the Kubernetes API's certificate
   also covers the new name. Answer `y`; the applications keep running.

At the end it prints the commands for steps 10 to 12 with your login and SSH
port filled in. Use those where they differ from the ones below.

## 10. Join your own computer

Install the Tailscale client on your computer (<https://tailscale.com/download>),
then:

```bash
sudo tailscale login --login-server=https://vpn.<your-domain>
```

It prints a link that ends in an id. Approve it on the server:

```bash
ssh root@<server-ip> 'k3s kubectl -n headscale exec deploy/headscale -- \
  headscale auth register --auth-id <the-id> --user infra'
```

The page behind the link shows the same `headscale auth register` command with
the id filled in.

**If your computer already uses Tailscale** for something else, it can be on
only one network at a time. The command above adds the cluster's network as a
second one and switches to it; `tailscale switch --list` shows both and
`sudo tailscale switch <name>` moves between them. While you are on the other
network, nothing in steps 11 to 13 works.

## 11. Check access over the private network

Do not skip this: step 12 closes the way you have been using so far.

1. **SSH:**

   ```bash
   ssh root@admin.<your-domain>
   ```

2. **The Kubernetes API.** Point your kubeconfig at the new name, then ask for
   the node:

   ```bash
   sed -i 's#https://<server-ip>:6443#https://admin.<your-domain>:6443#' \
     ~/.smallworlds/kubeconfigs/local.yaml
   kubectl get nodes
   ```

3. **The operator pages.** Their names resolve to the server's public address,
   where they answer `403`. Tell your computer to use the tailnet address from
   step 9 for them instead, by adding this line to `/etc/hosts`:

   ```
   <tailnet-address>  deploy.<your-domain> monitoring.<your-domain> identity.<your-domain>
   ```

   Then open `https://deploy.<your-domain>` and sign in as `admin` with the
   Argo CD password. The line only works while your computer is on the
   cluster's network; if you switch to another one (step 10), put a `#` in
   front of it, or `identity.<your-domain>` stops answering on this computer
   and no application lets you sign in.

## 12. Close SSH and the Kubernetes API to the internet

Over the private network, as in step 11:

```bash
ssh root@admin.<your-domain> smallworlds-firewall close
```

If you log in as a user with `sudo`:

```bash
ssh <user>@admin.<your-domain> sudo --preserve-env=SSH_CONNECTION smallworlds-firewall close
```

The command refuses unless the session it runs in came over the private
network, which is what stops you locking yourself out. `--preserve-env` is what
lets it see that through `sudo`.

From now on `ssh`, `kubectl` and the wizard reach the server only while your
computer is on the cluster's network. To undo it: `smallworlds-firewall open`.
If you do get locked out, use the web console in the Contabo panel and run that
command there.

## 13. Create the member accounts

Members are invited: each gets a one-time link, registers a passkey with it,
and never has a password. No mail is sent. Your computer must be on the
cluster's network with the `/etc/hosts` line from step 11 in place, because the
tool uses Keycloak's administration interface.

1. Write a list, one line per member, outside the checkout — for example
   `~/members.csv`. Only the username is required:

   ```
   username,phone,email
   anna,,anna@example.org
   ben,,
   ```

2. Create the accounts and their links:

   ```bash
   KEYCLOAK_URL="https://identity.<your-domain>" \
   KEYCLOAK_CLIENT_SECRET="$(grep '^INVITE_SECRET=' .smallworlds-cache.env | cut -d'"' -f2)" \
     python3 admin-tools/bulk-invite.py ~/members.csv -o ~/invite-links.csv
   ```

   The links are in `~/invite-links.csv`, and the tool says how long they are
   valid. `--qr` also writes a QR code per member, for phones. Treat the file
   like a list of passwords until the links have been used.

3. Give each member their link, yourself included. Opening it asks for three
   things: confirm the profile, register a passkey (fingerprint, phone or
   security key), and write down the recovery codes. The codes are the way back
   in when the passkey device is lost; without them only you can help, by
   issuing a new link.

4. Sign in at `https://dashboard.<your-domain>` with your passkey.

Members need nothing installed and are never on the private network: the
dashboard and the applications are public by design.

## 14. Check from outside

From a device that is **not** on the cluster's network — a phone on mobile data
will do, or your computer after switching networks:

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://deploy.<your-domain>/
curl -s -o /dev/null -w '%{http_code}\n' https://monitoring.<your-domain>/
curl -s -o /dev/null -w '%{http_code}\n' https://identity.<your-domain>/admin/
curl -s -o /dev/null -w '%{http_code}\n' https://dashboard.<your-domain>/
```

The first three must print `403` and the last `200`. `ssh root@<server-ip>`
must no longer connect. If an operator page answers anything but `403`, stop
and read `doc/hardening.md` §1 before inviting anyone.

Your community's cloud is now ready to use.

## If something goes wrong

| What you see | Likely cause | What to do |
|---|---|---|
| Storage check is red in the wizard | Wrong endpoint, region or key | Compare with the Datalix panel. The endpoint must not contain a bucket name. |
| Install stops at "Bootstrapping", mentions `nft` | `nftables` not installed | Step 2.5, then press Install again |
| Install stops, saying the machine's own firewall would block the cluster | The server already had a firewall that drops by default | "If the server already has a firewall" in step 2, then press Install again |
| Install stops after 15 minutes waiting for cert-manager | Pods cannot reach the Kubernetes API; `kubectl get pods -A` shows many restarts | Almost always the same firewall cause as the row above |
| Install stops with a checksum mismatch | Upstream no longer serves the K3s installer or Argo CD manifest that was pinned | Nothing was installed. `git pull` in the checkout and try again; if it persists, report it (`infrastructure/bootstrap-pins.env`) |
| Apps never become healthy; `garage-init` jobs keep restarting | Datalix refused to create a bucket | `kubectl -n nextcloud logs job/garage-init` names the bucket. Create it by hand, or allow the key to create buckets. |
| Photos does not start | Gallery on this setup is the least-tested part | `kubectl -n immich get pods` and the logs of `immich-server`. To fall back to Immich, re-run the wizard with **Photos runs: Immich**; photos then live on the VPS disk. |
| Install stops at once, saying the name does not point at the server | The wildcard record is missing, wrong, or not visible yet | Re-check step 4; press Install again when `dig` prints the server's address |
| Certificates stay pending | Port 80 is not reachable from the internet, or the record changed | `smallworlds-firewall status` must list `tcp 80`; `kubectl get challenges -A` shows what Let's Encrypt saw |
| Cannot SSH after the install | The firewall missed your SSH port | Contabo web console, then `smallworlds-firewall open`; report the port your `sshd` uses |
| The browser gets no answer at all for an address | Your computer remembers an older address for the name | `sudo resolvectl flush-caches`, restart the browser (step 8.3) |
| "Invalid username or email" at every sign-in | No member accounts exist yet; `admin` and `operator` are not members | Step 13 |
| An operator page answers `403` from your own computer | You are not on the cluster's network, or the `/etc/hosts` line is missing | Steps 10 and 11.3 |
| The invite tool fails with `403` or cannot authenticate | Same cause: it uses Keycloak's administration interface | Steps 10 and 11.3 |
| `setup-vpn.sh` says it cannot run commands as root | Your login needs `sudo` without a password | "If you log in as another user than root" in step 2 |
| `smallworlds-firewall close` refuses | The session did not come over the private network, or `sudo` hid that | Step 12 |
| Video in Meet fails, chat works | UDP 10000 blocked | `smallworlds-firewall status` should list `udp 10000` |

Installing again is safe, from the wizard's own page or after restarting it: it
keeps the generated passwords and the firewall state, and replaces its own
settings in the repository.

## Starting over

On the server:

```bash
sudo bash /path/to/bootstrap-local-node.sh --uninstall --purge-data
```

(the script is `infrastructure/local/bootstrap-local-node.sh` in the checkout;
copy it over first). This removes the cluster, the firewall and all data on
the VPS. The buckets at Datalix are not touched: empty or delete them in the
Datalix panel before installing again under the same prefix, or old data will
be picked up.

## Background

- Why object storage can be external: `docs/adr/0053`
- Why Gallery, and what it changes: `docs/adr/0054`
- Why a rented server uses this path, and how the firewall works: `docs/adr/0055`
- Why no DNS provider account is needed: `docs/adr/0056`
- Why the operator pages are private and the applications are not: `doc/hardening.md`
- How members sign in, and why without passwords: `doc/tenant-keycloak.md`
- Every other installation option: `README.md` and `doc/local-deployment.md`
