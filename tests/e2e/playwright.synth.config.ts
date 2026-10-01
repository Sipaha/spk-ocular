import { defineConfig } from '@playwright/test'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { join } from 'node:path'
import { ONE, env, writeAtomic, noDockerEnv } from './fixtures'

// The log viewer, terminals and tunnels against the synthetic provider
// (--test-synthetic): no cluster needed, so it is part of `make check`.
const port = Number(process.env.E2E_SYNTH_PORT ?? 5192)
let root = process.env.E2E_SYNTH_ROOT
if (!root) {
  mkdirSync(join(import.meta.dirname, '.run'), { recursive: true })
  root = mkdtempSync(join(import.meta.dirname, '.run', 'synth-'))
  writeAtomic(env(root).one, ONE)
  process.env.E2E_SYNTH_ROOT = root
}
const e = env(root)
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

export default defineConfig({
  testDir: '.',
  testMatch: ['logs.spec.ts', 'agents.spec.ts', 'terminal.spec.ts', 'tunnels.spec.ts', 'actions.spec.ts', 'problems.spec.ts', 'palette.spec.ts', 'keyboard.spec.ts', 'generic.spec.ts', 'warm.spec.ts'],
  workers: 1,
  use: { baseURL: `http://127.0.0.1:${port}`, locale: 'en-US', screenshot: 'only-on-failure', permissions: ['clipboard-read', 'clipboard-write'] },
  webServer: {
    command: `${bin} --browser --port ${port} --test-api --test-synthetic`,
    url: `http://127.0.0.1:${port}/`,
    env: {
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      ...noDockerEnv,
      SPK_OCULAR_TEST_SYNTH_SECOND: '1',
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
    // SIGTERM, not the default SIGKILL: the app hangs up its terminals (no
    // shells left in the cluster) and frees its tunnels' ports.
    gracefulShutdown: { signal: 'SIGTERM', timeout: 5000 },
  },
})
