import { test, expect } from '@playwright/test';
import { execFileSync } from 'child_process';
import { createHash, randomBytes } from 'crypto';
import { mkdtempSync, readFileSync, writeFileSync, existsSync } from 'fs';
import { tmpdir } from 'os';
import { join, resolve } from 'path';

/**
 * Pod archive end-to-end.
 *
 * Unlike the other specs this is an API test, not a browser one — the pod
 * gateway has no UI. It is here because it is the only place the hand-rolled
 * SigV4 in src/s3.py meets a real Garage, and because the append-only
 * guarantees are worth asserting against the real ingress rather than a stub.
 *
 * It drives the real operator tooling (pod-enroll-device.sh) and the real
 * device agent (pod-agent.py) rather than reimplementing either, so a break in
 * those shows up here too.
 *
 * Requires KUBECONFIG, which test-pr-locally.sh exports.
 */

const DOMAIN = process.env.DOMAIN!;
const POD_URL = `https://pod.${DOMAIN}`;
const REPO = resolve(__dirname, '../..');
const ENV_NAME = process.env.POD_TEST_ENV || 'staging';

// The gateway does not require pods to correspond to real accounts — the user
// id is just a prefix — so a fixed synthetic id keeps this independent of
// whatever Immich users happen to exist.
const USER_A = '00000000-0000-4000-8000-0000000000a1';
const USER_B = '00000000-0000-4000-8000-0000000000b2';

const OBJECT_KEY = `immich/2026/2026-08-12/e2e-${randomBytes(4).toString('hex')}.jpg`;
const OBJECT_BODY = Buffer.from(`pod archive e2e ${randomBytes(8).toString('hex')}`);

let agentToken = '';
let deviceToken = '';
let caBundle = '';

