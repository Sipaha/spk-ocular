import { defineConfig } from '@playwright/test'
import { join } from 'node:path'
import { scratchRoot } from './fixtures'

// P19: the page snapshot survives an app restart (memo.spec.ts). No
// webServer: the spec starts and stops the app itself, twice, on one data
// dir — the shared-instance configs cannot restart mid-suite.
export default defineConfig({
  testDir: '.',
  outputDir: join(scratchRoot, 'playwright-memo'),
  testMatch: ['memo.spec.ts', 'configurations.spec.ts'],
  workers: 1,
  timeout: 90_000,
  use: { locale: 'en-US', screenshot: 'only-on-failure' },
})
