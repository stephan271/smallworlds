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
      task: 'Verify launcher',
      plan: 'Inspect and plan',
      approve: 'Approve and run',
      verified: 'Verified'
    },
    {
      language: 'de',
      profileName: `Werkstatt ${Date.now()}`,
      next: 'Nächste empfohlene Aktion',
      task: 'Launcher überprüfen',
      plan: 'Prüfen und planen',
      approve: 'Genehmigen und ausführen',
      verified: 'Verifiziert'
    }
  ] as const;

  for (const journey of journeys) {
    await page.getByRole('button', { name: /create another profile|weiteres profil erstellen/i }).click();
    const profileForm = page.getByRole('region', { name: /create cluster profile|clusterprofil erstellen/i });
    await profileForm.getByLabel(/profile name|profilname/i).fill(journey.profileName);
    await profileForm.getByLabel(/language|sprache/i).selectOption(journey.language);
    await profileForm.getByLabel(/deployment mode|bereitstellungsmodus/i).selectOption('local-lan');
    const createButton = profileForm.getByRole('button', { name: /create profile|profil erstellen/i });
    await createButton.focus();
    await page.keyboard.press('Enter');

    await expect(page.getByRole('heading', { name: journey.profileName })).toBeVisible();
    await expect(page.getByText(journey.next)).toBeVisible();
    await expect(page.getByRole('heading', { name: journey.task })).toBeVisible();

    if (journey.language === 'en') {
      const capabilities = page.getByRole('region', { name: 'Cluster Capabilities' });
      await capabilities.getByLabel('Pinned SmallWorlds release').fill('v1.2.3');
      await capabilities.getByLabel('Private overlay repository').fill('https://github.com/example/private-overlay.git');
      await capabilities.getByLabel('Base domain').fill('home.example');
      const capabilityResponse = page.waitForResponse('/api/v1/capabilities/plan');
      await capabilities.getByRole('button', { name: 'Review GitOps overlay' }).click();
      expect((await capabilityResponse).status()).toBe(201);
      await expect(capabilities.getByTestId('overlay-diff')).toContainText('v1.2.3');
      await expect(capabilities.getByTestId('overlay-diff')).not.toContainText('secret');
    }

		const vault = page.getByRole('region', { name: /launcher vault|launcher-tresor/i });
		await expect(vault).toBeVisible();
		if (journey.language === 'en') {
			await expect(vault.getByText('Passphrase fallback')).toBeVisible();
			await vault.getByLabel('Vault passphrase').fill('playwright-vault-passphrase');
			await vault.getByRole('button', { name: 'Unlock vault' }).focus();
			await page.keyboard.press('Enter');
			await expect(vault.getByText('Unlocked', { exact: true })).toBeVisible();
			const secret = 'playwright-secret-must-not-render';
			await vault.getByLabel('Git provider token').fill(secret);
			await vault.getByLabel('Credential expiry').fill('2035-04-05T06:07:08Z');
			await vault.getByRole('button', { name: 'Store credential' }).click();
			await expect(vault.getByText('Current', { exact: true })).toBeVisible();
			await expect(page.getByText(secret)).toHaveCount(0);
			const recovery = page.getByRole('region', { name: 'Recovery Bundle' });
			await expect(recovery).toBeVisible();
			await recovery.getByLabel('Recovery passphrase').first().fill('playwright-recovery-passphrase');
			const download = page.waitForEvent('download');
			await recovery.getByRole('button', { name: 'Download encrypted bundle' }).click();
			await expect((await download).suggestedFilename()).toMatch(/-recovery\.bundle$/);
		} else {
			await expect(vault.getByText('Entsperrt', { exact: true })).toBeVisible();
			await vault.getByLabel('Git-Anbieter-Token').fill('erstes-geheimnis-darf-nicht-erscheinen');
			await vault.getByLabel('Ablaufdatum des Zugangsschlüssels').fill('2036-05-06T07:08:09Z');
			await vault.getByRole('button', { name: 'Zugangsschlüssel speichern' }).click();
			await vault.getByLabel('Git-Anbieter-Token').fill('ersatz-geheimnis-darf-nicht-erscheinen');
			await vault.getByRole('button', { name: 'Zugangsschlüssel ersetzen' }).click();
			await expect(vault.getByText('Aktuell', { exact: true })).toBeVisible();
			await vault.getByRole('button', { name: 'Zugangsschlüssel entfernen' }).click();
			await expect(vault.getByText('Kein Zugangsschlüssel gespeichert')).toBeVisible();
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
});
