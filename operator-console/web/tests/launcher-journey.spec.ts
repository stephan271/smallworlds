import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';

test('Operator completes and reopens the launcher journey in English and German', async ({ page }) => {
  await page.goto('/?token=e2e-token');
  await expect(page).toHaveURL('/');
  await expect(page.getByRole('heading', { name: 'SmallWorlds Operator Console' })).toBeVisible();

  const journeys = [
    {
      language: 'en',
      profileName: `Workshop ${Date.now()}`,
      next: 'Next recommended action',
      task: 'Check this console works',
      plan: 'See what will happen',
      approve: 'Approve and start',
      verified: 'Verified'
    },
    {
      language: 'de',
      profileName: `Werkstatt ${Date.now()}`,
      next: 'Nächste empfohlene Aktion',
      task: 'Prüfen, ob diese Konsole funktioniert',
      plan: 'Zeigen, was passieren wird',
      approve: 'Genehmigen und starten',
      verified: 'Verifiziert'
    }
  ] as const;

  for (const journey of journeys) {
    await page.getByRole('button', { name: /set up another installation|weitere installation einrichten/i }).click();
    const profileForm = page.getByRole('region', { name: /set up a new installation|neue installation einrichten/i });
    await profileForm.getByLabel(/name this installation|name dieser installation/i).fill(journey.profileName);
    await profileForm.getByLabel(/language|sprache/i).selectOption(journey.language);
    await profileForm.getByLabel(/where it runs|wo es läuft/i).selectOption('local-lan');
    const createButton = profileForm.getByRole('button', { name: /create installation|installation anlegen/i });
    await createButton.focus();
    await page.keyboard.press('Enter');

    await expect(page.getByRole('heading', { name: journey.profileName })).toBeVisible();
    await expect(page.getByText(journey.next)).toBeVisible();
    await expect(page.getByRole('heading', { name: journey.task })).toBeVisible();

    if (journey.language === 'en') {
      const capabilities = page.getByRole('region', { name: 'What the infrastructure will offer' });
      await capabilities.getByLabel('SmallWorlds version').fill('v1.2.3');
      await capabilities.getByLabel('Your private settings repository').fill('https://github.com/example/private-overlay.git');
      await capabilities.getByLabel('Your web address').fill('home.example');
      const capabilityResponse = page.waitForResponse('/api/v1/capabilities/plan');
      await capabilities.getByRole('button', { name: 'Show me the exact changes' }).click();
      expect((await capabilityResponse).status()).toBe(201);
      await expect(capabilities.getByTestId('overlay-diff')).toContainText('v1.2.3');
      // A secret's value must never appear in the diff. Its NAME may: the overlay
      // points Grafana at an existing Secret and names the one cert-manager writes
      // a certificate into. So assert on a key carrying a value, not on the word.
      // Anchored to the start of a line so existingSecret: and secretName: are not
      // mistaken for a key of their own.
      await expect(capabilities.getByTestId('overlay-diff')).not.toContainText(/(^|\n)[\t -]*(password|token|secret)[\t ]*:[\t ]*\S/i);
    }

		const vault = page.getByRole('region', { name: /password safe|passwort-tresor/i });
		await expect(vault).toBeVisible();
		if (journey.language === 'en') {
			// The passphrase is the fallback: it only appears once the operator asks
			// for it, so it does not compete with the computer's own login.
			await expect(vault.getByText('Or use a passphrase')).toHaveCount(0);
			await vault.getByRole('button', { name: 'Unlock another way' }).click();
			await expect(vault.getByText('Or use a passphrase')).toBeVisible();
			await vault.getByLabel('Safe passphrase').fill('playwright-vault-passphrase');
			await vault.getByRole('button', { name: 'Open the safe' }).focus();
			await page.keyboard.press('Enter');
			await expect(vault.getByText('Unlocked', { exact: true })).toBeVisible();
			// The safe reports what it holds and asks for nothing: every secret is
			// collected by the step that needs it, so there is no field here that
			// the operator has to guess the purpose of.
			await expect(vault.getByText('Nothing has to be entered here', { exact: false })).toBeVisible();
			await expect(vault.locator('input')).toHaveCount(0);
			// The recovery bundle lives in the sidebar, not in the journey, so it
			// cannot be stumbled into; opening it takes over the panel and closing
			// it returns the operator to the step they were on.
			await page.getByRole('button', { name: /^Recovery Bundle\.\.\./ }).click();
			const recovery = page.getByRole('region', { name: 'Recovery Bundle' });
			await expect(recovery).toBeVisible();
			await expect(page.getByRole('navigation', { name: 'Setup progress' })).toHaveCount(0);
			await recovery.getByLabel('Recovery passphrase').first().fill('playwright-recovery-passphrase');
			const download = page.waitForEvent('download');
			await recovery.getByRole('button', { name: 'Download encrypted bundle' }).click();
			await expect((await download).suggestedFilename()).toMatch(/-recovery\.bundle$/);
			await recovery.getByRole('button', { name: 'Cancel' }).click();
			await expect(page.getByRole('navigation', { name: 'Setup progress' })).toBeVisible();
		} else {
			await expect(vault.getByText('Entsperrt', { exact: true })).toBeVisible();
			await expect(vault.getByText('Kein Zugangsschlüssel gespeichert')).toBeVisible();
			await expect(vault.locator('input')).toHaveCount(0);
		}

    if (journey.language === 'en') {
      // The journey is staged now, so offsite protection lives on its own step.
      // Reaching it ahead of its prerequisites is allowed — the step says what
      // is still missing rather than refusing to open.
      await page.getByRole('navigation', { name: 'Setup progress' })
        .getByRole('button', { name: /Protect against losing the machine/ })
        .click();
      const offsite = page.getByRole('region', { name: 'Backup copy somewhere else' });
      await offsite.getByLabel('Storage address (S3 endpoint)').fill('https://s3.eu-central-003.backblazeb2.com');
      await offsite.getByLabel('Region').fill('eu-central-003');
      await offsite.getByLabel('Storage name (bucket)').fill('community-backups');
      await offsite.getByLabel('Access key ID').fill('e2e-offsite-access-key');
      const offsiteSecret = 'e2e-offsite-secret-must-not-render';
      await offsite.getByLabel('Secret access key').fill(offsiteSecret);
      const inspectResponse = page.waitForResponse('/api/v1/offsite/inspect');
      await offsite.getByRole('button', { name: 'Check this storage works' }).click();
      expect((await inspectResponse).status()).toBe(200);
      // The launcher has no live S3 client, so it cannot confirm versioning and
      // must require an explicit acknowledgement rather than claim it is on.
      await expect(offsite.getByText('Could not be checked')).toBeVisible();
      await offsite.getByLabel(/older versions are kept/i).check();
      const planResponse = page.waitForResponse('/api/v1/offsite/plan');
      await offsite.getByRole('button', { name: 'Show me what will change' }).click();
      expect((await planResponse).status()).toBe(201);
      await expect(offsite.getByTestId('offsite-diff')).toContainText('community-backups');
      await expect(offsite.getByTestId('offsite-diff')).not.toContainText(offsiteSecret);
      await expect(page.getByText(offsiteSecret)).toHaveCount(0);
    }

    const accessibility = await new AxeBuilder({ page }).analyze();
    expect(accessibility.violations).toEqual([]);

    const createPlan = page.getByRole('button', { name: journey.plan });
    if (await createPlan.isVisible()) await createPlan.click();
    await expect(page.getByTestId('plan-digest')).not.toBeEmpty();
    await page.getByRole('button', { name: journey.approve }).click();
    await expect(page.getByRole('status')).toContainText(journey.verified);

    await page.reload();
    await expect(page.getByRole('heading', { name: journey.profileName })).toBeVisible();
    await expect(page.getByRole('status')).toContainText(journey.verified);
  }

  await page.setViewportSize({ width: 375, height: 667 });
  const createAnother = page.getByRole('button', { name: /weitere installation einrichten/i });
  await expect(createAnother).toBeVisible();
  const bounds = await createAnother.boundingBox();
  expect(bounds).not.toBeNull();
  expect((bounds?.x ?? 0) + (bounds?.width ?? 0)).toBeLessThanOrEqual(375);
  await createAnother.focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('region', { name: /neue installation einrichten/i })).toBeVisible();

  const mobileAccessibility = await new AxeBuilder({ page }).analyze();
  expect(mobileAccessibility.violations).toEqual([]);
});
