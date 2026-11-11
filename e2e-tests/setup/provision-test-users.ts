/**
 * Provision test users in Keycloak for E2E smoke tests.
 *
 * The realm has no password credential any more (see doc/tenant-keycloak.md §5),
 * so test users are onboarded exactly like real members: created with the
 * passkey required action, then handed an action-token link minted by the
 * action-token-link SPI. auth.setup.ts opens each link with a CDP virtual
 * authenticator and registers a passkey. That makes the suite exercise the real
 * onboarding path, which previously had no coverage at all.
 *
 * The admin REST API lives under /admin, which is address-restricted
 * (doc/tenant-keycloak.md §6), so this talks to KC_ADMIN_URL — normally a
 * kubectl port-forward set up by run-smoke-tests.sh — rather than the public
 * ingress. Browser tests still use the public host.
 *
 * Credentials: join's service account (KC_CLIENT_ID / KC_CLIENT_SECRET, from
 * join's keycloak-secret) is what the Invitation Desk itself uses, and it holds
 * manage-, view- and query-users in the realm. The SPI authenticates bearer
 * tokens against the realm it is called in, so a master-realm admin token is
 * refused there (401) — links can only be minted with a token of this realm.
 * KC_ADMIN_PASS still works for creating users when no client is given, but
 * then no link can be minted.
 *
 * Usage:
 *   DOMAIN=smallworlds.network KC_CLIENT_ID=join KC_CLIENT_SECRET=<secret> npx tsx setup/provision-test-users.ts
 */

import fs from 'fs';
import path from 'path';

const DOMAIN = process.env.DOMAIN;
const KC_ADMIN_PASS = process.env.KC_ADMIN_PASS;
const KC_CLIENT_ID = process.env.KC_CLIENT_ID;
const KC_CLIENT_SECRET = process.env.KC_CLIENT_SECRET;
const REALM = 'smallworlds';

if (!DOMAIN || !((KC_CLIENT_ID && KC_CLIENT_SECRET) || KC_ADMIN_PASS)) {
  console.error('❌ Required: DOMAIN, and KC_CLIENT_ID + KC_CLIENT_SECRET (join\'s service account)');
  process.exit(1);
}

const PUBLIC_URL = `https://identity.${DOMAIN}`;
/** Where the admin REST API and the SPI are reachable from this machine. */
const ADMIN_URL = process.env.KC_ADMIN_URL || PUBLIC_URL;

export const LINKS_FILE = path.join(__dirname, '.auth/invite-links.json');

export const TEST_USERS = [
  {
    username: 'sw-test-alice',
    email: `sw-test-alice@${DOMAIN}`,
    firstName: 'Alice',
    lastName: 'Testuser',
  },
  {
    username: 'sw-test-bob',
    email: `sw-test-bob@${DOMAIN}`,
    firstName: 'Bob',
    lastName: 'Testuser',
  },
];

