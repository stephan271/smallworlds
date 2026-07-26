import { execFileSync, spawn, type ChildProcess } from 'node:child_process';
import { readlinkSync, realpathSync } from 'node:fs';
import { expect, test } from '@playwright/test';

function required(name: string): string {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required for the destructive real-node acceptance test`);
  return value;
}

function requiredPID(name: string): number {
  const value = Number(required(name));
  if (!Number.isSafeInteger(value) || value <= 1) throw new Error(`${name} must identify one Launcher process`);
  return value;
}

function sshArguments(target: string, command: string): string[] {
  return [
    '-o', 'BatchMode=yes',
    '-o', 'ConnectTimeout=10',
    '-o', 'StrictHostKeyChecking=yes',
    target,
    command
  ];
}

function waitForChild(child: ChildProcess, timeoutMs: number): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      child.kill('SIGTERM');
      reject(new Error('timed out waiting for the remote durable bootstrap marker'));
    }, timeoutMs);
    child.once('error', (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.once('exit', (code) => {
      clearTimeout(timer);
      code === 0 ? resolve() : reject(new Error(`remote marker watcher exited with ${code}`));
    });
  });
}

test('browser bootstrap survives a Launcher interruption after a durable node marker', async ({ page }) => {
  const baseURL = required('SMALLWORLDS_ACCEPTANCE_URL');
  const launchToken = required('SMALLWORLDS_ACCEPTANCE_LAUNCH_TOKEN');
  const launcherBinary = required('SMALLWORLDS_ACCEPTANCE_LAUNCHER_BINARY');
  const launcherDataDirectory = required('SMALLWORLDS_ACCEPTANCE_LAUNCHER_DATA_DIR');
  const sshTarget = required('SMALLWORLDS_ACCEPTANCE_SSH_TARGET');
  const sshHost = required('SMALLWORLDS_ACCEPTANCE_SSH_HOST');
  const sshUser = required('SMALLWORLDS_ACCEPTANCE_SSH_USER');
  const expectedFingerprint = required('SMALLWORLDS_ACCEPTANCE_SSH_FINGERPRINT');
  const repositoryURL = required('SMALLWORLDS_ACCEPTANCE_REPOSITORY_URL');
  const gitUsername = required('SMALLWORLDS_ACCEPTANCE_GIT_USERNAME');
  const gitToken = required('SMALLWORLDS_ACCEPTANCE_GIT_TOKEN');
  const domain = required('SMALLWORLDS_ACCEPTANCE_DOMAIN');
  const dataDirectory = required('SMALLWORLDS_ACCEPTANCE_NODE_DATA_DIR');
  const vaultPassphrase = required('SMALLWORLDS_ACCEPTANCE_VAULT_PASSPHRASE');
  const keycloakPassword = required('SMALLWORLDS_ACCEPTANCE_KEYCLOAK_PASSWORD');
  const inviteSecret = required('SMALLWORLDS_ACCEPTANCE_INVITE_SECRET');
  const garageRPCSecret = required('SMALLWORLDS_ACCEPTANCE_GARAGE_RPC_SECRET');
  const garageAdminToken = required('SMALLWORLDS_ACCEPTANCE_GARAGE_ADMIN_TOKEN');
  const grafanaPassword = required('SMALLWORLDS_ACCEPTANCE_GRAFANA_PASSWORD');
  const resumeSetup = process.env.SMALLWORLDS_ACCEPTANCE_RESUME_SETUP === '1';
  const cancelRunID = process.env.SMALLWORLDS_ACCEPTANCE_CANCEL_RUN_ID;
  const recoverRunID = process.env.SMALLWORLDS_ACCEPTANCE_RECOVER_RUN_ID;
  const convergenceTimeout = Number(process.env.SMALLWORLDS_ACCEPTANCE_CONVERGENCE_TIMEOUT_MS ?? 60 * 60_000);
  if (!Number.isSafeInteger(convergenceTimeout) || convergenceTimeout < 60_000) {
    throw new Error('SMALLWORLDS_ACCEPTANCE_CONVERGENCE_TIMEOUT_MS must be at least 60000');
  }
  const port = new URL(baseURL).port;
  let restartedLauncher: ChildProcess | undefined;

  try {
    await page.goto(`${baseURL}/?token=${encodeURIComponent(launchToken)}`);
    await expect(page.getByRole('heading', { name: 'SmallWorlds Operator Console' })).toBeVisible();

    if (cancelRunID || recoverRunID) {
      const selectedRunID = cancelRunID ?? recoverRunID;
      await page.evaluate(async (runID) => {
        const response = await fetch('/api/v1/profiles');
        const profiles = await response.json() as Array<{ id: string }>;
        if (!profiles[0]) throw new Error('acceptance profile is missing');
        window.localStorage.setItem('smallworlds.activeProfile', profiles[0].id);
        window.localStorage.setItem(`smallworlds.run.${profiles[0].id}`, runID);
      }, selectedRunID);
      await page.reload();
      await expect(page.getByRole('status')).toContainText('Running');
      const cancellationVault = page.getByRole('region', { name: 'Password safe (Launcher Vault)' });
      await cancellationVault.getByLabel('Safe passphrase').fill(vaultPassphrase);
      await cancellationVault.getByRole('button', { name: 'Open the safe' }).click();
      if (recoverRunID) {
        await expect(cancellationVault.getByText('Unlocked', { exact: true })).toBeVisible();
        await expect(page.getByRole('status')).toContainText('Verified', { timeout: convergenceTimeout });
        await expect(page.getByRole('status')).toContainText('verification-complete');
        const recoveredMarkers = execFileSync('ssh', sshArguments(sshTarget, "sudo -n find /etc/smallworlds -maxdepth 1 -type f -printf '%f\\n' | sort"), { encoding: 'utf8' });
        for (const marker of ['bootstrap-complete', 'k3s-ready', 'argocd-ready', 'overlay-applied']) {
          expect(recoveredMarkers).toContain(marker);
        }
        expect(recoveredMarkers).not.toContain('bootstrap-interrupted');
        return;
      }
      await page.getByRole('button', { name: 'Cancel' }).click();
      await expect(page.getByRole('status')).toContainText('Cancelled', { timeout: 60_000 });
      return;
    }

    const vault = page.getByRole('region', { name: 'Password safe (Launcher Vault)' });
    // The journey is staged, so each stage is opened from the rail. Looking
    // ahead is allowed; the stage itself says what is still missing.
    const rail = page.getByRole('navigation', { name: 'Setup progress' });
    if (!resumeSetup) {
      const profile = page.getByRole('region', { name: 'Set up a new installation' });
      await profile.getByLabel('Name this installation').fill(`Disposable acceptance ${Date.now()}`);
      await profile.getByLabel('Where it runs').selectOption('local-lan');
      await profile.getByRole('button', { name: 'Create installation' }).click();

      await vault.getByLabel('Safe passphrase').fill(vaultPassphrase);
      await vault.getByRole('button', { name: 'Open the safe' }).click();
      await expect(vault.getByText('Unlocked', { exact: true })).toBeVisible();

      const capabilities = page.getByRole('region', { name: 'What the infrastructure will offer' });
      await capabilities.getByLabel('How much to install').selectOption('minimal');
      await capabilities.getByLabel('SmallWorlds version').fill('v1.2.27');
      await capabilities.getByLabel('Your private settings repository').fill(repositoryURL);
      await capabilities.getByLabel('Your web address').fill(domain);
      const capabilityPlanResponse = page.waitForResponse('**/api/v1/capabilities/plan');
      await capabilities.getByRole('button', { name: 'Show me the exact changes' }).click();
      expect((await capabilityPlanResponse).status()).toBe(201);
      await expect(capabilities.getByTestId('overlay-diff')).toContainText('v1.2.27');

      const capabilityApproval = page.waitForResponse((response) => response.url().includes('/api/v1/plans/') && response.url().endsWith('/approve'));
      await page.getByRole('button', { name: 'Approve and start' }).click();
      expect((await capabilityApproval).status()).toBe(202);
      await expect(page.getByRole('status')).toContainText('Verified');

      // Both Git providers now live behind one explicit choice on the settings
      // stage, rather than two cards that looked like separate obligations.
      await rail.getByRole('button', { name: /Choose where your settings are kept/ }).click();
      const settingsRepo = page.getByRole('region', { name: 'Choose where your settings are kept' });
      await settingsRepo.getByLabel('Use a Git repository I already have').check();
      await settingsRepo.getByLabel('Git username').fill(gitUsername);
      await settingsRepo.getByLabel('Git access token').fill(gitToken);
      const validationResponse = page.waitForResponse('**/api/v1/generic-git/token/validate');
      await settingsRepo.getByRole('button', { name: 'Validate and store access' }).click();
      expect((await validationResponse).status()).toBe(200);
      await expect(settingsRepo.getByText(repositoryURL, { exact: true })).toBeVisible();
      const establishResponse = page.waitForResponse('**/api/v1/generic-git/overlay/establish');
      await settingsRepo.getByRole('button', { name: 'Initialize HTTPS Git overlay' }).click();
      expect((await establishResponse).status()).toBe(201);
      await expect(settingsRepo).toContainText(`${repositoryURL} @`);
    } else {
      await expect(vault).toBeVisible();
      if (await vault.getByText('Locked', { exact: true }).isVisible()) {
        await vault.getByLabel('Safe passphrase').fill(vaultPassphrase);
        await vault.getByRole('button', { name: 'Open the safe' }).click();
      }
      await expect(vault.getByText('Unlocked', { exact: true })).toBeVisible();
    }

    await rail.getByRole('button', { name: /Download the installer files/ }).click();
    const assets = page.getByRole('region', { name: 'Installer files' });
    await assets.getByLabel('SmallWorlds version').fill('v1.2.27');
    const inspectAssetsResponse = page.waitForResponse((response) => response.url().includes('/api/v1/bootstrap-assets?'));
    await assets.getByRole('button', { name: 'Show which files are needed' }).click();
    expect((await inspectAssetsResponse).status()).toBe(200);
    const acquireAssets = assets.getByRole('button', { name: 'Download and check the files' });
    if (await acquireAssets.isEnabled()) {
      const acquireAssetsResponse = page.waitForResponse('**/api/v1/bootstrap-assets/acquire', { timeout: 180_000 });
      await acquireAssets.click();
      expect((await acquireAssetsResponse).status()).toBe(201);
    }
    await expect(assets).toContainText('ready');

    await rail.getByRole('button', { name: /Choose the computer that will run it/ }).click();
    const node = page.getByRole('region', { name: 'The computer that will run it' });
    await node.getByLabel('Target').selectOption('remote');
    await node.getByLabel("Where your community's data is kept").fill(dataDirectory);
    await node.getByLabel('Its name or address on the network').fill(sshHost);
    await node.getByLabel('SSH username').fill(sshUser);
    await node.getByLabel('SSH authentication').selectOption('agent');
    const probeResponse = page.waitForResponse('**/api/v1/nodes/probe');
    await node.getByRole('button', { name: 'Show SSH fingerprint' }).click();
    expect((await probeResponse).status()).toBe(200);
    await expect(node.getByText(expectedFingerprint, { exact: true })).toBeVisible();
    const trustResponse = page.waitForResponse('**/api/v1/nodes/trust');
    await node.getByRole('button', { name: 'Confirm and trust' }).click();
    expect((await trustResponse).status()).toBe(201);
    const inspectNodeResponse = page.waitForResponse('**/api/v1/nodes/inspect');
    await node.getByRole('button', { name: 'Check this computer' }).click();
    expect((await inspectNodeResponse).status()).toBe(200);
    await expect(node.getByText('Suitable — ready to continue')).toBeVisible();

    const bootstrap = node.getByRole('region', { name: 'Install onto this computer' });
    await bootstrap.getByLabel('Your web address').fill(domain);
    await bootstrap.getByLabel('Kubernetes node name').fill('smallworlds-acceptance');
    const clusterSecrets = [
      'apiVersion: v1',
      'kind: Secret',
      'metadata:',
      '  name: keycloak-admin-creds',
      '  namespace: keycloak',
      'type: Opaque',
      'stringData:',
      `  admin-password: ${JSON.stringify(keycloakPassword)}`,
      `  bulk-invite-secret: ${JSON.stringify(inviteSecret)}`,
      '---',
      'apiVersion: v1',
      'kind: Secret',
      'metadata:',
      '  name: smallworlds-acceptance-repository',
      '  namespace: argocd',
      '  labels:',
      '    argocd.argoproj.io/secret-type: repository',
      'stringData:',
      '  type: git',
      `  url: ${JSON.stringify(repositoryURL)}`,
      `  username: ${JSON.stringify(gitUsername)}`,
      `  password: ${JSON.stringify(gitToken)}`,
      '---',
      'apiVersion: v1',
      'kind: Secret',
      'metadata:',
      '  name: grafana-admin-creds',
      '  namespace: monitoring',
      'type: Opaque',
      'stringData:',
      '  admin-user: "admin"',
      `  admin-password: ${JSON.stringify(grafanaPassword)}`,
      '---',
      'apiVersion: v1',
      'kind: Secret',
      'metadata:',
      '  name: garage-auth-secret',
      '  namespace: garage-system',
      'type: Opaque',
      'stringData:',
      `  rpcSecret: ${JSON.stringify(garageRPCSecret)}`,
      `  adminToken: ${JSON.stringify(garageAdminToken)}`
    ].join('\n');
    await bootstrap.getByLabel('Kubernetes Secret manifests (kept outside Git)').fill(clusterSecrets);
    const bootstrapPlanResponse = page.waitForResponse('**/api/v1/local-bootstrap/plan', { timeout: 120_000 });
    await bootstrap.getByRole('button', { name: 'Reinspect and create Change Plan' }).click();
    expect((await bootstrapPlanResponse).status()).toBe(201);
    await expect(page.getByText(dataDirectory, { exact: true })).toBeVisible();

    const markerWatcher = spawn('ssh', sshArguments(sshTarget, "while ! sudo -n test -f /etc/smallworlds/bootstrap-started; do sleep 0.05; done"), { stdio: 'ignore' });
    const markerObserved = waitForChild(markerWatcher, 10 * 60_000);
    const bootstrapApproval = page.waitForResponse((response) => response.url().includes('/api/v1/plans/') && response.url().endsWith('/approve'));
    await page.getByRole('button', { name: 'Approve and start' }).click();
    expect((await bootstrapApproval).status()).toBe(202);
    await expect(page.getByRole('status')).toContainText('bootstrap-atomic-operation', { timeout: 120_000 });
    await markerObserved;

    const initialLauncherPID = requiredPID('SMALLWORLDS_ACCEPTANCE_LAUNCHER_PID');
    const expectedLauncherExecutable = realpathSync(launcherBinary);
    const actualLauncherExecutable = readlinkSync(`/proc/${initialLauncherPID}/exe`).replace(/ \(deleted\)$/, '');
    expect(actualLauncherExecutable).toBe(expectedLauncherExecutable);
    process.kill(initialLauncherPID, 'SIGTERM');
    await expect.poll(() => {
      try {
        process.kill(initialLauncherPID, 0);
        return false;
      } catch {
        return true;
      }
    }, { timeout: 15_000 }).toBe(true);

    const interruptedMarkers = execFileSync('ssh', sshArguments(sshTarget, "sudo -n test -f /etc/smallworlds/bootstrap-started && sudo -n find /etc/smallworlds -maxdepth 1 -type f -printf '%f\\n' | sort"), { encoding: 'utf8' });
    expect(interruptedMarkers).toContain('bootstrap-started');
    expect(interruptedMarkers).not.toContain('bootstrap-complete');

    restartedLauncher = spawn(launcherBinary, [
      '--port', port,
      '--data-dir', launcherDataDirectory,
      '--token', launchToken,
      '--no-browser'
    ], { env: process.env, stdio: 'ignore' });
    await expect.poll(async () => {
      try {
        return (await fetch(baseURL)).status === 200;
      } catch {
        return false;
      }
    }, { timeout: 30_000 }).toBe(true);

    await page.goto(`${baseURL}/?token=${encodeURIComponent(launchToken)}`);
    await expect(page.getByRole('status')).toContainText(/waiting-for-vault|Running/);
    const restartedVault = page.getByRole('region', { name: 'Password safe (Launcher Vault)' });
    await restartedVault.getByLabel('Safe passphrase').fill(vaultPassphrase);
    await restartedVault.getByRole('button', { name: 'Open the safe' }).click();
    await expect(restartedVault.getByText('Unlocked', { exact: true })).toBeVisible();
    await expect(page.getByRole('status')).toContainText('Verified', { timeout: convergenceTimeout });
    await expect(page.getByRole('status')).toContainText('verification-complete');

    const finalMarkers = execFileSync('ssh', sshArguments(sshTarget, "sudo -n find /etc/smallworlds -maxdepth 1 -type f -printf '%f\\n' | sort"), { encoding: 'utf8' });
    for (const marker of ['bootstrap-complete', 'k3s-ready', 'argocd-ready', 'overlay-applied']) {
      expect(finalMarkers).toContain(marker);
    }
    expect(finalMarkers).not.toContain('bootstrap-interrupted');
  } finally {
    restartedLauncher?.kill('SIGTERM');
  }
});
