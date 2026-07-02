import { test, expect } from '@playwright/test';

const DOMAIN = process.env.DOMAIN!;
const EXCALIDRAW_URL = `https://excalidraw.${DOMAIN}`;

test.describe('Excalidraw', () => {
  test('loads the main canvas', async ({ page }) => {
    await page.goto(EXCALIDRAW_URL);

    // Wait for the excalidraw container to load. Excalidraw uses a canvas element.
    const canvas = page.locator('canvas');
    await expect(canvas.first()).toBeVisible({ timeout: 15_000 });
    
    // Check if the title is roughly correct
    await expect(page).toHaveTitle(/Excalidraw/i);
  });
});