async function getAdminToken(): Promise<string> {
  const url = `${ADMIN_URL}/realms/master/protocol/openid-connect/token`;
  const body = new URLSearchParams({
    client_id: 'admin-cli',
    grant_type: 'password',
    username: 'admin',
    password: KC_ADMIN_PASS!,
  });

  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: body.toString(),
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Failed to get admin token (${response.status}): ${text}`);
  }

  const data = await response.json();
  return data.access_token;
}

/** A token of the members' realm, from join's service account. */
async function getRealmToken(): Promise<string> {
  const url = `${ADMIN_URL}/realms/${REALM}/protocol/openid-connect/token`;
  const body = new URLSearchParams({
    client_id: KC_CLIENT_ID!,
    client_secret: KC_CLIENT_SECRET!,
    grant_type: 'client_credentials',
  });
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: body.toString(),
  });
  if (!response.ok) {
    throw new Error(`Failed to get a ${REALM} token for ${KC_CLIENT_ID} (${response.status}): ${await response.text()}`);
  }
  return (await response.json()).access_token;
}

async function findUser(token: string, username: string): Promise<string | null> {
  const url = `${ADMIN_URL}/admin/realms/${REALM}/users?username=${encodeURIComponent(username)}&exact=true`;
  const response = await fetch(url, {
    headers: { Authorization: `Bearer ${token}` },
  });

  if (!response.ok) return null;

  const users = await response.json();
  for (const user of users) {
    if (user.username === username) return user.id;
  }
  return null;
}

/**
 * Remove an existing test user's passkeys and give the registration back as a
 * required action. Every run registers a fresh passkey (the virtual
 * authenticator dies with its browser), and the login theme names a passkey
 * after the day it was made: a second one the same day was refused with "Device
 * already exists with the same name", and on Ring 0, where the checks run after
 * every commit, they would pile up.
 */
async function forgetPasskeys(token: string, userId: string): Promise<void> {
  const base = `${ADMIN_URL}/admin/realms/${REALM}/users/${userId}`;
  const headers = { Authorization: `Bearer ${token}` };
  const response = await fetch(`${base}/credentials`, { headers });
  if (!response.ok) throw new Error(`Failed to list credentials (${response.status})`);
  for (const credential of (await response.json()) as { id: string; type: string }[]) {
    if (!credential.type.startsWith('webauthn')) continue;
    const removed = await fetch(`${base}/credentials/${credential.id}`, { method: 'DELETE', headers });
    if (!removed.ok) throw new Error(`Failed to remove a passkey (${removed.status})`);
  }
  const updated = await fetch(base, {
    method: 'PUT',
    headers: { ...headers, 'Content-Type': 'application/json' },
    body: JSON.stringify({ requiredActions: ['webauthn-register-passwordless'] }),
  });
  if (!updated.ok) throw new Error(`Failed to reset required actions (${updated.status})`);
}

/**
 * Create the user, or return the existing id with its passkeys removed
 * (forgetPasskeys); a fresh link is minted either way.
 */
async function createUser(token: string, user: (typeof TEST_USERS)[0]): Promise<string | null> {
  const existingId = await findUser(token, user.username);
  if (existingId) {
    console.log(`  ⏭  User ${user.username} already exists (id: ${existingId})`);
    await forgetPasskeys(token, existingId);
    return existingId;
  }

  const url = `${ADMIN_URL}/admin/realms/${REALM}/users`;
  const payload = {
    username: user.username,
    email: user.email,
    firstName: user.firstName,
    lastName: user.lastName,
    enabled: true,
    emailVerified: true,
    // Profile fields are set above, so UPDATE_PROFILE is not needed; recovery
    // codes are skipped because nothing in the suite authenticates with them.
    requiredActions: ['webauthn-register-passwordless'],
  };

  const response = await fetch(url, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });

  if (response.status === 201) {
    console.log(`  ✅ Created user ${user.username}`);
    const location = response.headers.get('location');
    if (location) return location.replace(/\/$/, '').split('/').pop()!;
    return await findUser(token, user.username);
  }

  const text = await response.text();
  console.error(`  ❌ Failed to create ${user.username} (${response.status}): ${text}`);
  return null;
}

/** Mint an onboarding link via the action-token-link SPI (infrastructure/keycloak-spi). */
async function generateLink(token: string, userId: string): Promise<string | null> {
  const url = `${ADMIN_URL}/realms/${REALM}/action-token-link/generate-link`;
  const response = await fetch(url, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    // join's own token (docs/adr/0059), as a real invitation gets: no
    // confirmation page, and the member is signed in at the end, which is the
    // SSO session the later specs reuse. Keycloak's execute-actions token ends
    // on "Account updated" without signing anyone in. The redirect must be one
    // registered on the join client.
    body: JSON.stringify({
      userId,
      actions: ['webauthn-register-passwordless'],
      clientId: KC_CLIENT_ID || 'join',
      redirectUri: `https://join.${DOMAIN}/done/en`,
      tokenType: 'smallworlds-join',
    }),
  });

  if (response.status === 404) {
    console.error('  ❌ action-token-link SPI is not deployed on this Keycloak.');
    return null;
  }
  if (!response.ok) {
    console.error(`  ❌ Failed to mint link (${response.status}): ${await response.text()}`);
    return null;
  }

  const data = await response.json();
  // The SPI builds the link from the request's own base URI. Over a
  // port-forward that is 127.0.0.1, which the browser cannot use for a login
  // bound to the public host, so rewrite it back to the public origin.
  return (data.link as string).replace(ADMIN_URL, PUBLIC_URL);
}

async function main() {
  console.log(`\n🔧 Provisioning test users via ${ADMIN_URL}...\n`);

  const realmClient = Boolean(KC_CLIENT_ID && KC_CLIENT_SECRET);
  if (!realmClient) {
    console.error(`  ⚠ No KC_CLIENT_ID/KC_CLIENT_SECRET: users can be created, but no link minted.`);
  }
  const token = realmClient ? await getRealmToken() : await getAdminToken();
  console.log(`  🔑 Obtained ${realmClient ? `a ${REALM} token for ${KC_CLIENT_ID}` : 'a master admin token'}\n`);

  const links: Record<string, string> = {};
  for (const user of TEST_USERS) {
    const userId = await createUser(token, user);
    if (!userId) continue;
    const link = await generateLink(token, userId);
    if (link) {
      links[user.username] = link;
      console.log(`  🔗 Onboarding link ready for ${user.username}`);
    }
  }

  if (Object.keys(links).length !== TEST_USERS.length) {
    console.error('\n❌ Not every test user got an onboarding link.');
    process.exit(1);
  }

  fs.mkdirSync(path.dirname(LINKS_FILE), { recursive: true });
  fs.writeFileSync(LINKS_FILE, JSON.stringify(links, null, 2), { mode: 0o600 });
  console.log(`\n✅ Wrote onboarding links to ${LINKS_FILE}\n`);
}

main().catch((err) => {
  console.error('❌ Provisioning failed:', err.message);
  process.exit(1);
});
