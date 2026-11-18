import { test, expect } from '@playwright/test';
import https from 'node:https';

/**
 * Operator interfaces are separated by Traefik entrypoint, not by source
 * address (docs/adr/0066, doc/hardening.md §1).
 *
 * From inside the cluster a request carries a cluster source address, which
 * the old allow-lists admitted — the gap the `operators` entrypoint closes. So
 * from here, each operator page must:
 *   - refuse the public entrypoint (403): the allow-list now admits the node's
 *     LAN only, not cluster or tailnet sources;
 *   - answer on the `operators` entrypoint (traefik-internal:9443), which is
 *     what the Private Gateway forwards the Private Network to;
 *   - not answer on the `members` entrypoint (traefik-internal:9444), which is
 *     what the Member Gateway forwards members' devices to. That entrypoint
 *     exists in both modes, so this runs whether the Member Network is on or not.
 *
 * Runs only inside the cluster (OPERATOR_ENTRYPOINT_CHECK=1, set by Ring 0's
 * checks Job): from an operator's own machine on the LAN or the tailnet the
 * first half is supposed to fail, and the internal Service is not reachable.
 */

const DOMAIN = process.env.DOMAIN!;
const IN_CLUSTER = process.env.OPERATOR_ENTRYPOINT_CHECK === '1';
const OPERATORS = { host: 'traefik-internal.traefik-system.svc.cluster.local', port: 9443 };
const MEMBERS = { host: 'traefik-internal.traefik-system.svc.cluster.local', port: 9444 };

const PAGES: { host: string; path: string; private: number[] }[] = [
  { host: `deploy.${DOMAIN}`, path: '/', private: [200] },
  { host: `monitoring.${DOMAIN}`, path: '/login', private: [200] },
  // The admin console's entry redirects to the master realm's console.
  { host: `identity.${DOMAIN}`, path: '/admin/', private: [200, 302] },
  // Without a session the Desk sends you to its sign-in, never the Desk itself.
  { host: `join.${DOMAIN}`, path: '/desk/', private: [302] },
];

/** GET with SNI and Host set to `host`, optionally connecting somewhere else. */
function status(host: string, path: string, connect?: { host: string; port: number }): Promise<number> {
  return new Promise((resolve, reject) => {
    const request = https.request(
      {
        host: connect?.host ?? host,
        port: connect?.port ?? 443,
        path,
        servername: host,
        headers: { host },
        timeout: 15_000,
      },
      (response) => {
        response.resume();
        resolve(response.statusCode ?? 0);
      },
    );
    request.on('timeout', () => request.destroy(new Error(`timeout: ${host}${path}`)));
    request.on('error', reject);
    request.end();
  });
}

test.describe('operator entrypoint', () => {
  test.skip(!IN_CLUSTER, 'runs inside the cluster only (OPERATOR_ENTRYPOINT_CHECK=1)');

  for (const page of PAGES) {
    test(`${page.host}${page.path} refuses the public entrypoint`, async () => {
      expect(await status(page.host, page.path)).toBe(403);
    });

    test(`${page.host}${page.path} answers on the operators entrypoint`, async () => {
      expect(page.private).toContain(await status(page.host, page.path, OPERATORS));
    });

    // 404 where no router exists for the host or path (Argo CD, Grafana, the
    // Desk, which is on a port of its own); 403 for the Keycloak admin paths,
    // whose LAN-only router names `members` so that they cannot fall through
    // to the realm's public route.
    test(`${page.host}${page.path} does not answer on the members entrypoint`, async () => {
      expect([403, 404]).toContain(await status(page.host, page.path, MEMBERS));
    });
  }
});
