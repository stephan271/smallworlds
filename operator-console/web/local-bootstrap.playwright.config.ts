import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './local-bootstrap-tests',
  timeout: 30_000,
  fullyParallel: false,
  use: {
    baseURL: 'http://127.0.0.1:4175',
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'npm run build && cd .. && go run ./cmd/smallworlds-admin --port 4175 --data-dir .tmp/e2e-local-bootstrap --token local-bootstrap-token --no-browser',
    url: 'http://127.0.0.1:4175',
    reuseExistingServer: false,
    timeout: 120_000
  }
});
