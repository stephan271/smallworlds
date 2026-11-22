import { test, expect } from '@playwright/test';
import https from 'node:https';

/**
 * The Operator Board (docs/adr/0068, design-operator-board.md §9), read the
 * way the Private Gateway reaches it: on the `operators` entrypoint
 * (traefik-internal:9443). Spec 11 covers which entrypoints answer at all;
 * this one checks what the Board says:
 *   - the page and its state document load;
 *   - every tile and the verdict have a colour, and nothing the Board read
 *     from a source that did not answer is shown green;
 *   - nothing is red: this slice reads no alerts, and only an alert may be red;
 *   - the link hub found Argo CD, Grafana, the Keycloak admin console and the
 *     Invitation Desk on their Ingresses, on this cluster's domain, and each
 *     answers on the same entrypoint;
 *   - it accepts nothing but reading.
 *
 * Runs only inside the cluster (OPERATOR_ENTRYPOINT_CHECK=1, set by Ring 0's
 * checks Job), like spec 11.
 */

const DOMAIN = process.env.DOMAIN!;
const IN_CLUSTER = process.env.OPERATOR_ENTRYPOINT_CHECK === '1';
const OPERATORS = { host: 'traefik-internal.traefik-system.svc.cluster.local', port: 9443 };
const BOARD = `ops.${DOMAIN}`;
const COLOURS = ['green', 'amber', 'red', 'grey'];

type Answer = { status: number; body: string };

/** A request with SNI and Host set to `host`, sent to the operators entrypoint. */
function request(host: string, path: string, method = 'GET'): Promise<Answer> {
  return new Promise((resolve, reject) => {
    const req = https.request(
      { host: OPERATORS.host, port: OPERATORS.port, path, method, servername: host, headers: { host },
        timeout: 15_000 },
      (response) => {
        let body = '';
        response.setEncoding('utf8');
        response.on('data', (chunk) => { body += chunk; });
        response.on('end', () => resolve({ status: response.statusCode ?? 0, body }));
      },
    );
    req.on('timeout', () => req.destroy(new Error(`timeout: ${host}${path}`)));
    req.on('error', reject);
    req.end();
  });
}

test.describe('operator board', () => {
  test.skip(!IN_CLUSTER, 'runs inside the cluster only (OPERATOR_ENTRYPOINT_CHECK=1)');

  test('the page loads', async () => {
    const page = await request(BOARD, '/');
    expect(page.status).toBe(200);
    expect(page.body).toContain('Operator Board');
    expect((await request(BOARD, '/static/board.js')).status).toBe(200);
  });

  test('the state document is coloured, honest and never red on its own', async () => {
    const answer = await request(BOARD, '/api/state');
    expect(answer.status).toBe(200);
    const state = JSON.parse(answer.body);
    expect(COLOURS).toContain(state.verdict.colour);
    expect(state.verdict.headline).toBeTruthy();
    expect(state.applications.length).toBeGreaterThan(5);
    for (const tile of state.tiles) expect(COLOURS, tile.id).toContain(tile.colour);
    for (const app of state.applications) expect(COLOURS, app.name).toContain(app.colour);
    // Unknown is never green.
    for (const [name, source] of Object.entries<any>(state.sources)) {
      if (!source.ok) {
        expect(state.verdict.colour, `${name} did not answer`).not.toBe('green');
        for (const tile of state.tiles) expect(tile.colour, `${tile.id} while ${name} failed`).not.toBe('green');
      }
    }
    // Red comes from Alertmanager only, which this Board does not read yet.
    expect(JSON.stringify(state)).not.toContain('"colour":"red"');
    expect(state.applications.map((a: any) => a.name)).toContain('operator-board');
  });

  test('the link hub found every operator tool on this domain, and each answers', async () => {
    const state = JSON.parse((await request(BOARD, '/api/state')).body);
    const byId = new Map<string, any>(state.links.filter((l: any) => l.id).map((l: any) => [l.id, l]));
    const expected: Record<string, [string, number[]]> = {
      argocd: [`deploy.${DOMAIN}`, [200]],
      grafana: [`monitoring.${DOMAIN}`, [200, 302]],
      keycloak: [`identity.${DOMAIN}`, [200, 302]],
      desk: [`join.${DOMAIN}`, [302]],
    };
    for (const [id, [host, statuses]] of Object.entries(expected)) {
      const link = byId.get(id);
      expect(link, `${id} is on the Board`).toBeTruthy();
      const url = new URL(link.url);
      expect(url.host, id).toBe(host);
      expect(statuses, `${link.url}`).toContain((await request(url.host, url.pathname)).status);
    }
    // Application cards link into Argo CD on this domain.
    const card = state.applications.find((a: any) => a.name === 'operator-board');
    expect(card.link).toBe(`https://deploy.${DOMAIN}/applications/argocd/operator-board`);
  });

  test('it accepts nothing but reading', async () => {
    for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) {
      expect((await request(BOARD, '/api/state', method)).status, method).toBe(405);
    }
  });
});
