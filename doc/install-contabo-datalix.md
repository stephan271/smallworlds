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

> **Status: untried.** Every part of this path passes its automated tests, but
> nobody has yet completed this install against a real Contabo server and a real
> Datalix account. Use a domain and a storage account you can throw away for
> the first run, and expect to report what breaks.

Allow about an hour, most of it waiting.

## What you end up with

- Your community's apps at `files.`, `photos.` and `meet.<your-domain>`, with
  single sign-on at `identity.<your-domain>` and a start page at
  `dashboard.<your-domain>`, all with Let's Encrypt certificates.
- Files, photos and backups stored at Datalix. The VPS disk holds only the
  databases, the software and temporary files.
- A firewall on the VPS that admits only SSH, web traffic, the Kubernetes API
  and Jitsi's media port.

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
this guide says `root@<server-ip>`, including in the wizard. Keep the `root@`
in front: the installer uses `sudo` for any target that does not start with it.

The firewall the install sets up keeps the port open that your SSH server
listens on, whichever it is.

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

## 7. After the install

1. **Check you can still log in.** Before closing your SSH session to the
   server, open a second one. Then look at the firewall:

   ```bash
   ssh root@<server-ip> smallworlds-firewall status
   ```

2. **Wait for certificates.** Each application gets its certificate as it
   starts, within a few minutes. Until then, browsers warn.
3. **Open `https://dashboard.<your-domain>`** and sign in.
4. **Look at Datalix.** The seven buckets should exist. After the first night,
   the `…-backup`, `…-postgres-backups` and `…-velero-backups` buckets should
   contain data.
5. **Turn on versioning and an expiry rule** for the buckets in the Datalix
   panel, if Datalix offers them. The install does not, and without them a
   deleted file is gone from the live bucket for good (the nightly backup copy
   keeps earlier versions under `versions/<date>`).

Your kubeconfig is at `~/.smallworlds/kubeconfigs/local.yaml`.

## 8. Optional: reach the admin pages and close the admin ports

The operator pages (`deploy.<your-domain>` for Argo CD and
`monitoring.<your-domain>` for Grafana) answer only to devices on the
cluster's private network (a tailnet). SSH and the Kubernetes API, on the
other hand, are still open to the internet. One step fixes both:

```bash
./admin-tools/setup-vpn.sh
```

It joins the server to the tailnet, offers to install the Tailscale client on
it, and prints how to join your own computer. Partway through it shows one more
DNS record for you to create, `admin.<your-domain>` pointing at the server's
tailnet address. It has to be its own record; the wildcard would otherwise
answer for it with the public address.

When `ssh root@admin.<your-domain>` works from your computer, close the public
admin ports:

```bash
ssh root@admin.<your-domain> smallworlds-firewall close
```

The command refuses unless you run it over the tailnet, which is what stops you
locking yourself out. To undo it: `smallworlds-firewall open`. If you do get
locked out, use the web console in the Contabo panel and run that command
there.

## If something goes wrong

| What you see | Likely cause | What to do |
|---|---|---|
| Storage check is red in the wizard | Wrong endpoint, region or key | Compare with the Datalix panel. The endpoint must not contain a bucket name. |
| Install stops at "Bootstrapping", mentions `nft` | `nftables` not installed | Step 2.5, then press Install again |
| Apps never become healthy; `garage-init` jobs keep restarting | Datalix refused to create a bucket | `kubectl -n nextcloud logs job/garage-init` names the bucket. Create it by hand, or allow the key to create buckets. |
| Photos does not start | Gallery on this setup is the least-tested part | `kubectl -n immich get pods` and the logs of `immich-server`. To fall back to Immich, re-run the wizard with **Photos runs: Immich**; photos then live on the VPS disk. |
| Install stops at once, saying the name does not point at the server | The wildcard record is missing, wrong, or not visible yet | Re-check step 4; run the install again when `dig` prints the server's address |
| Certificates stay pending | Port 80 is not reachable from the internet, or the record changed | `smallworlds-firewall status` must list `tcp 80`; `kubectl get challenges -A` shows what Let's Encrypt saw |
| Cannot SSH after the install | The firewall missed your SSH port | Contabo web console, then `smallworlds-firewall open`; report the port your `sshd` uses |
| Video in Meet fails, chat works | UDP 10000 blocked | `smallworlds-firewall status` should list `udp 10000` |

Re-running the wizard is safe: it keeps the generated passwords and the
firewall state, and replaces its own settings in the repository.

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
- Every other installation option: `README.md` and `doc/local-deployment.md`
