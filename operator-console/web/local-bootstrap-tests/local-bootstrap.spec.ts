import { expect, test } from '@playwright/test';

test('Operator plans a Linux-node bootstrap and observes interruption recovery', async ({ page }) => {
  const profileName = `Bootstrap ${Date.now()}`;
  let runReads = 0;

  await page.route('**/api/v1/nodes/inspect', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        target: { kind: 'same-host' },
        report: { nodeIdentity: 'sha256:test-machine', operatingSystem: 'linux', architecture: 'amd64', systemd: true, capacity: { cpuCores: 8, memoryMi: 32768, diskGi: 500 }, ports: [], kernelReady: true, privilege: 'sudo', installation: { kubernetes: 'absent', smallworldsData: 'absent', interrupted: false } },
        assessment: { ready: true, resumable: false, blockers: [] }
      })
    });
  });

  await page.route('**/api/v1/local-bootstrap/plan', async (route) => {
    const input = route.request().postDataJSON() as { secretsManifest: string; configuration: { domain: string } };
    expect(input.secretsManifest).toContain('browser-only-secret');
    expect(input.configuration.domain).toBe('home.example');
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        plan: {
          id: 'bootstrap-plan', profileId: 'browser-profile', intent: 'BootstrapLocalNode', digest: 'a'.repeat(64), status: 'planned',
          preconditions: { profileRevision: 1, nodeIdentity: 'sha256:test-machine', inspectionDigest: 'b'.repeat(64), inspectedAt: new Date().toISOString(), bootstrapRelease: 'v1.2.26', overlayCommit: 'c'.repeat(40), dataDirectory: '/var/lib/smallworlds-data' },
          effects: [{ code: 'node.privileged.bootstrap', messageKey: 'plan.effect.local_bootstrap_privileged' }],
          risks: [{ code: 'node.services.may_restart', messageKey: 'plan.risk.local_bootstrap_downtime' }, { code: 'node.atomic_install', messageKey: 'plan.risk.local_bootstrap_cancellation' }], createdAt: new Date().toISOString()
        },
        inspection: { target: { kind: 'same-host' }, report: { nodeIdentity: 'sha256:test-machine', operatingSystem: 'linux', architecture: 'amd64', systemd: true, capacity: { cpuCores: 8, memoryMi: 32768, diskGi: 500 }, ports: [], kernelReady: true, privilege: 'sudo', installation: {} }, assessment: { ready: true, resumable: false, blockers: [] } }
      })
    });
  });

  await page.route('**/api/v1/plans/bootstrap-plan/approve', async (route) => {
    await route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify({ id: 'bootstrap-run', planId: 'bootstrap-plan', profileId: 'browser-profile', state: 'running', currentCheckpoint: 'approved', cancellationState: 'not-requested', verification: {}, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }) });
  });

  await page.route('**/api/v1/runs/bootstrap-run', async (route) => {
    runReads++;
    // Keep the interrupted checkpoint visible across several browser polls so
    // the UI must render recovery progress rather than coalescing both states.
    const recovered = runReads > 4;
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ id: 'bootstrap-run', planId: 'bootstrap-plan', profileId: 'browser-profile', state: recovered ? 'verified' : 'running', currentCheckpoint: recovered ? 'verification-complete' : 'interrupted', cancellationState: 'not-requested', verification: recovered ? { code: 'cluster.gitops.converged', observedAt: new Date().toISOString() } : {}, createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }) });
  });

  await page.goto('/?token=local-bootstrap-token');
  await page.getByRole('button', { name: 'Create another profile' }).click();
  const profile = page.getByRole('region', { name: 'Create Cluster Profile' });
  await profile.getByLabel('Profile name').fill(profileName);
  await profile.getByRole('button', { name: 'Create profile' }).click();

  const node = page.getByRole('region', { name: 'Inspect Local Cluster Node' });
  await node.getByLabel('Target').selectOption('same-host');
  await node.getByRole('button', { name: 'Inspect node' }).click();
  await expect(node.getByText('Ready to plan')).toBeVisible();

  const bootstrap = node.getByRole('region', { name: 'Bootstrap Kubernetes and GitOps' });
  await bootstrap.getByLabel('Base domain').fill('home.example');
  await bootstrap.getByLabel('Kubernetes Secret manifests (kept outside Git)').fill('apiVersion: v1\nkind: Secret\ndata:\n  token: browser-only-secret');
  await bootstrap.getByRole('button', { name: 'Reinspect and create Change Plan' }).click();
  await expect(page.getByTestId('plan-digest')).toHaveText('a'.repeat(64));
  await expect(page.getByText('k3s and workloads can restart', { exact: false })).toBeVisible();
  await expect(page.getByText('/var/lib/smallworlds-data', { exact: true })).toBeVisible();
  await expect(page.getByText('browser-only-secret')).toHaveCount(0);

  await page.getByRole('button', { name: 'Approve and run' }).click();
  await expect(page.getByRole('status')).toContainText('interrupted');
  await expect(page.getByRole('status')).toContainText('Verified');
  expect(runReads).toBeGreaterThan(1);
});
