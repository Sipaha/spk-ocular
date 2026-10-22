import { defineConfig } from '@playwright/test'
import { join } from 'node:path'
import { scratchRoot } from './fixtures'

// P19: the page snapshot survives an app restart (memo.spec.ts). No
// webServer: the spec starts and stops the app itself, twice, on one data
// dir — the shared-instance configs cannot restart mid-suite.
export default defineConfig({
  testDir: '.',
  outputDir: join(scratchRoot, 'playwright-memo'),
  testMatch: ['security-cluster-policy.spec.ts','security-policy.spec.ts','security-audit.spec.ts','security-cluster-audit.spec.ts','vpa.spec.ts','metrics-history.spec.ts','reliability.spec.ts','helm-diagnosis.spec.ts', 'certificate.spec.ts', 'view-presets.spec.ts', 'workspace-restart.spec.ts', 'memo.spec.ts', 'configurations.spec.ts'],
  workers: 1,
  timeout: 90_000,
  use: { locale: 'en-US', screenshot: 'only-on-failure' },
})
