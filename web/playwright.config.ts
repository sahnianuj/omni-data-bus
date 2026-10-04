import { defineConfig, devices } from '@playwright/test'

/**
 * End-to-end suite (OC-034). Runs against the e2e compose stack (deploy/compose/e2e.yml) started
 * by scripts/e2e.sh: the built image as api + worker, the dev Couchbase and sample PostgreSQL.
 * Specs run one at a time (they share one Couchbase) and isolate data with unique users,
 * projects and e2e_<random> collections.
 */
const all = process.env.E2E_BROWSERS === 'all'

export default defineConfig({
  testDir: 'tests/e2e',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 180_000,
  expect: { timeout: 15_000 },
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  globalSetup: './tests/e2e/global-setup.ts',
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:18081',
    trace: 'retain-on-failure',
    // The trace already records every step with DOM snapshots; video only locally.
    video: process.env.CI ? 'off' : 'retain-on-failure',
    screenshot: 'only-on-failure',
    actionTimeout: 15_000,
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    ...(all
      ? [
          { name: 'firefox', use: { ...devices['Desktop Firefox'], viewport: { width: 1440, height: 900 } } },
          { name: 'webkit', use: { ...devices['Desktop Safari'], viewport: { width: 1440, height: 900 } } },
        ]
      : []),
  ],
})
