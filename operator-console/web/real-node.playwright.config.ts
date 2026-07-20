import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './real-node-tests',
  timeout: 75 * 60_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: process.env.SMALLWORLDS_ACCEPTANCE_URL,
    trace: 'off',
    screenshot: 'off',
    video: 'off'
  }
});
