import { test, expect } from '@playwright/test';

test.describe('Jitsi Meet Smoke Test', () => {
  const domain = process.env.DOMAIN;
  
  test.beforeAll(() => {
    if (!domain) {
      throw new Error('DOMAIN environment variable must be set');
    }
  });

  test('Jitsi Meet loads successfully', async ({ page }) => {
    // Navigate to the Jitsi Meet subdomain
    const url = `https://meet.${domain}/`;
    
    // Ignore HTTPS errors since testing might occur on self-signed certs depending on environment
    const response = await page.goto(url, { waitUntil: 'networkidle' });
    
    // Expect a successful response (or redirect)
    expect(response).not.toBeNull();
    expect(response?.status()).toBeLessThan(400);

    // Verify Jitsi title is present
    await expect(page).toHaveTitle(/Jitsi/i);
    
    // Check that the main meeting input field or join button is present
    // The exact selector might depend on Jitsi version, but there's always a join button or input.
    const joinButton = page.locator('button[aria-label="Start meeting"], button[title="Start meeting"], input[id="enter_room_field"]');
    await expect(joinButton.first()).toBeVisible({ timeout: 15000 });
  });
});
