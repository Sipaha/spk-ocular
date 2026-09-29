import { defineConfig } from '@playwright/test'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { join } from 'node:path'
import { ONE, env, writeAtomic } from './fixtures'

// The log viewer against the synthetic provider (--test-synthetic): no
// cluster needed, so it is part of `make check`.
const port = Number(process.env.E2E_LOGS_PORT ?? 5192)
let root = process.env.E2E_LOGS_ROOT
if (!root) {
  mkdirSync(join(import.meta.dirname, '.run'), { recursive: true })
  root = mkdtempSync(join(import.meta.dirname, '.run', 'logs-'))
  writeAtomic(env(root).one, ONE)
  process.env.E2E_LOGS_ROOT = root
}
const e = env(root)
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

export default defineConfig({
  testDir: '.',
  testMatch: 'logs.spec.ts',
  workers: 1,
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US', screenshot: 'only-on-failure', permissions: ['clipboard-read', 'clipboard-write'] },
  webServer: {
    command: `${bin} --browser --port ${port} --test-api --test-synthetic`,
    url: `http://127.0.0.1:${port}/`,
    env: {
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      KUBECONFIG: e.one,
      LANG: 'en_US.UTF-8',
      LANGUAGE: '',
      LC_ALL: '',
      LC_MESSAGES: '',
      HTTPS_PROXY: '',
      HTTP_PROXY: '',
      https_proxy: '',
      http_proxy: '',
    },
    reuseExistingServer: false,
  },
})
