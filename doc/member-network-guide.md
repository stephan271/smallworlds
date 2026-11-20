# The Member Network: a guide for members

Some communities keep their applications on a **Member Network**. Files,
Photos, Git, the whiteboard, webmail and the dashboard then open only on
devices that have joined it. To everyone else on the internet they look as if
they did not exist. Your community chose this to keep strangers away from the
sign-in pages and the applications behind them.

Some things work without the Member Network:

- video calls
- the join page
- signing in
- **share links you send to other people**: someone who is not a member opens
  a shared folder or album without installing anything

You set this up once on each phone, tablet and computer you use. It takes a few
minutes.

*For Operators:* this guide is meant to be passed on. The join page shows the
same steps to every new member, with the community's address filled in. Below,
`<domain>` stands for the community's domain, for example `example.org`.

## What you need

- **Your account and its passkey.** You made them with your invitation. You
  sign in to the Member Network with the same passkey; there is no separate
  password and no Tailscale account.
- **The Tailscale app, version 1.104 or later.** Older versions connect but do
  not find the applications. Your app store updates it; check under the app's
  Settings → About if something does not work.

## Connect a device

The server address is `https://network.<domain>`. Enter it exactly as written,
with `https://`.

### Android

1. Install **Tailscale** from Google Play.
2. Open it and tap **Connect**. Allow the VPN connection Android asks for. If a
   page with Google, Microsoft or Apple opens after that, close it without
   signing in.
3. Tap the gear (or your profile picture) at the top right. In Settings, tap the
   account row at the very top. Then tap the three dots at the top right and
   choose **Use an alternate server**.
4. Enter `https://network.<domain>` and tap **Add account**.
5. Your community's sign-in page opens. Sign in with your passkey.

"Use an alternate server" only appears after step 2. If the menu does not show
it, go back and tap Connect first.

### iPhone and iPad

1. Install **Tailscale** from the App Store.
2. Open it. Before signing in, tap the profile icon at the top right, then
   **Settings** (or **Log in**). In the menu at the top right, choose **Use a
   custom coordination server**.
3. Enter `https://network.<domain>` and tap **Log in**.
4. Sign in with your passkey.

These steps follow Tailscale's own documentation and have not yet been tried on
an iPhone. If a menu is not where it says, tell your Operator what you see.

### A computer (macOS, Windows, Linux)

1. Install Tailscale from <https://tailscale.com/download>.
2. Open a terminal (on Windows, the Command Prompt) and enter:

   ```
   tailscale login --login-server=https://network.<domain>
   ```

   A browser window opens with your community's sign-in page.
3. Sign in with your passkey.

The server also has its own step-by-step pages: `https://network.<domain>/apple`
for Macs and `https://network.<domain>/windows` for Windows.

## Using the applications

Nothing changes in how you use them. They keep the same addresses
(`files.<domain>`, `photos.<domain>`, and so on), and the dashboard links to
all of them. Two things to know:

- **Tailscale has to be switched on.** On a phone, the app's switch, or the key
  symbol in the status bar, shows it. It can stay on all the time; only the
  community's applications go through it, everything else takes its usual way.
- **The app holds one account per network, and only one is active at a time.**
  If you also use Tailscale for work, both accounts sit side by side in the
  app's account list. Switch to your community's account to reach its
  applications.

Mail apps reach the community's mail server (`mail.<domain>`) only through the
Member Network as well. Receiving mail from outside is not affected.

## Sharing with people who are not members

Share a folder, a file or an album as you always did. The link works for anyone
you send it to, without the Tailscale app. They see what you shared and
nothing else.

## When something does not work

| What you see | What to do |
|---|---|
| **"404 page not found"** on `files.<domain>` or another application | This device is not connected. Check that Tailscale is switched on, that your community's account is the active one, and that the app is version 1.104 or later. |
| **"Use an alternate server" is not in the menu** (Android) | Tap **Connect** on the app's first screen and allow the VPN connection, then look again. |
| The sign-in page does not open after entering the address | Check the address: `https://network.<domain>`, with `https://` and nothing after it. |
| You got a new phone | Set it up as above; the old one keeps working until it is removed. |
| You lost a phone or computer | Tell your Operator, who can remove that one device. Your passkey on that device should be removed too (the Operator can do both). |

When you leave the community and your account is deleted, all your devices
leave the Member Network within five minutes. You can then remove the account
from the Tailscale app.

## For the Operator

- **Removing one device**, for example a lost phone:

  ```bash
  kubectl -n member-network exec deploy/headscale -c headscale -- headscale nodes list
  kubectl -n member-network exec deploy/headscale -c headscale -- headscale nodes delete --identifier <ID> --force
  ```

  Remove the device's passkey too, in the Keycloak admin console (`smallworlds`
  realm, Users, the member, Credentials), or give the member a new one with the
  Invitation Desk's **New passkey for a member**.
- **Removing a member** removes their devices by itself: delete the account in
  the Keycloak admin console, and `member-reconciler` takes the devices off the
  Member Network within five minutes (`docs/adr/0066`). Disabling the account
  keeps the devices.
- **How it is built:** `docs/adr/0066` and `doc/hardening.md` §1.
