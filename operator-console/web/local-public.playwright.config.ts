import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './real-public-tests',
  timeout: 10 * 60_000,
  workers: 1,
  reporter: [['html', { outputFolder: 'reports/local-public', open: 'never' }], ['list']],
  use: { trace: 'retain-on-failure', screenshot: 'only-on-failure' }
});
