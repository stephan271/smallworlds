import { expect, request, test } from '@playwright/test';

function required(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required for the real-router stable-release test`);
  return value;
}

test('records real-router public and private exposure evidence', async ({ request: trustedRequest }, testInfo) => {
  const publicDomain = required('SMALLWORLDS_PUBLIC_DOMAIN');
  const publicIPv4 = required('SMALLWORLDS_PUBLIC_IPV4');
  const privateConsoleURL = required('SMALLWORLDS_PRIVATE_CONSOLE_URL');
  const memberHosts = (process.env.SMALLWORLDS_PUBLIC_MEMBER_HOSTS ?? `identity.${publicDomain},dashboard.${publicDomain}`)
    .split(',').map((host) => host.trim()).filter(Boolean);
  const observedAt = new Date().toISOString();
  const observations: Record<string, unknown> = {
    release: required('SMALLWORLDS_RELEASE'),
    observedAt,
    publicDomain,
    publicIPv4,
    routerRulesAcknowledged: ['80/tcp', '443/tcp', '10000/udp'],
    memberHosts,
    privateConsoleURL
  };

  for (const host of memberHosts) {
    const response = await trustedRequest.get(`https://${host}/`, { maxRedirects: 0 });
    expect(response.status(), `${host} must be publicly reachable with a trusted certificate`).toBeLessThan(500);
  }

  const headscale = await trustedRequest.get(`https://vpn.${publicDomain}/`, { maxRedirects: 0 });
  expect(headscale.status(), 'public Headscale coordination must have a route').not.toBe(404);

  const privateConsole = await trustedRequest.get(privateConsoleURL, { maxRedirects: 0 });
  expect(privateConsole.status(), 'the enrolled Launcher Host must reach the private console').toBeLessThan(500);

  const forgedPublic = await request.newContext({
    baseURL: `https://${publicIPv4}`,
    ignoreHTTPSErrors: true,
    extraHTTPHeaders: { Host: new URL(privateConsoleURL).host }
  });
  try {
    const response = await forgedPublic.get('/', { maxRedirects: 0 });
    expect([401, 403, 404, 421], 'a forged operator Host header must not reach public ingress').toContain(response.status());
    observations.forgedOperatorHostStatus = response.status();
  } finally {
    await forgedPublic.dispose();
  }

  await testInfo.attach('local-public-stable-release-evidence.json', {
    body: Buffer.from(JSON.stringify(observations, null, 2)),
    contentType: 'application/json'
  });
});
