import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  fullyParallel: false,
  use: {
    baseURL: 'http://127.0.0.1:4174',
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'npm run build && cd .. && go run ./cmd/smallworlds-admin --port 4174 --data-dir .tmp/e2e --token e2e-token --no-browser',
    url: 'http://127.0.0.1:4174',
    reuseExistingServer: false,
    timeout: 120_000
  }
});
