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

    const accessibility = await new AxeBuilder({ page }).analyze();
    expect(accessibility.violations).toEqual([]);

    await page.getByRole('button', { name: journey.plan }).click();
    await expect(page.getByTestId('plan-digest')).not.toBeEmpty();
    await page.getByRole('button', { name: journey.approve }).click();
    await expect(page.getByRole('status')).toContainText(journey.verified);

    await page.reload();
    await expect(page.getByRole('heading', { name: journey.profileName })).toBeVisible();
    await expect(page.getByRole('status')).toContainText(journey.verified);
  }
});
