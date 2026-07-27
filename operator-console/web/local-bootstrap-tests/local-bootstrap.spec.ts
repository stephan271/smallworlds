import { expect, test } from '@playwright/test';

test('Operator plans a Linux-node bootstrap and observes interruption recovery', async ({ page }) => {
  const profileName = `Bootstrap ${Date.now()}`;
  let runReads = 0;

  await page.route('**/api/v1/nodes/inspect', async (route) => {
    expect(route.request().postDataJSON()).toMatchObject({ dataDirectory: '/data/smallworlds-acceptance' });
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
          preconditions: { profileRevision: 1, nodeIdentity: 'sha256:test-machine', inspectionDigest: 'b'.repeat(64), inspectedAt: new Date().toISOString(), bootstrapRelease: 'v1.2.27', overlayCommit: 'c'.repeat(40), dataDirectory: '/var/lib/smallworlds-data' },
          effects: [{ code: 'node.privileged.bootstrap', messageKey: 'plan.effect.local_bootstrap_privileged' }],
          risks: [{ code: 'node.services.may_restart', messageKey: 'plan.risk.local_bootstrap_downtime' }, { code: 'node.atomic_install', messageKey: 'plan.risk.local_bootstrap_cancellation' }], createdAt: new Date().toISOString()
        },
        inspection: { target: { kind: 'same-host' }, report: { nodeIdentity: 'sha256:test-machine', operatingSystem: 'linux', architecture: 'amd64', systemd: true, capacity: { cpuCores: 8, memoryMi: 32768, diskGi: 500 }, ports: [], kernelReady: true, privilege: 'sudo', installation: {} }, assessment: { ready: true, resumable: false, blockers: [] } }
      })
    });
  });

  // Installer files are fetched by the stage rather than asked for, so the
  // install stage will always have gone looking for them by the time the
  // operator reads the plan.
  const assets = {
    release: 'v1.2.27',
    offlineBundleAvailability: 'future',
    assets: [{ id: 'bootstrap-payload', release: 'v1.2.27', destination: '/tmp/bootstrap.tar.gz', sha256: 'd'.repeat(64), state: 'ready', bytes: 1024 }]
  };
  await page.route('**/api/v1/bootstrap-assets**', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(assets) });
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
  await page.getByRole('button', { name: 'Set up another installation' }).click();
  const profile = page.getByRole('region', { name: 'Set up a new installation' });
  await profile.getByLabel('Name this installation').fill(profileName);
  await profile.getByRole('button', { name: 'Create installation' }).click();

  // The web address is asked for once, in the stage about what the community
  // gets. Nothing later asks for it again, and the install reads it from there.
  const rail = page.getByRole('navigation', { name: 'Setup progress' });
  const capabilities = page.getByRole('region', { name: 'What the infrastructure will offer' });
  await capabilities.getByLabel('Your web address').fill('home.example');

  // Each stage is reached through the journey rail rather than by scrolling.
  await rail.getByRole('button', { name: /Choose the computer that will run it/ }).click();
  const node = page.getByRole('region', { name: 'The computer that will run it' });
  await node.getByLabel('Target').selectOption('same-host');
  await node.getByLabel("Where your community's data is kept").fill('/data/smallworlds-acceptance');
  await node.getByRole('button', { name: 'Check this computer' }).click();
  await expect(node.getByText('Suitable — ready to continue')).toBeVisible();

  await rail.getByRole('button', { name: /Install it/ }).click();
  const bootstrap = page.getByRole('region', { name: 'Install it' });
  await expect(bootstrap.getByText('Installer files for v1.2.27 downloaded and checked.')).toBeVisible();

  // Without Cluster Secrets the install would finish and then never start, so
  // the stage will not plan one. The field is on the form itself rather than
  // behind Advanced: there is no sensible default for it to hide.
  const createPlan = bootstrap.getByRole('button', { name: 'Reinspect and create Change Plan' });
  await expect(createPlan).toBeDisabled();
  await expect(bootstrap.getByText('Without these the installation finishes and then never starts', { exact: false })).toBeVisible();

  await bootstrap.getByLabel('Kubernetes Secret manifests (kept outside Git)').fill('apiVersion: v1\nkind: Secret\ndata:\n  token: browser-only-secret');
  await expect(createPlan).toBeEnabled();
  await createPlan.click();
  await expect(page.getByTestId('plan-digest')).toHaveText('a'.repeat(64));
  await expect(page.getByText('k3s and workloads can restart', { exact: false })).toBeVisible();
  await expect(page.getByText('/var/lib/smallworlds-data', { exact: true })).toBeVisible();
  await expect(page.getByText('browser-only-secret')).toHaveCount(0);

  await page.getByRole('button', { name: 'Approve and start' }).click();
  // The sidebar carries the run state for the installation as a whole.
  await expect(page.getByRole('status')).toContainText('interrupted');
  await expect(page.getByRole('status')).toContainText('Verified');
  expect(runReads).toBeGreaterThan(1);
});