function sh(command: string, args: string[], env: NodeJS.ProcessEnv = {}): string {
  return execFileSync(command, args, {
    cwd: REPO,
    encoding: 'utf-8',
    env: { ...process.env, ...env },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function kubectl(args: string[]): string {
  return sh('kubectl', args).trim();
}

/** Secret projections refresh on the kubelet's sync period, so newly minted
 *  tokens are not valid the instant the Secret is written. */
async function waitForToken(
  request: any, token: string, userId: string, expectStatus: number[],
): Promise<void> {
  for (let attempt = 0; attempt < 40; attempt++) {
    const response = await request.get(`${POD_URL}/pod/v1/${userId}/manifest`, {
      headers: { Authorization: `Bearer ${token}` },
      failOnStatusCode: false,
    });
    if (expectStatus.includes(response.status())) return;
    await new Promise((r) => setTimeout(r, 5_000));
  }
  throw new Error(`token never became active (wanted ${expectStatus.join('/')})`);
}

test.describe('Pod archive', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ request }) => {
    // Enrolment writes a Secret, and the gateway only sees it once the kubelet
    // re-projects the volume — measured at ~60s on staging. The default 90s
    // hook timeout is a coin flip against that, so allow for several syncs.
    test.setTimeout(300_000);
    test.skip(!process.env.KUBECONFIG, 'KUBECONFIG is required to mint pod tokens');
    test.skip(!kubectl(['get', 'ns', 'pod-gateway', '--ignore-not-found']),
              'pod-gateway is not deployed in this cluster');
    // The agent token is stored in the source app's namespace.
    test.skip(!kubectl(['get', 'ns', 'immich', '--ignore-not-found']),
              'immich is not deployed in this cluster');

    // Mint an append-only agent token through the real operator script.
    sh('./admin-tools/pod-enroll-device.sh', ['--agent', 'immich', '--env', ENV_NAME]);
    agentToken = Buffer.from(
      kubectl(['get', 'secret', 'immich-pod-agent', '-n', 'immich',
               '-o', 'jsonpath={.data.token}']),
      'base64',
    ).toString('utf-8');

    // ...and a device token, parsed out of the operator's enrolment string.
    const output = sh('./admin-tools/pod-enroll-device.sh',
                      ['--user', USER_A, '--name', 'e2e-device', '--env', ENV_NAME]);
    const blob = output.split('\n').map((l) => l.trim())
      .find((l) => /^[A-Za-z0-9+/=]{40,}$/.test(l));
    expect(blob, 'enrolment string was not printed').toBeTruthy();
    deviceToken = JSON.parse(Buffer.from(blob!, 'base64').toString('utf-8')).token;
    expect(deviceToken).toBeTruthy();

    // The staging issuer is self-signed; hand the CA to Python rather than
    // teaching the device agent to skip verification.
    // A CA issuer publishes ca.crt; a selfSigned issuer's leaf is its own root,
    // so tls.crt serves as the trust anchor there.
    const ca = kubectl(['get', 'secret', 'pod-gateway-tls', '-n', 'pod-gateway',
                        '-o', 'jsonpath={.data.ca\\.crt}'])
      || kubectl(['get', 'secret', 'pod-gateway-tls', '-n', 'pod-gateway',
                  '-o', 'jsonpath={.data.tls\\.crt}']);
    caBundle = join(mkdtempSync(join(tmpdir(), 'pod-ca-')), 'ca.pem');
    writeFileSync(caBundle, Buffer.from(ca, 'base64'));

    await waitForToken(request, deviceToken, USER_A, [200]);
  });

  test('an agent can append, and cannot append the same key twice', async ({ request }) => {
    const headers = {
      Authorization: `Bearer ${agentToken}`,
      'Content-Type': 'image/jpeg',
      'X-Pod-Source': 'immich',
      'X-Pod-Sha256': createHash('sha256').update(OBJECT_BODY).digest('hex'),
    };

    const created = await request.put(
      `${POD_URL}/pod/v1/${USER_A}/objects/${OBJECT_KEY}`,
      { headers, data: OBJECT_BODY, failOnStatusCode: false },
    );
    expect(created.status(), await created.text()).toBe(201);
    const entry = await created.json();
    expect(entry.seq).toBeGreaterThan(0);
    expect(entry.sha256).toBe(headers['X-Pod-Sha256']);

    const repeated = await request.put(
      `${POD_URL}/pod/v1/${USER_A}/objects/${OBJECT_KEY}`,
      { headers, data: Buffer.from('overwritten'), failOnStatusCode: false },
    );
    expect(repeated.status()).toBe(409);
  });

  test('an agent cannot read, and a device cannot write', async ({ request }) => {
    const asAgent = await request.get(
      `${POD_URL}/pod/v1/${USER_A}/objects/${OBJECT_KEY}`,
      { headers: { Authorization: `Bearer ${agentToken}` }, failOnStatusCode: false },
    );
    expect(asAgent.status()).toBe(403);

    const manifestAsAgent = await request.get(`${POD_URL}/pod/v1/${USER_A}/manifest`, {
      headers: { Authorization: `Bearer ${agentToken}` }, failOnStatusCode: false,
    });
    expect(manifestAsAgent.status()).toBe(403);

    const asDevice = await request.put(
      `${POD_URL}/pod/v1/${USER_A}/objects/immich/2026/nope.jpg`,
      {
        headers: { Authorization: `Bearer ${deviceToken}`, 'X-Pod-Source': 'immich' },
        data: Buffer.from('x'),
        failOnStatusCode: false,
      },
    );
    expect(asDevice.status()).toBe(403);
  });

  test('a device cannot reach another pod, and an unknown token cannot reach any', async ({ request }) => {
    const crossPod = await request.get(`${POD_URL}/pod/v1/${USER_B}/manifest`, {
      headers: { Authorization: `Bearer ${deviceToken}` }, failOnStatusCode: false,
    });
    expect(crossPod.status()).toBe(403);

    const anonymous = await request.get(`${POD_URL}/pod/v1/${USER_A}/manifest`, {
      failOnStatusCode: false,
    });
    expect(anonymous.status()).toBe(401);

    const bogus = await request.get(`${POD_URL}/pod/v1/${USER_A}/manifest`, {
      headers: { Authorization: 'Bearer not-a-real-token' }, failOnStatusCode: false,
    });
    expect(bogus.status()).toBe(401);
  });

  test('the manifest is a linked chain the device can follow', async ({ request }) => {
    const response = await request.get(`${POD_URL}/pod/v1/${USER_A}/manifest`, {
      headers: { Authorization: `Bearer ${deviceToken}` },
    });
    expect(response.status()).toBe(200);
    const { entries } = await response.json();
    expect(entries.length).toBeGreaterThan(0);

    expect(entries[0].prev_hash).toBe('0'.repeat(64));
    for (let i = 0; i < entries.length; i++) {
      expect(entries[i].seq).toBe(i + 1);
      expect(entries[i].entry_hash).toMatch(/^[0-9a-f]{64}$/);
      if (i > 0) expect(entries[i].prev_hash).toBe(entries[i - 1].entry_hash);
    }

    const appended = entries.find((e: any) => e.key === OBJECT_KEY);
    expect(appended, 'appended object is missing from the manifest').toBeTruthy();
    expect(appended.source).toBe('immich');
  });

  test('the real device agent pulls and verifies the archive', async () => {
    // Spawns python twice (sync, then a full re-verify), so it needs more than
    // the default per-test budget.
    test.setTimeout(180_000);
    const workDir = mkdtempSync(join(tmpdir(), 'pod-device-'));
    const configPath = join(workDir, 'config.json');
    writeFileSync(configPath, JSON.stringify({
      url: POD_URL, user_id: USER_A, token: deviceToken, name: 'e2e-device',
    }));

    // The agent verifies the hash chain and every digest itself; if either
    // fails it exits non-zero and execFileSync throws.
    const output = sh('python3', [
      'admin-tools/pod-device/pod-agent.py',
      '--config', configPath,
      '--data', workDir,
    ], { SSL_CERT_FILE: caBundle, REQUESTS_CA_BUNDLE: caBundle });

    expect(output).toContain('up to date at seq');

    const pulled = join(workDir, 'objects', OBJECT_KEY);
    expect(existsSync(pulled), `agent did not pull ${OBJECT_KEY}`).toBeTruthy();
    expect(readFileSync(pulled).equals(OBJECT_BODY)).toBeTruthy();

    // A second run is a no-op and must still verify cleanly.
    const again = sh('python3', [
      'admin-tools/pod-device/pod-agent.py',
      '--config', configPath, '--data', workDir, '--verify-only',
    ], { SSL_CERT_FILE: caBundle, REQUESTS_CA_BUNDLE: caBundle });
    expect(again).toContain('0 problem(s)');
  });

  test('the device heartbeat reaches the gateway metrics', async ({ request }) => {
    const metrics = await request.get(`${POD_URL}/metrics`);
    expect(metrics.status()).toBe(200);
    const body = await metrics.text();
    expect(body).toContain('pod_gateway_appends_total{source="immich"}');
    expect(body).toContain(`pod_gateway_device_heartbeat_age_seconds{device="e2e-device",user_id="${USER_A}"}`);
    // Nothing should have been stored without a manifest entry.
    expect(body).toMatch(/pod_gateway_orphan_objects 0/);
  });
});
