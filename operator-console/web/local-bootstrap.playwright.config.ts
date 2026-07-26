import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './local-bootstrap-tests',
  timeout: 30_000,
  fullyParallel: false,
  // Every spec drives the same launcher process and its one on-disk store, so
  // two files running at once fight over the profile list.
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:4175',
    trace: 'retain-on-failure'
  },
  // A launch token is single-use per launcher process, so one server can hand
  // out exactly one browser session. Each spec file therefore gets its own.
  webServer: [
    {
      command: 'npm run build && cd .. && go run ./cmd/smallworlds-admin --port 4175 --data-dir .tmp/e2e-local-bootstrap --token local-bootstrap-token --no-browser',
      url: 'http://127.0.0.1:4175',
      reuseExistingServer: false,
      timeout: 120_000
    },
    {
      command: 'cd .. && go run ./cmd/smallworlds-admin --port 4176 --data-dir .tmp/e2e-foreign-install --token foreign-install-token --no-browser',
      url: 'http://127.0.0.1:4176',
      reuseExistingServer: false,
      timeout: 120_000
    }
  ]
});
