/**
 * Provision test users in Keycloak for E2E smoke tests.
 *
 * Creates users with password-based login (no passkey enrollment).
 * Users are prefixed with "sw-test-" to distinguish them from real users.
 *
 * Usage:
 *   DOMAIN=smallworlds.network KC_ADMIN_PASS=<password> npx tsx setup/provision-test-users.ts
 */

const DOMAIN = process.env.DOMAIN;
const KC_ADMIN_PASS = process.env.KC_ADMIN_PASS;
const REALM = 'smallworlds';

if (!DOMAIN || !KC_ADMIN_PASS) {
  console.error('❌ Required environment variables: DOMAIN, KC_ADMIN_PASS');
  process.exit(1);
}

const KEYCLOAK_URL = `https://identity.${DOMAIN}`;

export const TEST_USERS = [
  {
    username: 'sw-test-alice',
    email: `sw-test-alice@${DOMAIN}`,
    firstName: 'Alice',
    lastName: 'Testuser',
    password: 'SmallW0rlds-Test!',
  },
  {
    username: 'sw-test-bob',
    email: `sw-test-bob@${DOMAIN}`,
    firstName: 'Bob',
    lastName: 'Testuser',
    password: 'SmallW0rlds-Test!',
  },
];

async function getAdminToken(): Promise<string> {
  const url = `${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token`;
  const body = new URLSearchParams({
    client_id: 'admin-cli',
    grant_type: 'password',
    username: 'admin',
    password: KC_ADMIN_PASS,
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

async function findUser(token: string, username: string): Promise<string | null> {
  const url = `${KEYCLOAK_URL}/admin/realms/${REALM}/users?username=${encodeURIComponent(username)}&exact=true`;
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

async function createUser(
  token: string,
  user: (typeof TEST_USERS)[0]
): Promise<void> {
  // Check if user already exists
  const existingId = await findUser(token, user.username);
  if (existingId) {
    console.log(`  ⏭  User ${user.username} already exists (id: ${existingId})`);
    // Update password in case it changed
    await setPassword(token, existingId, user.password);
    return;
  }

  const url = `${KEYCLOAK_URL}/admin/realms/${REALM}/users`;
  const payload = {
    username: user.username,
    email: user.email,
    firstName: user.firstName,
    lastName: user.lastName,
    enabled: true,
    emailVerified: true,
    // No required actions — skip passkey enrollment for test users
    requiredActions: [],
    credentials: [
      {
        type: 'password',
        value: user.password,
        temporary: false,
      },
    ],
  };

  const response = await fetch(url, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  });

  if (response.status === 201) {
    console.log(`  ✅ Created user ${user.username}`);
  } else if (response.status === 409) {
    console.log(`  ⏭  User ${user.username} already exists`);
  } else {
    const text = await response.text();
    console.error(`  ❌ Failed to create ${user.username} (${response.status}): ${text}`);
  }
}

async function setPassword(
  token: string,
  userId: string,
  password: string
): Promise<void> {
  const url = `${KEYCLOAK_URL}/admin/realms/${REALM}/users/${userId}/reset-password`;
  const response = await fetch(url, {
    method: 'PUT',
    headers: {
      Authorization: `Bearer ${token}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      type: 'password',
      value: password,
      temporary: false,
    }),
  });

  if (response.ok) {
    console.log(`  🔑 Password set/updated for user ${userId}`);
  } else {
    const text = await response.text();
    console.error(`  ❌ Failed to set password (${response.status}): ${text}`);
  }
}

async function main() {
  console.log(`\n🔧 Provisioning test users on ${KEYCLOAK_URL}...\n`);

  const token = await getAdminToken();
  console.log('  🔑 Obtained admin token\n');

  for (const user of TEST_USERS) {
    await createUser(token, user);
  }

  console.log('\n✅ Test user provisioning complete.\n');
  console.log('Test credentials:');
  for (const user of TEST_USERS) {
    console.log(`  ${user.username} / ${user.password}`);
  }
  console.log('');
}

main().catch((err) => {
  console.error('❌ Provisioning failed:', err.message);
  process.exit(1);
});
