import { expect, test } from '@playwright/test';

// The launcher spends its launch token on the first session it hands out, so
// this file talks to its own launcher process rather than the shared one.
test.use({ baseURL: 'http://127.0.0.1:4176' });

// The node inspection reports a data directory it did not create. Removing it
// is an unbacked rm -rf, so the console has to name every path before it runs
// and must not run on a single click.
test('Operator sees exactly what is in the way and what removing it would delete', async ({ page }) => {
  const dataDirectory = '/data/foreign-acceptance';
  let cleanRequests = 0;

  await page.route('**/api/v1/nodes/inspect', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        target: { kind: 'same-host' },
        report: { nodeIdentity: 'sha256:test-machine', operatingSystem: 'linux', architecture: 'amd64', systemd: true, capacity: { cpuCores: 8, memoryMi: 32768, diskGi: 500 }, ports: [], kernelReady: true, privilege: 'sudo', installation: { kubernetes: 'absent', smallworldsData: 'foreign', interrupted: false } },
        assessment: { ready: false, resumable: false, blockers: [{ code: 'installation.data.foreign' }] }
      })
    });
  });

  await page.route('**/api/v1/nodes/clean', async (route) => {
    cleanRequests++;
    await route.fulfill({ status: 204, body: '' });
  });

  await page.goto('/?token=foreign-install-token');
  await page.getByRole('button', { name: 'Set up another installation' }).click();
  const profile = page.getByRole('region', { name: 'Set up a new installation' });
  await profile.getByLabel('Name this installation').fill(`Foreign ${Date.now()}`);
  await profile.getByRole('button', { name: 'Create installation' }).click();

  await page.getByRole('navigation', { name: 'Setup progress' })
    .getByRole('button', { name: /Choose the computer that will run it/ })
    .click();
  const node = page.getByRole('region', { name: 'The computer that will run it' });
  await node.getByLabel('Target').selectOption('same-host');
  await node.getByLabel("Where your community's data is kept").fill(dataDirectory);
  await node.getByRole('button', { name: 'Check this computer' }).click();

  // The finding is stated in words, not as a bare code.
  await expect(node.getByText('The chosen data directory already exists and SmallWorlds did not create it.')).toBeVisible();

  const removal = page.getByRole('region', { name: /already installed/ });
  await expect(removal.getByText('Delete these directories with everything in them:')).toBeVisible();
  for (const path of ['/var/lib/rancher/k3s', '/etc/rancher', '/etc/smallworlds', dataDirectory]) {
    await expect(removal.getByText(path, { exact: true })).toBeVisible();
  }
  await expect(removal.getByText('This cannot be undone', { exact: false })).toBeVisible();
  // k3s is absent here, so the uninstaller line must not be promised.
  await expect(removal.getByText('Run the k3s uninstaller', { exact: false })).toHaveCount(0);

  // Declining the confirmation must leave the node untouched.
  page.once('dialog', (dialog) => {
    expect(dialog.message()).toContain(dataDirectory);
    void dialog.dismiss();
  });
  await removal.getByRole('button', { name: 'Remove what is in the way' }).click();
  await expect.poll(() => cleanRequests).toBe(0);

  page.once('dialog', (dialog) => void dialog.accept());
  await removal.getByRole('button', { name: 'Remove what is in the way' }).click();
  await expect.poll(() => cleanRequests).toBe(1);
});
