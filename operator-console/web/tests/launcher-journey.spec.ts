import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';

/** The safe opens with this computer's own login where that works, so a prompt
 *  is a possibility rather than a certainty. Both outcomes are correct; what
 *  would not be correct is asking when the launcher did not have to. */
async function ensureVaultOpen(page: Page, passphrase: string): Promise<void> {
  const prompt = page.getByRole('region', { name: /password safe|passwort-tresor/i });
  if (!(await prompt.isVisible())) return;
  await prompt.getByLabel(/safe passphrase|tresor-passphrase/i).fill(passphrase);
  await prompt.getByRole('button', { name: /^(Open the safe|Tresor öffnen)$/ }).click();
  await expect(prompt).toHaveCount(0);
}

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
      verified: 'Verified',
      setup: 'Setting up',
      activity: 'What happened',
      manage: 'This installation'
    },
    {
      language: 'de',
      profileName: `Werkstatt ${Date.now()}`,
      next: 'Nächste empfohlene Aktion',
      task: 'Prüfen, ob diese Konsole funktioniert',
      plan: 'Zeigen, was passieren wird',
      approve: 'Genehmigen und starten',
      verified: 'Verifiziert',
      setup: 'Einrichtung',
      activity: 'Was passiert ist',
      manage: 'Diese Installation'
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
    // A new installation lands on setting up, with the one thing worth doing
    // next named before anything else on the screen.
    await expect(page.getByRole('tab', { name: journey.setup })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByText(journey.next)).toBeVisible();

    await ensureVaultOpen(page, 'playwright-vault-passphrase');

    if (journey.language === 'en') {
      // Phase B: what the community gets, and what it is called. The version and
      // the environment label are advanced — sensible values are already in.
      const capabilities = page.getByRole('region', { name: 'What the infrastructure will offer' });
      await capabilities.getByRole('button', { name: 'Advanced settings' }).click();
      await capabilities.getByLabel('SmallWorlds version').fill('v1.2.3');
      await capabilities.getByLabel('Your web address').fill('home.example');
      const capabilityResponse = page.waitForResponse('/api/v1/capabilities/plan');
      await capabilities.getByRole('button', { name: 'Work out what that needs' }).click();
      expect((await capabilityResponse).status()).toBe(201);
      await expect(capabilities.getByText('Estimated memory')).toBeVisible();

      // Phase C: the exact file listing appears where the repository is actually
      // written, immediately before the click that writes it — not three stages
      // earlier, where it could not yet name the right repository.
      const rail = page.getByRole('navigation', { name: 'Setup progress' });
      await rail.getByRole('button', { name: /Choose where your settings are kept/ }).click();
      const settingsRepo = page.getByRole('region', { name: 'Choose where your settings are kept' });
      await settingsRepo.getByLabel('Create a new private repository on GitHub').check();
      await expect(settingsRepo.getByTestId('overlay-diff')).toContainText('v1.2.3');
      // A secret's value must never appear in the diff. Its NAME may: the overlay
      // points Grafana at an existing Secret and names the one cert-manager writes
      // a certificate into. So assert on a key carrying a value, not on the word.
      // Anchored to the start of a line so existingSecret: and secretName: are not
      // mistaken for a key of their own.
      await expect(settingsRepo.getByTestId('overlay-diff')).not.toContainText(/(^|\n)[\t -]*(password|token|secret)[\t ]*:[\t ]*\S/i);

      // Phase G: reaching a stage ahead of its prerequisites is allowed — the
      // stage says what is still missing rather than refusing to open.
      await rail.getByRole('button', { name: /Protect against losing the machine/ }).click();
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

    // The rehearsal lives with the diagnostics, not in the middle of the
    // journey: nobody asks whether the console works until something else has
    // already gone wrong. Shutting the installation down is next to it, and
    // says plainly that there is nothing to shut down yet.
    await page.getByRole('tab', { name: journey.manage }).click();
    const managePanel = page.getByRole('tabpanel');
    await expect(managePanel.getByRole('heading', { name: journey.task })).toBeVisible();
    if (journey.language === 'en') {
      await expect(managePanel.getByText('There is nothing to shut down')).toBeVisible();
      // The safe reports what it holds and asks for nothing: every secret is
      // collected by the step that needs it.
      const credentials = page.getByRole('region', { name: 'What the safe holds' });
      await expect(credentials.getByText('Nothing has to be entered here', { exact: false })).toBeVisible();
      await expect(credentials.locator('input')).toHaveCount(0);

      const recovery = page.getByRole('region', { name: 'Recovery Bundle' });
      await recovery.getByLabel('Recovery passphrase').first().fill('playwright-recovery-passphrase');
      const download = page.waitForEvent('download');
      await recovery.getByRole('button', { name: 'Download encrypted bundle' }).click();
      await expect((await download).suggestedFilename()).toMatch(/-recovery\.bundle$/);
    }

    await page.getByRole('button', { name: journey.plan }).click();
    await expect(page.getByTestId('plan-digest')).not.toBeEmpty();
    await page.getByRole('button', { name: journey.approve }).click();
    await expect(page.getByRole('status')).toContainText(journey.verified);

    // Every action ends up in one record for the whole installation, whichever
    // place it was started from.
    await page.getByRole('tab', { name: journey.activity }).click();
    await expect(page.getByRole('tabpanel').getByRole('listitem').first()).toBeVisible();

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
